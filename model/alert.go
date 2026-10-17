package model

import (
	"time"

	"github.com/xiaosu4610/LTZY-API/common"
)

// 用量告警的数据查询（LTZY-API 新增，D6）。
//
// 直接复用 quota_budgets 的日计数，而不是扫日志表：
//   - 计数表是 O(1) 的点查规模，告警轮询可以放心地高频跑；
//   - 不依赖 LogConsumeEnabled（关掉消费日志后告警依然有效）。

// UserDailyUsageOver 找出某日用量达到阈值的用户，按用量降序返回。
// threshold <= 0 时返回空（表示未启用）。
func UserDailyUsageOver(threshold int64, limit int) ([]*QuotaBudget, error) {
	if threshold <= 0 {
		return nil, nil
	}
	periodKey := BudgetPeriodKey(BudgetPeriodDaily, time.Now())
	var rows []*QuotaBudget
	tx := DB.Where("subject_type = ? AND period = ? AND period_key = ? AND used_quota >= ?",
		BudgetSubjectUser, BudgetPeriodDaily, periodKey, threshold).
		Order("used_quota desc")
	if limit > 0 {
		tx = tx.Limit(limit)
	}
	err := tx.Find(&rows).Error
	return rows, err
}

// GlobalDailyUsage 返回某日全部用户的消费合计（用于全局阈值告警）。
func GlobalDailyUsage() (int64, error) {
	periodKey := BudgetPeriodKey(BudgetPeriodDaily, time.Now())
	// 用 COALESCE/ifnull 兜住空表，避免 NULL 扫描失败（三种数据库函数名不同）
	ifnull := "ifnull"
	if common.UsingPostgreSQL {
		ifnull = "COALESCE"
	}
	var total int64
	err := DB.Model(&QuotaBudget{}).
		Select(ifnull + "(sum(used_quota),0)").
		Where("subject_type = ? AND period = ? AND period_key = ?",
			BudgetSubjectUser, BudgetPeriodDaily, periodKey).
		Scan(&total).Error
	return total, err
}
