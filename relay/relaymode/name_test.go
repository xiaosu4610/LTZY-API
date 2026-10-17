package relaymode

import "testing"

// TestModeNameRoundTrip 验证模式名与模式号互查（C3 scope 配置的基础）
func TestModeNameRoundTrip(t *testing.T) {
	cases := []struct {
		mode int
		name string
	}{
		{ChatCompletions, "chat"},
		{Embeddings, "embeddings"},
		{ImagesGenerations, "images"},
		{AudioSpeech, "audio_speech"},
		{Rerank, "rerank"},
		{Responses, "responses"},
		{Videos, "videos"},
		{Proxy, "proxy"},
	}
	for _, tc := range cases {
		if got := Name(tc.mode); got != tc.name {
			t.Fatalf("模式 %d 名称应为 %s，实际 %s", tc.mode, tc.name, got)
		}
		if !IsValidName(tc.name) {
			t.Fatalf("%s 应为合法模式名", tc.name)
		}
	}
	// 未知模式
	if Name(9999) != "" {
		t.Fatal("未知模式应返回空串")
	}
	if IsValidName("not-a-mode") {
		t.Fatal("非法模式名不应通过校验")
	}
	// 所有具名模式都必须可反查（防止只加 Name 忘了映射）
	for mode, name := range modeNames {
		if !IsValidName(name) {
			t.Fatalf("模式 %d(%s) 无法反查", mode, name)
		}
	}
}

// TestParseAllowedModes 验证 scope 配置解析
func TestParseAllowedModes(t *testing.T) {
	// 空 / 纯空白 → nil（不限制）
	for _, raw := range []string{"", "   ", ",", " , "} {
		if got := ParseAllowedModes(raw); got != nil {
			t.Fatalf("空配置 %q 应返回 nil（不限制），实际 %v", raw, got)
		}
	}
	// 全部非法 → nil（不限制），避免管理员写错一个词就把令牌锁死
	if got := ParseAllowedModes("chat,not-a-mode"); got == nil || !got[ChatCompletions] {
		t.Fatalf("含非法项时应保留合法项，实际 %v", got)
	}
	if got := ParseAllowedModes("bad1,bad2"); got != nil {
		t.Fatalf("全部非法应视为不限制，实际 %v", got)
	}
	// 正常解析（含空格容错）
	got := ParseAllowedModes(" chat , embeddings ")
	if !got[ChatCompletions] || !got[Embeddings] {
		t.Fatalf("解析结果错误：%v", got)
	}
	if got[ImagesGenerations] {
		t.Fatal("未配置的模式不应被放行")
	}
}
