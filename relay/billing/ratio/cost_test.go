package ratio

import "testing"

// TestCostRatioFallback 未配置进价时成本为 0（表示"未知成本"，不是"零成本"）
func TestCostRatioFallback(t *testing.T) {
	if err := UpdateCostRatioByJSONString(""); err != nil {
		t.Fatalf("空配置应合法：%v", err)
	}
	defer func() { _ = UpdateCostRatioByJSONString("") }()

	if HasCostRatio() {
		t.Fatal("空配置时 HasCostRatio 应为 false")
	}
	if got := CalcCostQuota("gpt-4o", 1000, 500); got != 0 {
		t.Fatalf("未配置进价时成本应为 0，实际 %d", got)
	}
}

// TestCalcCostQuota 验证成本计算口径与售价同形：(prompt + completion×completionRatio) × costRatio
func TestCalcCostQuota(t *testing.T) {
	if err := UpdateCostRatioByJSONString(`{"gpt-4o":0.5}`); err != nil {
		t.Fatalf("合法配置应通过：%v", err)
	}
	defer func() { _ = UpdateCostRatioByJSONString("") }()

	completionRatio := GetCompletionRatio("gpt-4o", 0)
	// 与售价口径一致：(prompt + completion × completionRatio) × 进价倍率
	const costRatio = 0.5
	want := int64((float64(1000) + float64(500)*completionRatio) * costRatio)
	got := CalcCostQuota("gpt-4o", 1000, 500)
	if got != want {
		t.Fatalf("成本计算不符：got %d, want %d (completionRatio=%v)", got, want, completionRatio)
	}
}

// TestCostRatioWildcard 通配键 `*` 作为兜底进价
func TestCostRatioWildcard(t *testing.T) {
	if err := UpdateCostRatioByJSONString(`{"gpt-4o":0.5,"*":2.0}`); err != nil {
		t.Fatalf("合法配置应通过：%v", err)
	}
	defer func() { _ = UpdateCostRatioByJSONString("") }()

	if got := GetCostRatio("gpt-4o"); got != 0.5 {
		t.Fatalf("精确匹配应优先，实际 %v", got)
	}
	if got := GetCostRatio("some-unknown-model"); got != 2.0 {
		t.Fatalf("未配置的模型应回退通配值，实际 %v", got)
	}
	// 通配值也要真正用于计算
	if got := CalcCostQuota("some-unknown-model", 100, 0); got == 0 {
		t.Fatal("通配进价应参与成本计算")
	}
}

// TestValidateCostRatioOption 验证脏配置被拦截且不破坏已生效的表
func TestValidateCostRatioOption(t *testing.T) {
	if err := UpdateCostRatioByJSONString(`{"gpt-4o":0.5}`); err != nil {
		t.Fatalf("初始配置失败：%v", err)
	}
	defer func() { _ = UpdateCostRatioByJSONString("") }()

	if err := ValidateCostRatioOption(""); err != nil {
		t.Fatalf("空配置应合法：%v", err)
	}
	if err := ValidateCostRatioOption(`{"gpt-4o":1.6,"*":2.0}`); err != nil {
		t.Fatalf("合法配置应通过：%v", err)
	}
	for _, bad := range []string{"not-a-json", `{"gpt-4o":-1}`, `{"":1.0}`} {
		if err := ValidateCostRatioOption(bad); err == nil {
			t.Fatalf("非法配置应报错：%s", bad)
		}
	}
	// 关键：写入失败不应清空已生效的表
	if err := UpdateCostRatioByJSONString("not-a-json"); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	if got := GetCostRatio("gpt-4o"); got != 0.5 {
		t.Fatalf("校验失败后原表应保持不变，实际 %v", got)
	}
}
