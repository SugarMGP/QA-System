package user

import (
	"errors"
	"image"
	"mime/multipart"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"QA-System/internal/dao"
	"QA-System/internal/model"
	"QA-System/internal/pkg/code"
	"QA-System/internal/pkg/utils"
	"QA-System/internal/service"
	"github.com/dustin/go-humanize"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/zjutjh/WeJH-SDK/oauth"
	"github.com/zjutjh/WeJH-SDK/oauth/oauthException"
	"go.uber.org/zap"
)

type submitSurveyData struct {
	ID            int                 `json:"id" binding:"required"`
	Token         string              `json:"token"`
	QuestionsList []dao.QuestionsList `json:"questions_list"`
}

// SubmitSurvey 提交问卷
func SubmitSurvey(c *gin.Context) {
	var data submitSurveyData
	err := c.ShouldBindJSON(&data)
	if err != nil {
		code.AbortWithException(c, code.ParamError, err)
		return
	}
	// 判断问卷问题和答卷问题数目是否一致
	survey, err := service.GetSurveyByID(data.ID)
	if err != nil {
		code.AbortWithException(c, code.ServerError, err)
		return
	}
	var userInfo oauth.UserInfo
	if survey.Verify {
		userInfo, err = utils.ParseJWT(data.Token)
		if err != nil {
			code.AbortWithException(c, code.ServerError, err)
			return
		}
	}
	stuId := userInfo.StudentID
	questions, err := service.GetQuestionsBySurveyID(survey.ID)
	if err != nil {
		code.AbortWithException(c, code.ServerError, err)
		return
	}
	if len(questions) != len(data.QuestionsList) {
		code.AbortWithException(c, code.SurveyError, errors.New("问卷问题和上传问题数量不一致"))
		return
	}
	// 判断填写时间是否在问卷有效期内
	if !survey.Deadline.IsZero() && survey.Deadline.Before(time.Now()) {
		code.AbortWithException(c, code.TimeBeyondError, errors.New("填写时间已过"))
		return
	}
	if !survey.StartTime.IsZero() && survey.StartTime.After(time.Now()) {
		code.AbortWithException(c, code.TimeBeyondError, errors.New("填写时间未到"))
		return
	}
	// 判断问卷是否开放
	if survey.Status != 2 {
		code.AbortWithException(c, code.SurveyNotOpen, errors.New("问卷未开放"))
		return
	}
	// 逐个判断问题答案
	for _, q := range data.QuestionsList {
		question, err := service.GetQuestionByID(q.QuestionID)
		if err != nil {
			code.AbortWithException(c, code.ServerError, err)
			return
		}
		if question.SurveyID != survey.ID {
			code.AbortWithException(c, code.ServerError,
				errors.New("问题"+strconv.Itoa(question.SerialNum)+"不属于该问卷"))
			return
		}
		// 判断必填字段是否为空
		if question.Required && q.Answer == "" {
			code.AbortWithException(c, code.ServerError,
				errors.New("问题"+strconv.Itoa(q.QuestionID)+"必填字段为空"))
			return
		}
		// 判断多选题选项数量是否符合要求
		if (question.QuestionType == 2 && survey.Type == 0) || (question.QuestionType == 1 && survey.Type == 1) {
			length := uint(len(strings.Split(q.Answer, "┋")))
			if question.MinimumOption != 0 && length < question.MinimumOption {
				code.AbortWithException(c, code.OptionNumError, errors.New("问题"+strconv.Itoa(q.QuestionID)+"选项数量不符合要求"))
				return
			}
			if question.MaximumOption != 0 && length > question.MaximumOption {
				code.AbortWithException(c, code.OptionNumError, errors.New("问题"+strconv.Itoa(q.QuestionID)+"选项数量不符合要求"))
				return
			}
		}
	}
	flagSum, flagDay := false, false

	if survey.Verify {
		var err error
		if userInfo.UserTypeDesc != "本科生" {
			code.AbortWithException(c, code.NotUnderGraduateError, errors.New("当前问卷仅允许本科生回答"))
			return
		}
		// 统一检查总投票次数和每日投票次数
		if flagSum, err = service.CheckLimit(c, stuId, survey, "sumLimit", int(survey.SumLimit)); err != nil {
			if err.Error() == "sumLimit已达上限" {
				code.AbortWithException(c, code.VoteSumLimitError, errors.New("总投票次数已达上限"))
			} else {
				code.AbortWithException(c, code.ServerError, err)
			}
			return
		}

		if flagDay, err = service.CheckLimit(c, stuId, survey, "dailyLimit", int(survey.DailyLimit)); err != nil {
			if err.Error() == "dailyLimit已达上限" {
				code.AbortWithException(c, code.VoteLimitError, errors.New("单日投票次数已达上限"))
			} else {
				code.AbortWithException(c, code.ServerError, err)
			}
			return
		}
	}
	err = service.SubmitSurvey(data.ID, data.QuestionsList, time.Now().Format("2006-01-02 15:04:05"))
	if err != nil {
		code.AbortWithException(c, code.ServerError, err)
		return
	}

	if survey.Verify {
		if survey.DailyLimit > 0 {
			err := service.UpdateVoteLimit(c, stuId, survey.ID, flagDay, "dailyLimit")
			if err != nil {
				code.AbortWithException(c, code.ServerError, err)
				return
			}
		}
		if survey.SumLimit > 0 {
			err := service.UpdateVoteLimit(c, stuId, survey.ID, flagSum, "sumLimit")
			if err != nil {
				code.AbortWithException(c, code.ServerError, err)
				return
			}
		}
		// 记录授权
		if err = service.CreateOauthRecord(userInfo, time.Now(), data.ID); err != nil {
			code.AbortWithException(c, code.ServerError, err)
			return
		}
	}
	utils.JsonSuccessResponse(c, nil)
}

type getSurveyData struct {
	ID int `form:"id" binding:"required"`
}

// GetSurvey 用户获取问卷
func GetSurvey(c *gin.Context) {
	var data getSurveyData
	err := c.ShouldBindQuery(&data)
	if err != nil {
		code.AbortWithException(c, code.ParamError, err)
		return
	}
	// 获取问卷
	survey, err := service.GetSurveyByID(data.ID)
	if err != nil {
		code.AbortWithException(c, code.ServerError, err)
		return
	}
	// 判断填写时间是否在问卷有效期内
	if !survey.Deadline.IsZero() && survey.Deadline.Before(time.Now()) {
		code.AbortWithException(c, code.TimeBeyondError, errors.New("问卷填写时间已截至"))
		return
	}
	// 判断问卷是否开放
	if survey.Status != 2 {
		code.AbortWithException(c, code.SurveyNotOpen, errors.New("问卷未开放"))
		return
	}
	if survey.StartTime.IsZero() && survey.StartTime.After(time.Now()) {
		code.AbortWithException(c, code.SurveyNotOpen, errors.New("问卷未开放"))
		return
	}
	// 获取相应的问题
	questions, err := service.GetQuestionsBySurveyID(survey.ID)
	if err != nil {
		code.AbortWithException(c, code.ServerError, err)
		return
	}
	// 构建问卷响应
	questionListsResponse := make([]map[string]any, 0)
	for _, question := range questions {
		options, err := service.GetOptionsByQuestionID(question.ID)
		if err != nil {
			code.AbortWithException(c, code.ServerError, err)
			return
		}
		optionsResponse := make([]map[string]any, 0)
		for _, option := range options {
			optionResponse := map[string]any{
				"img":         option.Img,
				"content":     option.Content,
				"description": option.Description,
				"serial_num":  option.SerialNum,
			}
			optionsResponse = append(optionsResponse, optionResponse)
		}

		questionSettingResponse := map[string]any{
			"required":       question.Required,
			"unique":         question.Unique,
			"other_option":   question.OtherOption,
			"question_type":  question.QuestionType,
			"reg":            question.Reg,
			"maximum_option": question.MaximumOption,
			"minimum_option": question.MinimumOption,
		}

		questionListMap := map[string]any{
			"id":           question.ID,
			"serial_num":   question.SerialNum,
			"subject":      question.Subject,
			"description":  question.Description,
			"img":          question.Img,
			"ques_setting": questionSettingResponse,
			"options":      optionsResponse,
		}
		questionListsResponse = append(questionListsResponse, questionListMap)
	}

	questionsConfigResponse := map[string]any{
		"title":         survey.Title,
		"desc":          survey.Desc,
		"question_list": questionListsResponse,
	}
	baseConfigResponse := map[string]any{
		"start_time": survey.StartTime,
		"end_time":   survey.Deadline,
		"day_limit":  survey.DailyLimit,
		"sum_limit":  survey.SumLimit,
		"verify":     survey.Verify,
	}
	response := map[string]any{
		"id":          survey.ID,
		"status":      survey.Status,
		"survey_type": survey.Type,
		"base_config": baseConfigResponse,
		"ques_config": questionsConfigResponse,
	}

	utils.JsonSuccessResponse(c, response)
}

// UploadImg 上传图片
func UploadImg(c *gin.Context) {
	// 获取文件
	fileHeader, err := c.FormFile("img")
	if err != nil {
		code.AbortWithException(c, code.ParamError, err)
		return
	}

	// 检查文件大小是否超出限制
	if fileHeader.Size > 10*humanize.MiByte {
		code.AbortWithException(c, code.FileSizeError, err)
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		code.AbortWithException(c, code.ServerError, err)
		return
	}
	defer func(file multipart.File) {
		err := file.Close()
		if err != nil {
			zap.L().Error("Failed to close file", zap.Error(err))
		}
	}(file)

	reader, err := service.ConvertToJPEG(file)
	if errors.Is(err, image.ErrFormat) {
		code.AbortWithException(c, code.PictureError, err)
		return
	}
	if err != nil {
		code.AbortWithException(c, code.ServerError, err)
		return
	}

	// 保存图片
	filename := uuid.New().String() + ".jpg"
	dst := filepath.Join("./public/static/", filename)
	err = service.SaveFile(reader, dst)
	if err != nil {
		code.AbortWithException(c, code.ServerError, err)
		return
	}

	url := service.GetConfigUrl() + "/public/static/" + filename
	utils.JsonSuccessResponse(c, url)
}

// UploadFile 上传文件
func UploadFile(c *gin.Context) {
	// 获取文件
	fileHeader, err := c.FormFile("file")
	if err != nil {
		code.AbortWithException(c, code.ParamError, err)
		return
	}

	// 检查文件大小是否超出限制
	if fileHeader.Size > 50*humanize.MiByte {
		code.AbortWithException(c, code.FileSizeError, err)
		return
	}

	// 保存文件
	filename := uuid.New().String() + filepath.Ext(fileHeader.Filename)
	dst := filepath.Join("./public/file/", filename)
	err = c.SaveUploadedFile(fileHeader, dst)
	if err != nil {
		code.AbortWithException(c, code.ServerError, err)
		return
	}

	url := service.GetConfigUrl() + "/public/file/" + filename
	utils.JsonSuccessResponse(c, url)
}

type oauthData struct {
	StudentID string `json:"stu_id" binding:"required"`
	Password  string `json:"password" binding:"required"`
}

// Oauth 统一验证
func Oauth(c *gin.Context) {
	var data oauthData
	err := c.ShouldBindJSON(&data)
	if err != nil {
		code.AbortWithException(c, code.ParamError, err)
		return
	}
	user, err := service.Oauth(data.StudentID, data.Password)
	if err != nil {
		var oauthErr *oauthException.Error
		if !errors.As(err, &oauthErr) {
			code.AbortWithException(c, code.ServerError, err)
			return
		}

		switch {
		case errors.Is(oauthErr, oauthException.WrongPassword),
			errors.Is(oauthErr, oauthException.WrongAccount):
			code.AbortWithException(c, code.WrongOauthUsernameOrPassword, err)
		case errors.Is(oauthErr, oauthException.ClosedError):
			code.AbortWithException(c, code.OauthTimeError, err)
		default:
			code.AbortWithException(c, code.ServerError, err)
		}
		return
	}
	token := utils.NewJWT(user.Name, user.College, user.StudentID, user.UserType, user.UserTypeDesc, user.Gender)
	if token == "" {
		code.AbortWithException(c, code.ServerError, errors.New("统一验证失败原因: token生成失败"))
		return
	}
	utils.JsonSuccessResponse(c, gin.H{"token": token})
}

type getOptionCount struct {
	SerialNum int    `json:"serial_num"` // 选项序号
	Content   string `json:"content"`    // 选项内容
	Count     int    `json:"count"`      // 选项数量
	Rank      int    `json:"rank"`       // 选项排名
}

type getSurveyStatisticsResponse struct {
	SerialNum    int              `json:"serial_num"`    // 问题序号
	Question     string           `json:"question"`      // 问题内容
	QuestionType int              `json:"question_type"` // 问题类型  1:单选 2:多选
	Options      []getOptionCount `json:"options"`       // 选项内容
}

// GetSurveyStatistics 获取投票统计
func GetSurveyStatistics(c *gin.Context) {
	var data getSurveyData
	err := c.ShouldBindQuery(&data)
	if err != nil {
		code.AbortWithException(c, code.ParamError, err)
		return
	}
	survey, err := service.GetSurveyByID(data.ID)
	if err != nil {
		code.AbortWithException(c, code.ServerError, err)
		return
	}
	if survey.Type != 1 {
		code.AbortWithException(c, code.SurveyTypeError, errors.New("问卷为调研问卷"))
		return
	}
	answerSheets, err := service.GetSurveyAnswersBySurveyID(data.ID)
	if err != nil {
		code.AbortWithException(c, code.ServerError, err)
		return
	}

	questions, err := service.GetQuestionsBySurveyID(data.ID)
	if err != nil {
		code.AbortWithException(c, code.ServerError, err)
		return
	}

	// 如果 answerSheets 为空，则返回所有问题和选项统计为 0
	if len(answerSheets) == 0 {
		response := make([]getSurveyStatisticsResponse, 0, len(questions))
		for _, q := range questions {
			options, err := service.GetOptionsByQuestionID(q.ID)
			if err != nil {
				code.AbortWithException(c, code.ServerError, err)
				return
			}

			qOptions := make([]getOptionCount, 0, len(options)+1)
			for _, option := range options {
				qOptions = append(qOptions, getOptionCount{
					SerialNum: option.SerialNum,
					Content:   option.Content,
					Count:     0,
					Rank:      1,
				})
			}

			// 如果支持 "其他" 选项，添加一项
			if q.OtherOption {
				qOptions = append(qOptions, getOptionCount{
					SerialNum: 0,
					Content:   "其他",
					Count:     0,
					Rank:      1,
				})
			}

			response = append(response, getSurveyStatisticsResponse{
				SerialNum:    q.SerialNum,
				Question:     q.Subject,
				QuestionType: q.QuestionType,
				Options:      qOptions,
			})
		}
		utils.JsonSuccessResponse(c, gin.H{"statistics": response})
		return
	}

	// 问题编号对应的问题
	questionMap := make(map[int]model.Question)
	// 问题编号对应的选项们
	optionsMap := make(map[int][]model.Option)
	// 问题编号与选项内容对应的选项
	optionAnswerMap := make(map[int]map[string]model.Option)
	// 问题编号与选项序号对应的选项
	optionSerialNumMap := make(map[int]map[int]model.Option)
	for _, question := range questions {
		questionMap[question.ID] = question
		optionAnswerMap[question.ID] = make(map[string]model.Option)
		optionSerialNumMap[question.ID] = make(map[int]model.Option)
		options, err := service.GetOptionsByQuestionID(question.ID)
		if err != nil {
			code.AbortWithException(c, code.ServerError, err)
			return
		}
		optionsMap[question.ID] = options
		for _, option := range options {
			optionAnswerMap[question.ID][option.Content] = option
			optionSerialNumMap[question.ID][option.SerialNum] = option
		}
	}

	// 问题编号对应的选项编号对应的选项数量
	optionCounts := make(map[int]map[int]int)
	for _, sheet := range answerSheets {
		for _, answer := range sheet.Answers {
			options := optionsMap[answer.QuestionID]
			question := questionMap[answer.QuestionID]
			// 初始化选项统计（确保每个选项的计数存在且为 0）
			if _, initialized := optionCounts[question.ID]; !initialized {
				counts := ensureMap(optionCounts, question.ID)
				for _, option := range options {
					counts[option.SerialNum] = 0
				}
			}
			if question.QuestionType == 1 {
				answerOptions := strings.Split(answer.Content, "┋")
				questionOptions := optionAnswerMap[answer.QuestionID]
				for _, answerOption := range answerOptions {
					// 查找选项
					if questionOptions != nil {
						option, exists := questionOptions[answerOption]
						if exists {
							// 如果找到选项，处理逻辑
							ensureMap(optionCounts, answer.QuestionID)[option.SerialNum]++
							continue
						}
					}
					// 如果选项不存在，处理为 "其他" 选项
					ensureMap(optionCounts, answer.QuestionID)[0]++
				}
			}
		}
	}

	response := make([]getSurveyStatisticsResponse, 0, len(optionCounts))
	for qid, options := range optionCounts {
		q := questionMap[qid]
		var qOptions []getOptionCount
		if q.OtherOption {
			qOptions = make([]getOptionCount, 0, len(options)+1)
			// 添加其他选项
			qOptions = append(qOptions, getOptionCount{
				SerialNum: 0,
				Content:   "其他",
				Count:     options[0],
			})
		} else {
			qOptions = make([]getOptionCount, 0, len(options))
		}
		// 按序号排序
		sortedSerialNums := make([]int, 0, len(options))
		for oSerialNum := range options {
			sortedSerialNums = append(sortedSerialNums, oSerialNum)
		}
		sort.Ints(sortedSerialNums)
		for _, oSerialNum := range sortedSerialNums {
			count := options[oSerialNum]
			op := optionSerialNumMap[qid][oSerialNum]
			qOptions = append(qOptions, getOptionCount{
				SerialNum: op.SerialNum,
				Content:   op.Content,
				Count:     count,
			})
		}

		// 创建一个副本用于排序
		sortedQOptions := make([]getOptionCount, len(qOptions))
		copy(sortedQOptions, qOptions)

		// 按选项数量排序
		sort.Slice(sortedQOptions, func(i, j int) bool {
			// 按数量降序排列，数量相同时按序号升序排列
			if sortedQOptions[i].Count == sortedQOptions[j].Count {
				return sortedQOptions[i].SerialNum < sortedQOptions[j].SerialNum
			}
			return sortedQOptions[i].Count > sortedQOptions[j].Count
		})

		// 补充 rank
		rankMap := make(map[int]int) // 用于记录选项的排名
		currentRank := 1
		for i := 0; i < len(sortedQOptions); i++ {
			if i > 0 && sortedQOptions[i].Count < sortedQOptions[i-1].Count {
				// 当前排名等于前面所有项目数量
				currentRank = i + 1
			}
			rankMap[sortedQOptions[i].SerialNum] = currentRank
		}

		// 将排名写回原始的 qOptions
		for i := range qOptions {
			qOptions[i].Rank = rankMap[qOptions[i].SerialNum]
		}

		response = append(response, getSurveyStatisticsResponse{
			SerialNum:    q.SerialNum,
			Question:     q.Subject,
			QuestionType: q.QuestionType,
			Options:      qOptions,
		})
	}
	utils.JsonSuccessResponse(c, gin.H{"statistics": response})
}

func ensureMap(m map[int]map[int]int, key int) map[int]int {
	if m[key] == nil {
		m[key] = make(map[int]int)
	}
	return m[key]
}
