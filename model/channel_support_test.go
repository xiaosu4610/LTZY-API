package model

import (
	"testing"

	"github.com/xiaosu4610/LTZY-API/common/config"
)

// withMemoryCache 在用例期间固定 config.MemoryCacheEnabled 的取值。
// 该开关决定灰度校验走内存索引还是数据库，两条路径都要覆盖。
func withMemoryCache(t *testing.T, enabled bool) {
	t.Helper()
	previous := config.MemoryCacheEnabled
	config.MemoryCacheEnabled = enabled
	t.Cleanup(func() { config.MemoryCacheEnabled = previous })
}

// TestChannelSupportsModelByMemoryCache 灰度校验的内存索引路径：
// 只有「分组 + 模型 + 渠道」三者同时命中才算支持，任何一维不匹配都不能放行。
func TestChannelSupportsModelByMemoryCache(t *testing.T) {
	channelSyncLock.Lock()
	previous := group2model2channels
	group2model2channels = map[string]map[string][]*Channel{
		"default": {
			"gpt-4o": {
				{Id: 7, Status: ChannelStatusEnabled},
				{Id: 8, Status: ChannelStatusEnabled},
			},
		},
	}
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		group2model2channels = previous
		channelSyncLock.Unlock()
	})
	withMemoryCache(t, true)

	cases := []struct {
		name      string
		group     string
		model     string
		channelId int
		want      bool
	}{
		{"三者同时命中", "default", "gpt-4o", 8, true},
		{"渠道不在该模型的索引里", "default", "gpt-4o", 9, false},
		{"分组不匹配", "vip", "gpt-4o", 7, false},
		{"模型不匹配", "default", "gpt-3.5-turbo", 7, false},
		{"分组与模型都不匹配", "vip", "gpt-3.5-turbo", 7, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChannelSupportsModel(tc.group, tc.model, tc.channelId); got != tc.want {
				t.Fatalf("ChannelSupportsModel(%q, %q, %d) = %v，期望 %v",
					tc.group, tc.model, tc.channelId, got, tc.want)
			}
		})
	}
}

// TestChannelSupportsModelByAbilityTable 未开启内存缓存时退化为查 Ability 表，
// 与主选渠道路径保持同一判定口径；被停用的能力记录不能放行。
func TestChannelSupportsModelByAbilityTable(t *testing.T) {
	initTestDB(t)
	if err := DB.AutoMigrate(&Ability{}); err != nil {
		t.Fatalf("迁移能力表失败：%v", err)
	}
	withMemoryCache(t, false)

	abilities := []Ability{
		{Group: "default", Model: "gpt-4o", ChannelId: 7, Enabled: true},
		{Group: "default", Model: "gpt-4o", ChannelId: 8, Enabled: false},
		{Group: "default", Model: "claude-3", ChannelId: 7, Enabled: true},
	}
	if err := DB.Create(&abilities).Error; err != nil {
		t.Fatalf("写入能力记录失败：%v", err)
	}

	cases := []struct {
		name      string
		group     string
		model     string
		channelId int
		want      bool
	}{
		{"启用的能力记录", "default", "gpt-4o", 7, true},
		{"能力记录被停用", "default", "gpt-4o", 8, false},
		{"该渠道没有这个模型的能力", "default", "gpt-4o", 9, false},
		{"模型不匹配", "default", "gpt-4o-mini", 7, false},
		{"分组不匹配", "vip", "gpt-4o", 7, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChannelSupportsModel(tc.group, tc.model, tc.channelId); got != tc.want {
				t.Fatalf("ChannelSupportsModel(%q, %q, %d) = %v，期望 %v",
					tc.group, tc.model, tc.channelId, got, tc.want)
			}
		})
	}
}

// TestAbilityExistsWithoutDatabase 数据库尚未初始化时按「不支持」返回：
// 灰度校验出现在请求主链路上，任何情况下都不能 panic 或让请求失败。
func TestAbilityExistsWithoutDatabase(t *testing.T) {
	previous := DB
	DB = nil
	t.Cleanup(func() { DB = previous })

	if AbilityExists("default", "gpt-4o", 7) {
		t.Fatal("数据库未初始化时不应判定为支持")
	}
}
