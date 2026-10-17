package ratio

import (
	"testing"
)

// TestGetGroupRatioForModelFallback 验证未配置矩阵时与旧的一维分组倍率完全一致（B3 的安全网）
func TestGetGroupRatioForModelFallback(t *testing.T) {
	if err := UpdateModelGroupRatioByJSONString(""); err != nil {
		t.Fatalf("空矩阵应合法：%v", err)
	}
	for _, group := range []string{"default", "vip", "svip"} {
		old := GetGroupRatio(group)
		got := GetGroupRatioForModel(group, "gpt-4o")
		if got != old {
			t.Fatalf("分组 %s 未命中矩阵时应回退一维倍率：got %v, want %v", group, got, old)
		}
	}
}

// TestGetGroupRatioForModelHit 验证二维矩阵命中时覆盖一维倍率，且不影响其他组合
func TestGetGroupRatioForModelHit(t *testing.T) {
	matrix := `{"vip":{"gpt-4o":0.8,"claude-3-5-sonnet":0.9}}`
	if err := UpdateModelGroupRatioByJSONString(matrix); err != nil {
		t.Fatalf("合法矩阵应通过：%v", err)
	}
	defer func() { _ = UpdateModelGroupRatioByJSONString("") }()

	if got := GetGroupRatioForModel("vip", "gpt-4o"); got != 0.8 {
		t.Fatalf("命中矩阵应为 0.8，实际 %v", got)
	}
	if got := GetGroupRatioForModel("vip", "claude-3-5-sonnet"); got != 0.9 {
		t.Fatalf("命中矩阵应为 0.9，实际 %v", got)
	}
	// 同分组未列出的模型 → 回退
	if got := GetGroupRatioForModel("vip", "deepseek-chat"); got != GetGroupRatio("vip") {
		t.Fatalf("同分组未列出的模型应回退，实际 %v", got)
	}
	// 其他分组的同名模型 → 回退
	if got := GetGroupRatioForModel("default", "gpt-4o"); got != GetGroupRatio("default") {
		t.Fatalf("其他分组应回退，实际 %v", got)
	}
	// 不存在的分组 → 回退（GetGroupRatio 对未知分组返回 1）
	if got := GetGroupRatioForModel("not-exist", "gpt-4o"); got != 1 {
		t.Fatalf("未知分组应返回默认 1，实际 %v", got)
	}
}

// TestUpdateModelGroupRatioValidation 验证脏数据被拦截且不会破坏已生效的矩阵
func TestUpdateModelGroupRatioValidation(t *testing.T) {
	good := `{"vip":{"gpt-4o":0.5}}`
	if err := UpdateModelGroupRatioByJSONString(good); err != nil {
		t.Fatalf("合法矩阵应通过：%v", err)
	}
	defer func() { _ = UpdateModelGroupRatioByJSONString("") }()

	// 非法 JSON
	if err := UpdateModelGroupRatioByJSONString("not-a-json"); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	// 负倍率
	if err := UpdateModelGroupRatioByJSONString(`{"vip":{"gpt-4o":-1}}`); err == nil {
		t.Fatal("负倍率应报错")
	}
	// 空分组名
	if err := UpdateModelGroupRatioByJSONString(`{"":{"gpt-4o":1}}`); err == nil {
		t.Fatal("空分组名应报错")
	}
	// 空模型名
	if err := UpdateModelGroupRatioByJSONString(`{"vip":{"":1}}`); err == nil {
		t.Fatal("空模型名应报错")
	}

	// 关键：以上失败均不应影响已生效的矩阵
	if got := GetGroupRatioForModel("vip", "gpt-4o"); got != 0.5 {
		t.Fatalf("校验失败后原矩阵应保持不变，实际 %v", got)
	}
}

// TestModelGroupRatio2JSONString 验证序列化可用于系统设置回显
func TestModelGroupRatio2JSONString(t *testing.T) {
	if err := UpdateModelGroupRatioByJSONString(`{"vip":{"gpt-4o":0.8}}`); err != nil {
		t.Fatalf("设置矩阵失败：%v", err)
	}
	defer func() { _ = UpdateModelGroupRatioByJSONString("") }()
	out := ModelGroupRatio2JSONString()
	if out != `{"vip":{"gpt-4o":0.8}}` {
		t.Fatalf("序列化结果不符：%s", out)
	}
}
