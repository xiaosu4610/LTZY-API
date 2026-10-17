package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
)

// newScopeTestRouter 构造一个只挂 TokenScope 的最小路由，便于隔离测试。
// allowedModes 会被写入 context，模拟 TokenAuth 的产物。
func newScopeTestRouter(allowedModes string, setCtx bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if setCtx {
			c.Set(ctxkey.TokenAllowedModes, allowedModes)
		}
	})
	router.Use(TokenScope())
	router.POST("/v1/chat/completions", func(c *gin.Context) { c.String(http.StatusOK, "chat-ok") })
	router.POST("/v1/embeddings", func(c *gin.Context) { c.String(http.StatusOK, "embedding-ok") })
	router.POST("/v1/images/generations", func(c *gin.Context) { c.String(http.StatusOK, "image-ok") })
	router.POST("/v1/files", func(c *gin.Context) { c.String(http.StatusOK, "placeholder-ok") })
	return router
}

func doScopeRequest(router *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestTokenScopeUnset 未配置 scope（TokenAuth 没写 context）时必须全放行
func TestTokenScopeUnset(t *testing.T) {
	router := newScopeTestRouter("", false)
	for _, path := range []string{"/v1/chat/completions", "/v1/images/generations"} {
		if rec := doScopeRequest(router, path); rec.Code != http.StatusOK {
			t.Fatalf("%s 未配置 scope 时应放行，实际 %d", path, rec.Code)
		}
	}
}

// TestTokenScopeEmptyValue 配置为空串时也应全放行（等价于未限制）
func TestTokenScopeEmptyValue(t *testing.T) {
	router := newScopeTestRouter("", true)
	if rec := doScopeRequest(router, "/v1/images/generations"); rec.Code != http.StatusOK {
		t.Fatalf("空 scope 应放行，实际 %d", rec.Code)
	}
}

// TestTokenScopeAllowsConfigured 允许的模式放行、未允许的拒绝
func TestTokenScopeAllowsConfigured(t *testing.T) {
	router := newScopeTestRouter("chat,embeddings", true)

	if rec := doScopeRequest(router, "/v1/chat/completions"); rec.Code != http.StatusOK {
		t.Fatalf("chat 在允许列表内应放行，实际 %d", rec.Code)
	}
	if rec := doScopeRequest(router, "/v1/embeddings"); rec.Code != http.StatusOK {
		t.Fatalf("embeddings 在允许列表内应放行，实际 %d", rec.Code)
	}

	rec := doScopeRequest(router, "/v1/images/generations")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("images 不在允许列表内应 403，实际 %d", rec.Code)
	}
	// 错误码与提示必须是约定的，便于客户端识别
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON：%s", rec.Body.String())
	}
	if body.Error.Code != "token_scope_denied" {
		t.Fatalf("错误码应为 token_scope_denied，实际 %s", body.Error.Code)
	}
	if body.Error.Message == "" {
		t.Fatal("应给出中文提示")
	}
}

// TestTokenScopeUnknownPathNotBlocked 未知路径不应被 scope 拦成 403
// （例如 /v1/files 由占位处理器返回 501，不能被误报为无权限）
func TestTokenScopeUnknownPathNotBlocked(t *testing.T) {
	router := newScopeTestRouter("chat", true)
	if rec := doScopeRequest(router, "/v1/files"); rec.Code != http.StatusOK {
		t.Fatalf("未知模式路径应交由后续处理器，实际 %d", rec.Code)
	}
}

// TestTokenScopeInvalidConfigNotLocking 配置全是非法模式名时不应把令牌锁死
func TestTokenScopeInvalidConfigNotLocking(t *testing.T) {
	router := newScopeTestRouter("bad1,bad2", true)
	if rec := doScopeRequest(router, "/v1/images/generations"); rec.Code != http.StatusOK {
		t.Fatalf("非法配置应视为不限制，实际 %d", rec.Code)
	}
}
