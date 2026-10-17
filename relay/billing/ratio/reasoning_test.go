package ratio

import "testing"

// TestValidateReasoningRatioOption 验证 ReasoningRatio 配置写入前的校验（LTZY-API 新增）
func TestValidateReasoningRatioOption(t *testing.T) {
	if err := ValidateReasoningRatioOption(""); err != nil {
		t.Fatalf("空配置应合法，got %v", err)
	}
	if err := ValidateReasoningRatioOption(`{"o1":2.0,"deepseek-reasoner":3.5}`); err != nil {
		t.Fatalf("合法 JSON 应通过，got %v", err)
	}
	if err := ValidateReasoningRatioOption("not-a-json"); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	if err := ValidateReasoningRatioOption(`{"o1":-1}`); err == nil {
		t.Fatal("负倍率应报错")
	}
	if err := ValidateReasoningRatioOption(`{"":2.0}`); err == nil {
		t.Fatal("空模型名应报错")
	}
}
