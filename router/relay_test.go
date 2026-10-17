package router

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestSetRelayRouterRegistersPassthroughEndpoints 校验 A6 改造后的路由注册结果：
//   - files / fine_tuning / assistants / threads / 删除微调模型这批端点都挂在 /v1 组下；
//   - 它们指向的是真正的中继控制器（controller.Relay），而不是 501 占位处理器。
//
// gin 的路由树在注册期就会因路径冲突 panic，所以这里直接跑一次 SetRelayRouter。
func TestSetRelayRouterRegistersPassthroughEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	handlers := map[string]string{}
	for _, route := range engine.Routes() {
		handlers[route.Method+" "+route.Path] = route.Handler
	}

	expected := []string{
		http.MethodGet + " /v1/files",
		http.MethodPost + " /v1/files",
		http.MethodDelete + " /v1/files/:id",
		http.MethodGet + " /v1/files/:id",
		http.MethodGet + " /v1/files/:id/content",
		http.MethodPost + " /v1/fine_tuning/jobs",
		http.MethodGet + " /v1/fine_tuning/jobs",
		http.MethodGet + " /v1/fine_tuning/jobs/:id",
		http.MethodPost + " /v1/fine_tuning/jobs/:id/cancel",
		http.MethodGet + " /v1/fine_tuning/jobs/:id/events",
		http.MethodDelete + " /v1/models/:model",
		http.MethodPost + " /v1/assistants",
		http.MethodGet + " /v1/assistants",
		http.MethodGet + " /v1/assistants/:id",
		http.MethodPost + " /v1/assistants/:id",
		http.MethodDelete + " /v1/assistants/:id",
		http.MethodPost + " /v1/assistants/:id/files",
		http.MethodGet + " /v1/assistants/:id/files",
		http.MethodGet + " /v1/assistants/:id/files/:fileId",
		http.MethodDelete + " /v1/assistants/:id/files/:fileId",
		http.MethodPost + " /v1/threads",
		http.MethodGet + " /v1/threads/:id",
		http.MethodPost + " /v1/threads/:id",
		http.MethodDelete + " /v1/threads/:id",
		http.MethodPost + " /v1/threads/:id/messages",
		http.MethodGet + " /v1/threads/:id/messages/:messageId",
		http.MethodPost + " /v1/threads/:id/messages/:messageId",
		http.MethodGet + " /v1/threads/:id/messages/:messageId/files",
		http.MethodGet + " /v1/threads/:id/messages/:messageId/files/:filesId",
		http.MethodPost + " /v1/threads/:id/runs",
		http.MethodGet + " /v1/threads/:id/runs",
		http.MethodGet + " /v1/threads/:id/runs/:runsId",
		http.MethodPost + " /v1/threads/:id/runs/:runsId",
		http.MethodPost + " /v1/threads/:id/runs/:runsId/submit_tool_outputs",
		http.MethodPost + " /v1/threads/:id/runs/:runsId/cancel",
		http.MethodGet + " /v1/threads/:id/runs/:runsId/steps",
		http.MethodGet + " /v1/threads/:id/runs/:runsId/steps/:stepId",
	}
	for _, route := range expected {
		handler, ok := handlers[route]
		if !ok {
			t.Fatalf("路由未注册：%s", route)
		}
		if !strings.HasSuffix(handler, "controller.Relay") {
			t.Fatalf("%s 应指向中继控制器，实际挂的是 %s", route, handler)
		}
	}

	// 本次改造范围之外的占位端点必须保持原样（避免顺手改出计划外的行为）
	for _, route := range []string{
		http.MethodPost + " /v1/images/edits",
		http.MethodPost + " /v1/images/variations",
	} {
		if handler, ok := handlers[route]; !ok || !strings.HasSuffix(handler, "controller.RelayNotImplemented") {
			t.Fatalf("%s 应保持 501 占位，实际 %s", route, handler)
		}
	}
}
