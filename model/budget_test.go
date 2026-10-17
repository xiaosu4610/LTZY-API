package model

import (
	"testing"
	"time"
)

// TestBudgetPeriodKey 验证周期键：跨周期自动归零依赖它（B6）
func TestBudgetPeriodKey(t *testing.T) {
	d := time.Date(2026, 10, 17, 23, 59, 59, 0, time.Local)
	if got := BudgetPeriodKey(BudgetPeriodDaily, d); got != "20261017" {
		t.Fatalf("日周期键错误：%s", got)
	}
	if got := BudgetPeriodKey(BudgetPeriodMonthly, d); got != "202610" {
		t.Fatalf("月周期键错误：%s", got)
	}
	// 跨日：日键变化、月键不变
	next := time.Date(2026, 10, 18, 0, 0, 1, 0, time.Local)
	if BudgetPeriodKey(BudgetPeriodDaily, next) == BudgetPeriodKey(BudgetPeriodDaily, d) {
		t.Fatal("跨日后日周期键应变化")
	}
	if BudgetPeriodKey(BudgetPeriodMonthly, next) != BudgetPeriodKey(BudgetPeriodMonthly, d) {
		t.Fatal("同月内月周期键不应变化")
	}
	// 跨月
	nextMonth := time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local)
	if BudgetPeriodKey(BudgetPeriodMonthly, nextMonth) == BudgetPeriodKey(BudgetPeriodMonthly, d) {
		t.Fatal("跨月后月周期键应变化")
	}
}

// TestRecordAndReadBudgetUsage 验证计数的累加与按主体隔离（B6）
func TestRecordAndReadBudgetUsage(t *testing.T) {
	initTestDB(t)
	if err := DB.AutoMigrate(&QuotaBudget{}); err != nil {
		t.Fatalf("迁移预算表失败：%v", err)
	}

	now := time.Now()
	RecordBudgetUsage(1, 10, 500)
	RecordBudgetUsage(1, 10, 300)
	RecordBudgetUsage(2, 20, 999)

	daily, monthly, err := GetBudgetUsages(BudgetSubjectUser, 1, now)
	if err != nil {
		t.Fatalf("读取用户预算失败：%v", err)
	}
	if daily != 800 || monthly != 800 {
		t.Fatalf("用户用量应为 800/800，实际 %d/%d", daily, monthly)
	}

	// 令牌维度独立计数
	tDaily, tMonthly, err := GetBudgetUsages(BudgetSubjectToken, 10, now)
	if err != nil {
		t.Fatalf("读取令牌预算失败：%v", err)
	}
	if tDaily != 800 || tMonthly != 800 {
		t.Fatalf("令牌用量应为 800/800，实际 %d/%d", tDaily, tMonthly)
	}

	// 其他主体不受影响
	otherDaily, _, _ := GetBudgetUsages(BudgetSubjectUser, 2, now)
	if otherDaily != 999 {
		t.Fatalf("用户 2 用量应为 999，实际 %d", otherDaily)
	}
	// 不存在的用户返回 0 而非报错
	zeroDaily, zeroMonthly, err := GetBudgetUsages(BudgetSubjectUser, 9999, now)
	if err != nil || zeroDaily != 0 || zeroMonthly != 0 {
		t.Fatalf("不存在的主体应返回 0，实际 %d/%d err=%v", zeroDaily, zeroMonthly, err)
	}
}

// TestRecordBudgetUsageSkipInvalid 验证非法入参不会写入垃圾数据
func TestRecordBudgetUsageSkipInvalid(t *testing.T) {
	initTestDB(t)
	if err := DB.AutoMigrate(&QuotaBudget{}); err != nil {
		t.Fatalf("迁移预算表失败：%v", err)
	}
	// 0 或负数额度不计数（退还预扣时 quota 可能为负）
	RecordBudgetUsage(1, 1, 0)
	RecordBudgetUsage(1, 1, -100)
	var count int64
	DB.Model(&QuotaBudget{}).Count(&count)
	if count != 0 {
		t.Fatalf("非法额度不应写入计数，实际 %d 行", count)
	}
}

// TestParseQuotaOption 验证 Option 字符串解析（B7 之前的先行实现）
func TestParseQuotaOption(t *testing.T) {
	cases := map[string]int64{
		"":         0,
		"0":        0,
		"1000000":  1000000,
		"-1":       0, // 负数视为未配置
		"abc":      0,
		"12abc":    0,
		" 100":     0, // 带空格视为非法，回退未配置
	}
	for input, want := range cases {
		if got := parseQuotaOption(input); got != want {
			t.Fatalf("parseQuotaOption(%q) = %d, want %d", input, got, want)
		}
	}
}
