package model

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/logger"
)

// 预算与熔断（LTZY-API 新增，B6）。
//
// 目标：给「用户 / 令牌」设置日/月额度上限，超出即拒绝新请求（熔断），
// 避免某个用户的脚本失控或密钥被盗后在短时间内把额度烧穿。
//
// 存储设计：单独的 quota_budgets 计数表，而不是每次 SUM(logs)：
//   - 不依赖 LogConsumeEnabled（关掉消费日志后预算依然有效）
//   - O(1) 读写，不随日志量增长
//   - 复合主键含 period_key，跨周期自动「归零」——新的一天/月是新的
//     period_key，无需定时任务清理历史计数
//
// 一致性取舍：用量在「结算时」累加，校验发生在「请求前」，因此是软限制，
// 允许少量超额（并发请求 + 结算异步）。这与 One API 既有的预扣-结算模型一致：
// 硬性不超额由用户剩余额度（quota）保证，预算用于控制「单位时间内的消耗速度」。

const (
	BudgetSubjectUser  = "user"
	BudgetSubjectToken = "token"

	BudgetPeriodDaily   = "daily"
	BudgetPeriodMonthly = "monthly"
)

// QuotaBudget 某主体在某周期的累计消费计数。
type QuotaBudget struct {
	SubjectType string `json:"subject_type" gorm:"primaryKey;type:varchar(16)"`
	SubjectId   int    `json:"subject_id" gorm:"primaryKey"`
	Period      string `json:"period" gorm:"primaryKey;type:varchar(16)"`
	PeriodKey   string `json:"period_key" gorm:"primaryKey;type:varchar(16)"`
	UsedQuota   int64  `json:"used_quota" gorm:"bigint;default:0"`
	UpdatedAt   int64  `json:"updated_at" gorm:"bigint"`
}

// BudgetPeriodKey 生成周期键：日 -> 20060102，月 -> 200601。
// 用本地时区，与运营视角的「今天/本月」一致。
func BudgetPeriodKey(period string, t time.Time) string {
	if period == BudgetPeriodMonthly {
		return t.Format("200601")
	}
	return t.Format("20060102")
}

// RecordBudgetUsage 把一次消费累加到「用户 + 令牌」的日/月计数（B6）。
//
// 性能：四个计数用**单条多行 UPSERT** 写入（同一 delta），避免每请求 4 次独立写。
// 挂在各结算出口之后调用（text / rerank / responses / video / image）。
func RecordBudgetUsage(userId int, tokenId int, quota int64) {
	if quota <= 0 {
		return
	}
	now := time.Now()
	nowTs := now.Unix()
	dailyKey := BudgetPeriodKey(BudgetPeriodDaily, now)
	monthlyKey := BudgetPeriodKey(BudgetPeriodMonthly, now)

	entries := make([]QuotaBudget, 0, 4)
	// 注意：UsedQuota 必须带上本次增量 —— INSERT（首次）路径写入该值，
	// 冲突路径则由 ON CONFLICT 的 used_quota = used_quota + ? 累加。
	// 若这里留空，首次消费会被写成 0，只有后续消费才会计数（曾踩过）。
	newEntry := func(subjectType string, subjectId int, period string, periodKey string) QuotaBudget {
		return QuotaBudget{
			SubjectType: subjectType,
			SubjectId:   subjectId,
			Period:      period,
			PeriodKey:   periodKey,
			UsedQuota:   quota,
			UpdatedAt:   nowTs,
		}
	}
	if userId > 0 {
		entries = append(entries,
			newEntry(BudgetSubjectUser, userId, BudgetPeriodDaily, dailyKey),
			newEntry(BudgetSubjectUser, userId, BudgetPeriodMonthly, monthlyKey),
		)
	}
	if tokenId > 0 {
		entries = append(entries,
			newEntry(BudgetSubjectToken, tokenId, BudgetPeriodDaily, dailyKey),
			newEntry(BudgetSubjectToken, tokenId, BudgetPeriodMonthly, monthlyKey),
		)
	}
	if len(entries) == 0 {
		return
	}

	err := DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "subject_type"}, {Name: "subject_id"}, {Name: "period"}, {Name: "period_key"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"used_quota": gorm.Expr("used_quota + ?", quota),
			"updated_at": nowTs,
		}),
	}).Create(&entries).Error
	if err != nil {
		// 预算是旁路能力：计数失败不应影响计费主流程，记日志便于发现断流
		logger.SysError("failed to record budget usage: " + err.Error())
	}
}

// GetBudgetUsages 一次查询取回某主体的日/月用量（单条 SQL，含主键索引）。
// 不设缓存：主键点查很便宜，而缓存会让熔断判断滞后。
func GetBudgetUsages(subjectType string, subjectId int, now time.Time) (daily int64, monthly int64, err error) {
	if subjectId == 0 {
		return 0, 0, nil
	}
	dailyKey := BudgetPeriodKey(BudgetPeriodDaily, now)
	monthlyKey := BudgetPeriodKey(BudgetPeriodMonthly, now)
	var rows []QuotaBudget
	err = DB.Where(
		"subject_type = ? AND subject_id = ? AND ((period = ? AND period_key = ?) OR (period = ? AND period_key = ?))",
		subjectType, subjectId, BudgetPeriodDaily, dailyKey, BudgetPeriodMonthly, monthlyKey,
	).Find(&rows).Error
	if err != nil {
		return 0, 0, err
	}
	for _, row := range rows {
		if row.Period == BudgetPeriodDaily {
			daily = row.UsedQuota
		} else if row.Period == BudgetPeriodMonthly {
			monthly = row.UsedQuota
		}
	}
	return daily, monthly, nil
}

// GetDefaultUserBudgetLimits 读取「全局用户级」日/月预算上限（0 = 不限制）。
// 走内存 OptionMap，热更新即时生效，且不产生任何数据库查询。
func GetDefaultUserBudgetLimits() (daily int64, monthly int64) {
	config.OptionMapRWMutex.RLock()
	defer config.OptionMapRWMutex.RUnlock()
	return parseQuotaOption(config.OptionMap["DefaultUserDailyQuotaLimit"]),
		parseQuotaOption(config.OptionMap["DefaultUserMonthlyQuotaLimit"])
}

func parseQuotaOption(value string) int64 {
	if value == "" {
		return 0
	}
	var result int64
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0
		}
		result = result*10 + int64(ch-'0')
	}
	return result
}
