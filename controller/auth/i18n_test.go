package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/i18n"
)

// getWeChatIdByCode 的错误会通过 err.Error() 原样回显给调用方，
// 所以它也必须按请求语言取文案。code 为空时函数在发起网络请求前就返回，测试不需要网络。
func TestGetWeChatIdByCodeLocalizesErrorMessage(t *testing.T) {
	cases := []struct {
		lang string
		want string
	}{
		{"zh-CN", "无效的参数"},
		{"en", "invalid parameter"},
		{"fr", "[fr] invalid parameter"},
		{"de-DE", "invalid parameter"},
	}
	for _, tc := range cases {
		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/api/oauth/wechat", nil)
		c.Set(i18n.ContextKey, tc.lang)

		if _, err := getWeChatIdByCode(c, ""); err == nil {
			t.Fatalf("lang=%q: 空 code 应当报错", tc.lang)
		} else if err.Error() != tc.want {
			t.Errorf("lang=%q: message = %q，期望 %q", tc.lang, err.Error(), tc.want)
		}
	}
}
