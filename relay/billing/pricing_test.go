package billing

import (
	"math"
	"testing"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
)

// TestPricingTokenMode 验证 token 模式下按
// (prompt + completion*completionRatio) * modelRatio * groupRatio * RatioScale 计算
func TestPricingTokenMode(t *testing.T) {
	m := PricingModel{
		Mode:            PricingModeToken,
		ModelRatio:      2,
		CompletionRatio: 4,
		PromptRatio:     1,
	}
	scale := ratio.RatioScale()
	got := m.CalcQuota(UsageInput{PromptTokens: 1000, CompletionTokens: 500}, 1)
	want := int64(math.Ceil((1000 + 500*4) * 2 * scale)) // 倍率需按当前货币口径换算
	if got != want {
		t.Fatalf("token 模式额度错误，got %d, want %d", got, want)
	}

	// 群组倍率生效
	got = m.CalcQuota(UsageInput{PromptTokens: 1000, CompletionTokens: 500}, 2)
	if want = want * 2; got != want {
		t.Fatalf("群组倍率未生效，got %d, want %d", got, want)
	}
}

// TestPricingReasoningTokenRatio 验证 reasoning token 使用单独的推理倍率
func TestPricingReasoningTokenRatio(t *testing.T) {
	scale := ratio.RatioScale()
	input := UsageInput{PromptTokens: 0, CompletionTokens: 1000, ReasoningTokens: 1000}
	// completionRatio=4, reasoningRatio=8 -> 1000*4 + 1000*(8-4) = 8000
	m := PricingModel{
		Mode:            PricingModeToken,
		ModelRatio:      1,
		CompletionRatio: 4,
		ReasoningRatio:  8,
	}
	if want := int64(math.Ceil(8000 * scale)); m.CalcQuota(input, 1) != want {
		t.Fatalf("reasoning 倍率计算错误，got %d, want %d", m.CalcQuota(input, 1), want)
	}

	// ReasoningRatio 为 0 时回退到 CompletionRatio
	m.ReasoningRatio = 0
	if want := int64(math.Ceil(4000 * scale)); m.CalcQuota(input, 1) != want {
		t.Fatalf("reasoning 倍率回退错误，got %d, want %d", m.CalcQuota(input, 1), want)
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

// TestCalcQuotaAppliesTieredRatio 阶梯倍率必须真正参与结算计算（B4 回归测试）。
//
// 曾经的真实缺陷：阶梯倍率只乘进了「预扣用的 ratio」，而结算走
// modelRatio × groupRatio 重算，导致阶梯折扣预扣打了折、结算却按原价扣，
// 用户实际未享受折扣。单测只测倍率函数本身是发现不了的，必须锁在这里。
func TestCalcQuotaAppliesTieredRatio(t *testing.T) {
	usage := UsageInput{PromptTokens: 1000, CompletionTokens: 500}
	base := PricingModel{Mode: PricingModeToken, ModelRatio: 2, CompletionRatio: 4, PromptRatio: 1}
	scale := ratio.RatioScale()

	full := base.CalcQuota(usage, 1) // (1000 + 500*4) * 2 * scale
	if want := int64(math.Ceil(6000 * scale)); full != want {
		t.Fatalf("无阶梯时应为 %d，实际 %d", want, full)
	}
	base.TieredRatio = 0.5
	if got, want := base.CalcQuota(usage, 1), int64(math.Ceil(3000*scale)); got != want {
		t.Fatalf("阶梯倍率 0.5 未生效，实际 %d，期望 %d", got, want)
	}
	// TieredRatio 为 0（未配置）必须等价于 1.0，保证与旧公式一致
	base.TieredRatio = 0
	if got := base.CalcQuota(usage, 1); got != full {
		t.Fatalf("TieredRatio=0 应等价于 1.0，实际 %d，期望 %d", got, full)
	}
}

// TestCalcQuotaEquivalentToLegacy 锁死统一定价引擎与旧计费公式的等价性（B1 接入的安全网）。
// 旧公式（helper.go 改造前）：ceil((prompt + completion*completionRatio) * modelRatio * groupRatio)，
// 倍率非零但结果非正时兜底为 1。ReasoningRatio 未配置（=补全倍率）时两者必须逐分不差。
// 人民币计价改造后，新引擎在此基础上多乘一个 RatioScale()（倍率表美元基准 → 人民币口径）。
func TestCalcQuotaEquivalentToLegacy(t *testing.T) {
	setReasoningRatioOption(t, "")
	defer setReasoningRatioOption(t, "")

	scale := ratio.RatioScale()
	cases := []struct {
		prompt, completion int
		modelRatio         float64
		groupRatio         float64
	}{
		{0, 0, 2, 1},               // 全零（调用方仍会按 totalTokens==0 归零，这里只比对公式与兜底）
		{1000, 500, 2, 1},          // 常规
		{1000, 500, 2, 1.5},        // 群组倍率
		{1, 0, 0.0001, 1},          // 小数向上取整为 1
		{500, 0, 0.75, 1},          // 仅提示 token
		{123, 456, 1.33, 2},        // 非整倍率
		{777, 888, 3, 0.5},         // 折扣群组
		{100, 200, 0, 1},           // 免费模型
		{1048576, 1048576, 2.5, 3}, // 大数
	}
	const completionRatio = 4.0
	for _, tc := range cases {
		legacy := int64(math.Ceil((float64(tc.prompt) + float64(tc.completion)*completionRatio) * tc.modelRatio * tc.groupRatio * scale))
		m := PricingModel{
			Mode:            PricingModeToken,
			ModelRatio:      tc.modelRatio,
			CompletionRatio: completionRatio,
			PromptRatio:     1,
			ReasoningRatio:  completionRatio, // 未配置时的回退值
		}
		got := m.CalcQuota(UsageInput{PromptTokens: tc.prompt, CompletionTokens: tc.completion}, tc.groupRatio)
		// postConsumeQuota 保留了原实现的兜底：倍率非零但结果非正时至少计 1
		if tc.modelRatio*tc.groupRatio != 0 {
			if legacy <= 0 {
				legacy = 1
			}
			if got <= 0 {
				got = 1
			}
		}
		if got != legacy {
			t.Fatalf("case %+v: 新旧公式不一致，got %d, legacy %d", tc, got, legacy)
		}
	}
}

// TestNewTextPricingModelFallback 验证结算路径构造器：ReasoningRatio 未配置时回退补全倍率
func TestNewTextPricingModelFallback(t *testing.T) {
	setReasoningRatioOption(t, "")
	defer setReasoningRatioOption(t, "")

	m := NewTextPricingModel("o1", 2, 4, 0)
	if m.ReasoningRatio != 4 {
		t.Fatalf("未配置时应回退补全倍率，got %f", m.ReasoningRatio)
	}
	setReasoningRatioOption(t, `{"o1":8}`)
	m = NewTextPricingModel("o1", 2, 4, 0)
	if m.ReasoningRatio != 8 {
		t.Fatalf("配置后应生效，got %f", m.ReasoningRatio)
	}
}
