package billing

import (
	"testing"

	"github.com/xiaosu4610/LTZY-API/common/config"
)

// TestPricingTokenMode 验证 token 模式下按 (prompt + completion*completionRatio) * modelRatio * groupRatio 计算
func TestPricingTokenMode(t *testing.T) {
	m := PricingModel{
		Mode:            PricingModeToken,
		ModelRatio:      2,
		CompletionRatio: 4,
		PromptRatio:     1,
	}
	got := m.CalcQuota(UsageInput{PromptTokens: 1000, CompletionTokens: 500}, 1)
	want := int64(6000) // (1000 + 500*4) * 2
	if got != want {
		t.Fatalf("token 模式额度错误，got %d, want %d", got, want)
	}

	// 群组倍率生效
	got = m.CalcQuota(UsageInput{PromptTokens: 1000, CompletionTokens: 500}, 2)
	if got != want*2 {
		t.Fatalf("群组倍率未生效，got %d, want %d", got, want*2)
	}
}

// TestPricingReasoningTokenRatio 验证 reasoning token 使用单独的推理倍率
func TestPricingReasoningTokenRatio(t *testing.T) {
	input := UsageInput{PromptTokens: 0, CompletionTokens: 1000, ReasoningTokens: 1000}
	// completionRatio=4, reasoningRatio=8 -> 1000*4 + 1000*(8-4) = 8000
	m := PricingModel{
		Mode:            PricingModeToken,
		ModelRatio:      1,
		CompletionRatio: 4,
		ReasoningRatio:  8,
	}
	if got := m.CalcQuota(input, 1); got != 8000 {
		t.Fatalf("reasoning 倍率计算错误，got %d, want 8000", got)
	}

	// ReasoningRatio 为 0 时回退到 CompletionRatio
	m.ReasoningRatio = 0
	if got := m.CalcQuota(input, 1); got != 4000 {
		t.Fatalf("reasoning 倍率回退错误，got %d, want 4000", got)
	}
}

// TestPricingPerRequestMode 验证按次计费
func TestPricingPerRequestMode(t *testing.T) {
	m := PricingModel{Mode: PricingModePerRequest, PerUnitQuota: 100}
	if got := m.CalcQuota(UsageInput{Count: 3}, 1); got != 300 {
		t.Fatalf("按次计费错误，got %d, want 300", got)
	}
	if got := m.CalcQuota(UsageInput{Count: 3}, 1.5); got != 450 {
		t.Fatalf("按次计费群组倍率错误，got %d, want 450", got)
	}
}

// TestPricingZeroRatio 验证倍率为 0 时额度为 0
func TestPricingZeroRatio(t *testing.T) {
	m := PricingModel{Mode: PricingModeToken, ModelRatio: 0, CompletionRatio: 4}
	if got := m.CalcQuota(UsageInput{PromptTokens: 1000, CompletionTokens: 1000}, 1); got != 0 {
		t.Fatalf("倍率为 0 时应返回 0，got %d", got)
	}
	pm := PricingModel{Mode: PricingModePerRequest, PerUnitQuota: 0}
	if got := pm.CalcQuota(UsageInput{Count: 10}, 1); got != 0 {
		t.Fatalf("单价为 0 时应返回 0，got %d", got)
	}
}

// TestPricingMinQuota 验证额度不足 1 时向上取整为 1
func TestPricingMinQuota(t *testing.T) {
	m := PricingModel{Mode: PricingModeToken, ModelRatio: 0.0001, CompletionRatio: 1}
	if got := m.CalcQuota(UsageInput{PromptTokens: 1}, 1); got != 1 {
		t.Fatalf("额度不足 1 时应为 1，got %d", got)
	}
	pm := PricingModel{Mode: PricingModePerSecond, PerUnitQuota: 0.1}
	if got := pm.CalcQuota(UsageInput{Seconds: 1}, 1); got != 1 {
		t.Fatalf("按秒额度不足 1 时应为 1，got %d", got)
	}
}

// TestGetPricingModel 验证从既有倍率表推导默认定价模型
func TestGetPricingModel(t *testing.T) {
	m := GetPricingModel("deepseek-reasoner", 0)
	if m.Mode != PricingModeToken {
		t.Fatalf("默认计费模式应为 token，got %s", m.Mode)
	}
	if m.ModelRatio <= 0 {
		t.Fatalf("模型倍率应大于 0，got %f", m.ModelRatio)
	}
	if m.CompletionRatio <= 0 {
		t.Fatalf("补全倍率应大于 0，got %f", m.CompletionRatio)
	}
	if m.PromptRatio != 1.0 {
		t.Fatalf("输入倍率默认应为 1.0，got %f", m.PromptRatio)
	}
}

// TestGetReasoningRatio 验证 ReasoningRatio Option 的读取、覆盖与解析失败回退
func TestGetReasoningRatio(t *testing.T) {
	setReasoningRatioOption(t, "")
	defer setReasoningRatioOption(t, "")

	if got := GetReasoningRatio("o1"); got != 1.0 {
		t.Fatalf("未配置时应返回默认值 1.0，got %f", got)
	}

	setReasoningRatioOption(t, `{"o1":2.0,"deepseek-reasoner":3.5}`)
	if got := GetReasoningRatio("o1"); got != 2.0 {
		t.Fatalf("Option 覆盖未生效，got %f, want 2.0", got)
	}
	if got := GetReasoningRatio("deepseek-reasoner"); got != 3.5 {
		t.Fatalf("Option 覆盖未生效，got %f, want 3.5", got)
	}
	if got := GetReasoningRatio("gpt-4o"); got != 1.0 {
		t.Fatalf("未配置模型应返回默认值 1.0，got %f", got)
	}

	// JSON 解析失败时回退默认值
	setReasoningRatioOption(t, "not-a-json")
	if got := GetReasoningRatio("o1"); got != 1.0 {
		t.Fatalf("解析失败时应回退 1.0，got %f", got)
	}
}

// setReasoningRatioOption 测试辅助：直接写入内存中的 OptionMap，避免依赖数据库
func setReasoningRatioOption(t *testing.T, value string) {
	t.Helper()
	config.OptionMapRWMutex.Lock()
	if config.OptionMap == nil {
		config.OptionMap = make(map[string]string)
	}
	config.OptionMap["ReasoningRatio"] = value
	config.OptionMapRWMutex.Unlock()
}
