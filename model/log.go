package model

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/helper"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	billingratio "github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
)

type Log struct {
	Id                int    `json:"id"`
	UserId            int    `json:"user_id" gorm:"index"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index:idx_created_at_type"`
	Type              int    `json:"type" gorm:"index:idx_created_at_type"`
	Content           string `json:"content"`
	Username          string `json:"username" gorm:"index:index_username_model_name,priority:2;default:''"`
	TokenName         string `json:"token_name" gorm:"index;default:''"`
	ModelName         string `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	Quota             int    `json:"quota" gorm:"default:0"`
	PromptTokens      int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens  int    `json:"completion_tokens" gorm:"default:0"`
	ChannelId         int    `json:"channel" gorm:"index"`
	RequestId         string `json:"request_id" gorm:"default:''"`
	ElapsedTime       int64  `json:"elapsed_time" gorm:"default:0"` // unit is ms
	IsStream          bool   `json:"is_stream" gorm:"default:false"`
	SystemPromptReset bool   `json:"system_prompt_reset" gorm:"default:false"`
	// 上游成本（LTZY-API 新增，B7）：与 Quota（售价）同口径，便于直接算毛利。
	// 0 表示"未配置进价/未知成本"，不等价于"零成本"。
	CostQuota int64 `json:"cost_quota" gorm:"bigint;default:0"`
}

const (
	LogTypeUnknown = iota
	LogTypeTopup
	LogTypeConsume
	LogTypeManage
	LogTypeSystem
	LogTypeTest
)

func recordLogHelper(ctx context.Context, log *Log) {
	requestId := helper.GetRequestID(ctx)
	log.RequestId = requestId
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.Error(ctx, "failed to record log: "+err.Error())
		return
	}
	// 只打印固定字段的安全摘要，不再用 %+v 整个结构体：
	// Content 的来源包括上游报错原文与渠道测试响应，部分上游会在报错里回显密钥，
	// 整个结构体打印（或直接打印 Content）都可能把凭据带进日志文件。
	logger.Infof(ctx, "record log: id=%d type=%d user_id=%d model=%s channel=%d quota=%d content=%s",
		log.Id, log.Type, log.UserId, log.ModelName, log.ChannelId, log.Quota, common.MaskString(log.Content))
}

func RecordLog(ctx context.Context, userId int, logType int, content string) {
	if logType == LogTypeConsume && !config.LogConsumeEnabled {
		return
	}
	log := &Log{
		UserId:    userId,
		Username:  GetUsernameById(userId),
		CreatedAt: helper.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	recordLogHelper(ctx, log)
}

func RecordTopupLog(ctx context.Context, userId int, content string, quota int) {
	log := &Log{
		UserId:    userId,
		Username:  GetUsernameById(userId),
		CreatedAt: helper.GetTimestamp(),
		Type:      LogTypeTopup,
		Content:   content,
		Quota:     quota,
	}
	recordLogHelper(ctx, log)
}

func RecordConsumeLog(ctx context.Context, log *Log) {
	if !config.LogConsumeEnabled {
		return
	}
	log.Username = GetUsernameById(log.UserId)
	log.CreatedAt = helper.GetTimestamp()
	log.Type = LogTypeConsume
	// 上游成本（LTZY-API 新增，B7）：在这里统一算并落库，而不是散落到各结算出口。
	// 好处有两个：一处覆盖全部计费模式；成本是「当时」的进价，
	// 事后改 CostRatio 不会篡改历史账单（否则毛利报表会随配置漂移）。
	if log.CostQuota == 0 {
		log.CostQuota = billingratio.CalcCostQuota(log.ModelName, log.PromptTokens, log.CompletionTokens)
	}
	recordLogHelper(ctx, log)
}

func RecordTestLog(ctx context.Context, log *Log) {
	log.CreatedAt = helper.GetTimestamp()
	log.Type = LogTypeTest
	recordLogHelper(ctx, log)
}

func GetAllLogs(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, startIdx int, num int, channel int, tenantId int) (logs []*Log, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB
	} else {
		tx = LOG_DB.Where("type = ?", logType)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}
	// 租户过滤（C5）：tenantId=0 为平台视角，不过滤
	tx, err = scopeLogsByTenant(tx, tenantId)
	if err != nil {
		return nil, err
	}
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&logs).Error
	return logs, err
}

func GetUserLogs(userId int, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, startIdx int, num int) (logs []*Log, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB.Where("user_id = ?", userId)
	} else {
		tx = LOG_DB.Where("user_id = ? and type = ?", userId, logType)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Omit("id").Find(&logs).Error
	return logs, err
}

func SearchAllLogs(keyword string, tenantId int) (logs []*Log, err error) {
	tx, err := scopeLogsByTenant(LOG_DB, tenantId)
	if err != nil {
		return nil, err
	}
	err = tx.Where("type = ? or content LIKE ?", keyword, keyword+"%").Order("id desc").Limit(config.MaxRecentItems).Find(&logs).Error
	return logs, err
}

func SearchUserLogs(userId int, keyword string) (logs []*Log, err error) {
	err = LOG_DB.Where("user_id = ? and type = ?", userId, keyword).Order("id desc").Limit(config.MaxRecentItems).Omit("id").Find(&logs).Error
	return logs, err
}

func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int) (quota int64) {
	ifnull := "ifnull"
	if common.UsingPostgreSQL {
		ifnull = "COALESCE"
	}
	tx := LOG_DB.Table("logs").Select(fmt.Sprintf("%s(sum(quota),0)", ifnull))
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&quota)
	return quota
}

func SumUsedToken(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	ifnull := "ifnull"
	if common.UsingPostgreSQL {
		ifnull = "COALESCE"
	}
	tx := LOG_DB.Table("logs").Select(fmt.Sprintf("%s(sum(prompt_tokens),0) + %s(sum(completion_tokens),0)", ifnull, ifnull))
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

func DeleteOldLog(targetTimestamp int64) (int64, error) {
	result := LOG_DB.Where("created_at < ?", targetTimestamp).Delete(&Log{})
	return result.RowsAffected, result.Error
}

// ChannelStatistic 渠道维度的消费统计（LTZY-API 新增），用于渠道健康度看板。
type ChannelStatistic struct {
	ChannelId        int   `json:"channel_id" gorm:"column:channel_id"`
	RequestCount     int64 `json:"request_count" gorm:"column:request_count"`
	Quota            int64 `json:"quota" gorm:"column:quota"`
	CostQuota        int64 `json:"cost_quota" gorm:"column:cost_quota"` // 上游成本（B7）
	PromptTokens     int64 `json:"prompt_tokens" gorm:"column:prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens" gorm:"column:completion_tokens"`
	AvgElapsedTime   int64 `json:"avg_elapsed_time" gorm:"column:avg_elapsed_time"`
}

// GetChannelStatistics 按渠道聚合指定时间窗口内的消费日志。
// startTimestamp / endTimestamp 为 0 时表示不限制该边界。
func GetChannelStatistics(startTimestamp int64, endTimestamp int64, tenantId int) (stats []*ChannelStatistic, err error) {
	ifnull := "ifnull"
	if common.UsingPostgreSQL {
		ifnull = "COALESCE"
	}
	tx := LOG_DB.Table("logs").
		Select(fmt.Sprintf("channel_id, count(*) as request_count, %s(sum(quota),0) as quota, "+
			"%s(sum(cost_quota),0) as cost_quota, "+
			"%s(sum(prompt_tokens),0) as prompt_tokens, %s(sum(completion_tokens),0) as completion_tokens, "+
			"%s(avg(elapsed_time),0) as avg_elapsed_time", ifnull, ifnull, ifnull, ifnull, ifnull)).
		Where("type = ?", LogTypeConsume)
	tx, err = scopeLogsByTenant(tx, tenantId)
	if err != nil {
		return nil, err
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	err = tx.Group("channel_id").Scan(&stats).Error
	return stats, err
}

// ModelCostStatistic 模型维度的成本统计（LTZY-API 新增）。
type ModelCostStatistic struct {
	ModelName        string `json:"model_name" gorm:"column:model_name"`
	RequestCount     int64  `json:"request_count" gorm:"column:request_count"`
	Quota            int64  `json:"quota" gorm:"column:quota"`
	CostQuota        int64  `json:"cost_quota" gorm:"column:cost_quota"` // 上游成本（B7）
	PromptTokens     int64  `json:"prompt_tokens" gorm:"column:prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens" gorm:"column:completion_tokens"`
}

// UserCostStatistic 用户维度的成本统计（LTZY-API 新增）。
type UserCostStatistic struct {
	UserId       int    `json:"user_id" gorm:"column:user_id"`
	Username     string `json:"username" gorm:"column:username"`
	RequestCount int64  `json:"request_count" gorm:"column:request_count"`
	Quota        int64  `json:"quota" gorm:"column:quota"`
	CostQuota    int64  `json:"cost_quota" gorm:"column:cost_quota"` // 上游成本（B7）
}

// DailyCostStatistic 按日维度的成本趋势统计（LTZY-API 新增）。
type DailyCostStatistic struct {
	Day          string `json:"day" gorm:"column:day"`
	RequestCount int64  `json:"request_count" gorm:"column:request_count"`
	Quota        int64  `json:"quota" gorm:"column:quota"`
	CostQuota    int64  `json:"cost_quota" gorm:"column:cost_quota"` // 上游成本（B7）
	Tokens       int64  `json:"tokens" gorm:"column:tokens"`
}

// GetModelCostStatistics 按模型聚合指定时间窗口内的成本，按额度降序取前 limit 条。
func GetModelCostStatistics(startTimestamp int64, endTimestamp int64, limit int, tenantId int) (stats []*ModelCostStatistic, err error) {
	ifnull := "ifnull"
	if common.UsingPostgreSQL {
		ifnull = "COALESCE"
	}
	tx := LOG_DB.Table("logs").
		Select(fmt.Sprintf("model_name, count(*) as request_count, %s(sum(quota),0) as quota, "+
			"%s(sum(cost_quota),0) as cost_quota, "+
			"%s(sum(prompt_tokens),0) as prompt_tokens, %s(sum(completion_tokens),0) as completion_tokens",
			ifnull, ifnull, ifnull, ifnull)).
		Where("type = ?", LogTypeConsume)
	tx, err = scopeLogsByTenant(tx, tenantId)
	if err != nil {
		return nil, err
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	tx = tx.Group("model_name").Order("quota desc")
	if limit > 0 {
		tx = tx.Limit(limit)
	}
	err = tx.Scan(&stats).Error
	return stats, err
}

// GetUserCostStatistics 按用户聚合指定时间窗口内的成本，按额度降序取前 limit 条。
func GetUserCostStatistics(startTimestamp int64, endTimestamp int64, limit int, tenantId int) (stats []*UserCostStatistic, err error) {
	ifnull := "ifnull"
	if common.UsingPostgreSQL {
		ifnull = "COALESCE"
	}
	tx := LOG_DB.Table("logs").
		Select(fmt.Sprintf("user_id, username, count(*) as request_count, %s(sum(quota),0) as quota, "+
			"%s(sum(cost_quota),0) as cost_quota", ifnull, ifnull)).
		Where("type = ?", LogTypeConsume)
	tx, err = scopeLogsByTenant(tx, tenantId)
	if err != nil {
		return nil, err
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	tx = tx.Group("user_id, username").Order("quota desc")
	if limit > 0 {
		tx = tx.Limit(limit)
	}
	err = tx.Scan(&stats).Error
	return stats, err
}

// GetDailyCostStatistics 按天聚合指定时间窗口内的成本，用于趋势图。
func GetDailyCostStatistics(startTimestamp int64, endTimestamp int64, tenantId int) (stats []*DailyCostStatistic, err error) {
	groupSelect := "DATE_FORMAT(FROM_UNIXTIME(created_at), '%Y-%m-%d')"
	if common.UsingPostgreSQL {
		groupSelect = "TO_CHAR(date_trunc('day', to_timestamp(created_at)), 'YYYY-MM-DD')"
	}
	if common.UsingSQLite {
		groupSelect = "strftime('%Y-%m-%d', datetime(created_at, 'unixepoch'))"
	}
	ifnull := "ifnull"
	if common.UsingPostgreSQL {
		ifnull = "COALESCE"
	}
	tx := LOG_DB.Table("logs").
		Select(fmt.Sprintf("%s as day, count(*) as request_count, %s(sum(quota),0) as quota, "+
			"%s(sum(cost_quota),0) as cost_quota, "+
			"%s(sum(prompt_tokens) + sum(completion_tokens),0) as tokens",
			groupSelect, ifnull, ifnull, ifnull)).
		Where("type = ?", LogTypeConsume)
	tx, err = scopeLogsByTenant(tx, tenantId)
	if err != nil {
		return nil, err
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	err = tx.Group("day").Order("day asc").Scan(&stats).Error
	return stats, err
}

type LogStatistic struct {
	Day              string `gorm:"column:day"`
	ModelName        string `gorm:"column:model_name"`
	RequestCount     int    `gorm:"column:request_count"`
	Quota            int    `gorm:"column:quota"`
	PromptTokens     int    `gorm:"column:prompt_tokens"`
	CompletionTokens int    `gorm:"column:completion_tokens"`
}

func SearchLogsByDayAndModel(userId, start, end int) (LogStatistics []*LogStatistic, err error) {
	groupSelect := "DATE_FORMAT(FROM_UNIXTIME(created_at), '%Y-%m-%d') as day"

	if common.UsingPostgreSQL {
		groupSelect = "TO_CHAR(date_trunc('day', to_timestamp(created_at)), 'YYYY-MM-DD') as day"
	}

	if common.UsingSQLite {
		groupSelect = "strftime('%Y-%m-%d', datetime(created_at, 'unixepoch')) as day"
	}

	err = LOG_DB.Raw(`
		SELECT `+groupSelect+`,
		model_name, count(1) as request_count,
		sum(quota) as quota,
		sum(prompt_tokens) as prompt_tokens,
		sum(completion_tokens) as completion_tokens
		FROM logs
		WHERE type=2
		AND user_id= ?
		AND created_at BETWEEN ? AND ?
		GROUP BY day, model_name
		ORDER BY day, model_name
	`, userId, start, end).Scan(&LogStatistics).Error

	return LogStatistics, err
}
