package model

import (
	"time"
)

// 消费日志的聚合统计（LTZY-API 新增）。
//
// 用途：模型广场需要展示「速率」。这里用最近一段时间窗的消费日志算
// 「平均生成速率 = 输出 token 总量 ÷ 总耗时」，单位 tokens/秒。
//
// 口径说明（避免误读）：
//   - 耗时用的是端到端 elapsed_time，包含排队与首 token 等待，
//     因此这个值是「体感速率」而不是「峰值解码速率」，用于横向比较不同模型/渠道更合适。
//   - 只统计 completion_tokens > 0 且 elapsed_time > 0 的消费日志，
//     否则会把「失败请求的 0 token」算进分母，把速率拉低。
func GetModelThroughput(windowSeconds int64) (map[string]float64, error) {
	if windowSeconds <= 0 {
		windowSeconds = 24 * 3600
	}
	since := time.Now().Unix() - windowSeconds
	type throughputRow struct {
		ModelName        string
		CompletionTokens int64
		ElapsedMs        int64
	}
	rows := make([]throughputRow, 0)
	err := LOG_DB.Model(&Log{}).
		Select("model_name, SUM(completion_tokens) AS completion_tokens, SUM(elapsed_time) AS elapsed_ms").
		Where("type = ? AND created_at >= ? AND elapsed_time > 0 AND completion_tokens > 0",
			LogTypeConsume, since).
		Group("model_name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]float64, len(rows))
	for _, row := range rows {
		if row.ElapsedMs <= 0 || row.CompletionTokens <= 0 {
			continue
		}
		result[row.ModelName] = float64(row.CompletionTokens) / (float64(row.ElapsedMs) / 1000.0)
	}
	return result, nil
}
