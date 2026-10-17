package relaymode

import "testing"

// TestGetByPathPassthroughEndpoints 验证 A6 新增端点族「路径 → 中继模式」的映射。
//
// 这张表同时是路由清单的镜像：router/relay.go 里每一个从 501 改成透传的端点都应
// 出现在这里，否则令牌 scope（middleware.TokenScope 依赖 GetByPath）会漏掉它，
// 前端下拉也看不到对应模式。
func TestGetByPathPassthroughEndpoints(t *testing.T) {
	cases := []struct {
		path string
		mode int
	}{
		// files：列表/上传/查询/删除/下载内容
		{"/v1/files", Files},
		{"/v1/files/file-abc123", Files},
		{"/v1/files/file-abc123/content", Files},
		// fine_tuning
		{"/v1/fine_tuning/jobs", FineTuning},
		{"/v1/fine_tuning/jobs/ftjob-1", FineTuning},
		{"/v1/fine_tuning/jobs/ftjob-1/cancel", FineTuning},
		{"/v1/fine_tuning/jobs/ftjob-1/events", FineTuning},
		// assistants（含 assistant files）
		{"/v1/assistants", Assistants},
		{"/v1/assistants/asst_1", Assistants},
		{"/v1/assistants/asst_1/files", Assistants},
		{"/v1/assistants/asst_1/files/file-1", Assistants},
		// threads（含 messages 与 runs）
		{"/v1/threads", Threads},
		{"/v1/threads/thread_1", Threads},
		{"/v1/threads/thread_1/messages", Threads},
		{"/v1/threads/thread_1/messages/msg_1", Threads},
		{"/v1/threads/thread_1/messages/msg_1/files", Threads},
		{"/v1/threads/thread_1/messages/msg_1/files/file-1", Threads},
		{"/v1/threads/thread_1/runs", Threads},
		{"/v1/threads/thread_1/runs/run_1", Threads},
		{"/v1/threads/thread_1/runs/run_1/submit_tool_outputs", Threads},
		{"/v1/threads/thread_1/runs/run_1/cancel", Threads},
		{"/v1/threads/thread_1/runs/run_1/steps", Threads},
		{"/v1/threads/thread_1/runs/run_1/steps/step_1", Threads},
		// 删除微调模型：只认带子路径的形态
		{"/v1/models/ft:gpt-4o-mini-org:abc", Models},
	}
	for _, tc := range cases {
		if got := GetByPath(tc.path); got != tc.mode {
			t.Fatalf("%s 应映射到模式 %d，实际 %d", tc.path, tc.mode, got)
		}
		if !IsPassthrough(tc.mode) {
			t.Fatalf("模式 %d 应属于透传家族", tc.mode)
		}
	}
}

// TestGetByPathKeepsExistingModes 回归：新增分支不能影响既有端点判定。
func TestGetByPathKeepsExistingModes(t *testing.T) {
	cases := []struct {
		path string
		mode int
	}{
		{"/v1/chat/completions", ChatCompletions},
		{"/v1/completions", Completions},
		{"/v1/embeddings", Embeddings},
		{"/v1/engines/gpt-3.5-turbo/embeddings", Embeddings},
		{"/v1/moderations", Moderations},
		{"/v1/images/generations", ImagesGenerations},
		{"/v1/audio/transcriptions", AudioTranscription},
		{"/v1/rerank", Rerank},
		{"/v1/responses", Responses},
		{"/v1/videos", Videos},
		{"/v1/oneapi/proxy/1/anything", Proxy},
	}
	for _, tc := range cases {
		if got := GetByPath(tc.path); got != tc.mode {
			t.Fatalf("%s 应仍然映射到模式 %d，实际 %d", tc.path, tc.mode, got)
		}
	}
	// /v1/models 本身是本地模型列表接口，不属于任何中继模式
	if got := GetByPath("/v1/models"); got != Unknown {
		t.Fatalf("/v1/models 应保持 Unknown，实际 %d", got)
	}
	// 非透传模式不应被 IsPassthrough 认领
	for _, mode := range []int{Unknown, ChatCompletions, Responses, Videos, Proxy} {
		if IsPassthrough(mode) {
			t.Fatalf("模式 %d 不该属于透传家族", mode)
		}
	}
}

// TestPassthroughModeNames 验证新模式都有稳定名，且能被 scope 反查。
// 名字会写进数据库、被管理员配置引用，这里同时固定住拼写（改名属于破坏性变更）。
func TestPassthroughModeNames(t *testing.T) {
	cases := map[int]string{
		Files:      "files",
		FineTuning: "fine_tuning",
		Assistants: "assistants",
		Threads:    "threads",
		Models:     "models",
	}
	for mode, name := range cases {
		if got := Name(mode); got != name {
			t.Fatalf("模式 %d 的名称应为 %s，实际 %s", mode, name, got)
		}
		if !IsValidName(name) {
			t.Fatalf("%s 应能被反查为合法模式名", name)
		}
		if parsed := ParseAllowedModes(name); parsed == nil || !parsed[mode] {
			t.Fatalf("%s 应能解析为放行模式 %d，实际 %v", name, mode, parsed)
		}
	}
	// 模式名不得重复（重复会让 scope 配置指向错误的模式）
	seen := map[string]int{}
	for mode, name := range modeNames {
		if other, ok := seen[name]; ok {
			t.Fatalf("模式名 %s 被模式 %d 与 %d 重复占用", name, other, mode)
		}
		seen[name] = mode
	}
}
