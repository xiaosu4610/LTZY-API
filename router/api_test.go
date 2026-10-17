package router

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestSetApiRouterRegistersMidjourneyRoutes 校验 MJ 路由能注册且不与既有路径冲突（A4）。
//
// gin 的路由树在注册期就会因路径冲突 panic，而 /mj/tasks 与 /mj/task/:id/fetch
// 恰好共享 "task" 前缀，属于只能靠真实注册才能验证的边界，因此这里直接跑一次 SetApiRouter。
func TestSetApiRouterRegistersMidjourneyRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	registered := map[string]bool{}
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	expected := []string{
		http.MethodPost + " /api/mj/submit",
		http.MethodGet + " /api/mj/tasks",
		http.MethodGet + " /api/mj/task/:id/fetch",
		http.MethodGet + " /api/mj/admin/tasks",
	}
	for _, route := range expected {
		if !registered[route] {
			t.Fatalf("路由未注册：%s", route)
		}
	}
}
