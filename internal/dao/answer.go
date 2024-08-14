package dao

import (
	"QA-System/internal/pkg/log"
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Answer struct {
	QuestionID int    `json:"question_id" bson:"questionid"` // 问题ID
	SerialNum  int    `json:"serial_num" bson:"serialnum"`   // 问题序号
	Subject    string `json:"subject" bson:"subject"`        // 问题标题
	Content    string `json:"content" bson:"content"`        // 答案内容
}

type AnswerSheet struct {
	SurveyID int      `json:"survey_id" bson:"surveyid"` // 问卷ID
	Time     string   `json:"time" bson:"time"`          // 答卷时间
	Unique   bool     `json:"unique" bson:"unique"`      // 是否唯一
	Answers  []Answer `json:"answers" bson:"answers"`    // 答案列表
}

type QuestionAnswers struct {
	Title        string   `json:"title"`
	QuestionType int      `json:"question_type"`
	Answers      []string `json:"answers"`
}

type AnswersResonse struct {
	QuestionAnswers []QuestionAnswers `json:"question_answers"`
	Time            []string          `json:"time"`
}

// SaveAnswerSheet 将答卷直接保存到 MongoDB 集合中
func (d *Dao) SaveAnswerSheet(ctx context.Context, answerSheet AnswerSheet, qids []int) error {
	// 构建查询条件
	matchConditions := make([]bson.M, 0) // 初始化为空切片
	for _, answer := range answerSheet.Answers {
		if contains(qids, answer.QuestionID) {
			matchConditions = append(matchConditions, bson.M{
				"answers": bson.M{
					"$elemMatch": bson.M{
						"questionid": answer.QuestionID,
						"content":    answer.Content,
					},
				},
			})
		}
	}

	if len(matchConditions) == 0 {
		// 没有符合条件的记录，直接插入新记录
		_, err := d.mongo.InsertOne(ctx, answerSheet)
		if err != nil {
			return err
		}
		log.Logger.Info("没有找到符合条件的记录，已新增一条记录")
		return nil
	}

	filter := bson.M{
		"unique": true,
		"$or":    matchConditions,
	}

	var result AnswerSheet
	err := d.mongo.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			// 没有找到符合条件的记录，直接插入新记录
			_, err := d.mongo.InsertOne(ctx, answerSheet)
			if err != nil {
				return err
			}
			log.Logger.Info("没有找到符合条件的记录，已新增一条记录")
			return nil
		}
		return err
	}

	// 更新找到的记录，将unique设为false
	update := bson.M{
		"$set": bson.M{"unique": false},
	}
	_, err = d.mongo.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}

	// 新增一条记录
	newAnswerSheet := AnswerSheet{
		SurveyID: answerSheet.SurveyID,
		Time:     answerSheet.Time,
		Unique:   true,
		Answers:  answerSheet.Answers,
	}

	_, err = d.mongo.InsertOne(ctx, newAnswerSheet)
	if err != nil {
		return err
	}

	log.Logger.Info("更新并新增记录成功")
	return nil
}

func contains(arr []int, item int) bool {
	for _, a := range arr {
		if a == item {
			return true
		}
	}
	return false
}

// GetAnswerSheetBySurveyID 根据问卷ID分页获取答卷
func (d *Dao) GetAnswerSheetBySurveyID(ctx context.Context, surveyID int, pageNum int, pageSize int, text string, unique bool) ([]AnswerSheet, *int64, error) {
	var answerSheets []AnswerSheet
	filter := bson.M{"surveyid": surveyID}

	// 如果 text 不为空，添加 text 的查询条件
	if text != "" {
		filter["answers.content"] = bson.M{"$regex": text, "$options": "i"} // i 表示不区分大小写
	}

	// 如果 unique 为 true，添加 unique 的查询条件
	if unique {
		filter["unique"] = true
	}

	// 设置总记录数查询过滤条件和选项
	countFilter := filter
	countOpts := options.Count()

	// 执行总记录数查询
	total, err := d.mongo.CountDocuments(ctx, countFilter, countOpts)
	if err != nil {
		return nil, nil, err
	}

	// 设置分页查询选项
	opts := options.Find()
	if pageNum != 0 && pageSize != 0 {
		opts.SetSkip(int64((pageNum - 1) * pageSize)) // 计算要跳过的文档数
		opts.SetLimit(int64(pageSize))                // 设置返回的文档数
	}

	// 执行分页查询
	cur, err := d.mongo.Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, err
	}
	defer cur.Close(ctx)

	// 迭代查询结果
	for cur.Next(ctx) {
		var answerSheet AnswerSheet
		if err := cur.Decode(&answerSheet); err != nil {
			return nil, nil, err
		}
		answerSheets = append(answerSheets, answerSheet)
	}
	if err := cur.Err(); err != nil {
		return nil, nil, err
	}
	// 返回分页数据和总记录数
	return answerSheets, &total, nil
}

// DeleteAnswerSheetBySurveyID 根据问卷ID删除答卷
func (d *Dao) DeleteAnswerSheetBySurveyID(ctx context.Context, surveyID int) error {
	filter := bson.M{"surveyid": surveyID}
	// 删除所有满足条件的文档
	_, err := d.mongo.DeleteMany(ctx, filter)
	return err
}