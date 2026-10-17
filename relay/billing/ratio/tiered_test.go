package ratio

import (
	"testing"

	"github.com/xiaosu4610/LTZY-API/common/config"
)

// setTieredRatioOption 测试辅助：直接写内存 OptionMap，避免依赖数据库
func setTieredRatioOption(t *testing.T, value string) {
	t.Helper()
	config.OptionMapRWMutex.Lock()
	if config.OptionMap == nil {
		config.OptionMap = make(map[string]string)
	}
	config.OptionMap["TieredRatio"] = value
	config.OptionMapRWMutex.Unlock()
}

// TestTieredRatioNotConfigured 未配置阶梯时必须恒为 1.0（保证不影响既有计费，B4 安全网）
func TestTieredRatioNotConfigured(t *testing.T) {
	setTieredRatioOption(t, "")
	defer setTieredRatioOption(t, "")

	if HasTieredRatio() {
		t.Fatal("空配置时 HasTieredRatio 应为 false")
	}
	if got := GetTieredRatio("gpt-4o", 99999999); got != 1.0 {
		t.Fatalf("未配置时应为 1.0，实际 %v", got)
	}
}

// TestTieredRatioSelection 验证按累计用量选中正确档位
func TestTieredRatioSelection(t *testing.T) {
	// 故意乱序书写，验证解析时会归一化为升序
	setTieredRatioOption(t, `{"gpt-4o":[
		{"threshold":5000000,"ratio":0.5},
		{"threshold":0,"ratio":1.0},
		{"threshold":1000000,"ratio":0.8}
	]}`)
	defer setTieredRatioOption(t, "")

	if !HasTieredRatio() {
		t.Fatal("配置后 HasTieredRatio 应为 true")
	}
	cases := []struct {
		used int64
		want float64
	}{
		{0, 1.0},        // 第一档
		{999999, 1.0},   // 未达第二档
		{1000000, 0.8},  // 恰好达标（>= 语义）
		{4999999, 0.8},  // 第三档之前
		{5000000, 0.5},  // 最高档
		{99999999, 0.5}, // 超过最高档仍取最高档
	}
	for _, tc := range cases {
		if got := GetTieredRatio("gpt-4o", tc.used); got != tc.want {
			t.Fatalf("用量 %d 应为 %v，实际 %v", tc.used, tc.want, got)
		}
	}
	// 未配置阶梯的模型不受影响
	if got := GetTieredRatio("deepseek-chat", 99999999); got != 1.0 {
		t.Fatalf("未配置的模型应为 1.0，实际 %v", got)
	}
}

// TestTieredRatioOnlyHighTierConfigured 只配了非零起始档位时，
// 低于该阈值的用量不应命中任何档位（保持 1.0，而不是误用某个档位）
func TestTieredRatioOnlyHighTierConfigured(t *testing.T) {
	setTieredRatioOption(t, `{"gpt-4o":[{"threshold":1000000,"ratio":0.8}]}`)
	defer setTieredRatioOption(t, "")

	if got := GetTieredRatio("gpt-4o", 999999); got != 1.0 {
		t.Fatalf("低于首档阈值应为 1.0，实际 %v", got)
	}
	if got := GetTieredRatio("gpt-4o", 1000000); got != 0.8 {
		t.Fatalf("达到阈值应为 0.8，实际 %v", got)
	}
}

// TestValidateTieredRatioOption 验证脏配置被拦截
func TestValidateTieredRatioOption(t *testing.T) {
	if err := ValidateTieredRatioOption(""); err != nil {
		t.Fatalf("空配置应合法：%v", err)
	}
	if err := ValidateTieredRatioOption(`{"gpt-4o":[{"threshold":0,"ratio":1.0},{"threshold":1000000,"ratio":0.8}]}`); err != nil {
		t.Fatalf("合法配置应通过：%v", err)
	}
	bad := []string{
		"not-a-json",
		`{"gpt-4o":[{"threshold":-1,"ratio":1}]}`,
		`{"gpt-4o":[{"threshold":0,"ratio":-1}]}`,
		`{"gpt-4o":[{"threshold":0,"ratio":1},{"threshold":0,"ratio":0.5}]}`,
		`{"":[{"threshold":0,"ratio":1}]}`,
	}
	for _, value := range bad {
		if err := ValidateTieredRatioOption(value); err == nil {
			t.Fatalf("非法配置应报错：%s", value)
		}
	}
}

// TestTieredRatioInvalidJSONFallsBack 配置写成非法 JSON 时按"未配置"处理，不能让计费炸掉
func TestTieredRatioInvalidJSONFallsBack(t *testing.T) {
	setTieredRatioOption(t, "not-a-json")
	defer setTieredRatioOption(t, "")

	if got := GetTieredRatio("gpt-4o", 99999999); got != 1.0 {
		t.Fatalf("非法 JSON 应按未配置处理，实际 %v", got)
	}
}
