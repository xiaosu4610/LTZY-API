package model

import (
	"path/filepath"
	"testing"

	"github.com/xiaosu4610/LTZY-API/common"
)

// initTestDB 为单测初始化一个临时的 SQLite 数据库。
func initTestDB(t *testing.T) {
	t.Helper()
	common.SQLitePath = filepath.Join(t.TempDir(), "ltzy-api-test.db")
	common.UsingSQLite = true
	var err error
	DB, err = openSQLite()
	if err != nil {
		t.Fatalf("打开测试数据库失败：%v", err)
	}
	LOG_DB = DB
	if err = LOG_DB.AutoMigrate(&Log{}); err != nil {
		t.Fatalf("迁移日志表失败：%v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := DB.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
}

func TestCostStatistics(t *testing.T) {
	initTestDB(t)

	base := int64(1700000000)
	logs := []*Log{
		{UserId: 1, Username: "alice", ModelName: "gpt-4o", ChannelId: 10, Type: LogTypeConsume, CreatedAt: base, Quota: 100, PromptTokens: 10, CompletionTokens: 20},
		{UserId: 1, Username: "alice", ModelName: "gpt-4o", ChannelId: 10, Type: LogTypeConsume, CreatedAt: base + 60, Quota: 200, PromptTokens: 30, CompletionTokens: 40},
		{UserId: 2, Username: "bob", ModelName: "claude-3", ChannelId: 20, Type: LogTypeConsume, CreatedAt: base + 120, Quota: 50, PromptTokens: 5, CompletionTokens: 5},
		// 非消费类型，不应被统计
		{UserId: 1, Username: "alice", ModelName: "gpt-4o", ChannelId: 10, Type: LogTypeTopup, CreatedAt: base + 180, Quota: 9999},
	}
	for _, l := range logs {
		if err := LOG_DB.Create(l).Error; err != nil {
			t.Fatalf("插入日志失败：%v", err)
		}
	}

	// 模型维度
	modelStats, err := GetModelCostStatistics(0, 0, 10)
	if err != nil {
		t.Fatalf("GetModelCostStatistics 失败：%v", err)
	}
	if len(modelStats) != 2 {
		t.Fatalf("模型维度应返回 2 条，实际 %d", len(modelStats))
	}
	// 按额度降序：gpt-4o(300) 在前
	if modelStats[0].ModelName != "gpt-4o" || modelStats[0].Quota != 300 {
		t.Fatalf("模型维度排序/聚合错误：%+v", modelStats[0])
	}
	if modelStats[0].RequestCount != 2 || modelStats[0].PromptTokens != 40 || modelStats[0].CompletionTokens != 60 {
		t.Fatalf("模型维度聚合数值错误：%+v", modelStats[0])
	}

	// 用户维度
	userStats, err := GetUserCostStatistics(0, 0, 10)
	if err != nil {
		t.Fatalf("GetUserCostStatistics 失败：%v", err)
	}
	if len(userStats) != 2 || userStats[0].Username != "alice" || userStats[0].Quota != 300 {
		t.Fatalf("用户维度聚合错误：%+v", userStats)
	}

	// 渠道维度
	channelStats, err := GetChannelStatistics(0, 0)
	if err != nil {
		t.Fatalf("GetChannelStatistics 失败：%v", err)
	}
	if len(channelStats) != 2 {
		t.Fatalf("渠道维度应返回 2 条，实际 %d", len(channelStats))
	}

	// 日维度
	dailyStats, err := GetDailyCostStatistics(0, 0)
	if err != nil {
		t.Fatalf("GetDailyCostStatistics 失败：%v", err)
	}
	if len(dailyStats) == 0 {
		t.Fatal("日维度不应为空")
	}
	var totalQuota int64
	for _, d := range dailyStats {
		totalQuota += d.Quota
	}
	if totalQuota != 350 {
		t.Fatalf("日维度总额应为 350，实际 %d", totalQuota)
	}
}
