package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/model"
)

func newPermissionRouter(role int, perm string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(ctxkey.Role, role) })
	router.GET("/guarded", PermissionAuth(perm), func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return router
}

func doPermissionRequest(router *gin.Engine) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/guarded", nil))
	return rec
}

// TestPermissionAuthAllowsAdminByDefault 默认配置下 admin 应能通过（与改造前 AdminAuth 等价）
func TestPermissionAuthAllowsAdminByDefault(t *testing.T) {
	router := newPermissionRouter(model.RoleAdminUser, model.PermChannelRead)
	if rec := doPermissionRequest(router); rec.Code != http.StatusOK {
		t.Fatalf("默认配置下 admin 应通过 channel.read，实际 %d", rec.Code)
	}
}

// TestPermissionAuthDeniesCommonUser 普通用户应被拒
func TestPermissionAuthDeniesCommonUser(t *testing.T) {
	router := newPermissionRouter(model.RoleCommonUser, model.PermChannelRead)
	rec := doPermissionRequest(router)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("普通用户应 403，实际 %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "permission_denied") {
		t.Fatalf("错误响应应包含 permission_denied 码，实际 %s", body)
	}
}

// TestPermissionAuthRootAlwaysAllowed root 恒通过（无论配置如何）
func TestPermissionAuthRootAlwaysAllowed(t *testing.T) {
	router := newPermissionRouter(model.RoleRootUser, model.PermOptionWrite)
	if rec := doPermissionRequest(router); rec.Code != http.StatusOK {
		t.Fatalf("root 应恒通过，实际 %d", rec.Code)
	}
}

// TestPermissionAuthMissingRoleIsDenied 没有 role（未鉴权直接调用）时应拒绝，而不是放行
func TestPermissionAuthMissingRoleIsDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// 故意不设置 role，模拟中间件顺序写错（PermissionAuth 跑在 auth 之前）
	router.GET("/guarded", PermissionAuth(model.PermChannelRead), func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/guarded", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("缺少角色上下文时应拒绝（避免中间件顺序写错导致越权），实际 %d", rec.Code)
	}
}
