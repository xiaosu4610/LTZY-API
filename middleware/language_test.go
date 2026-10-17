package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/i18n"
)

// TestLanguageNegotiation 验证 Language 中间件把 Accept-Language 协商结果写入 context（E6 接线）。
//
// 背景：原实现「只认 zh*→zh-CN，其余一律 en」，导致 i18n 新增的
// fr/ru/es/ar 词条在生产请求下永远选不中（文件加了等于没加）。
func TestLanguageNegotiation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		header string
		want   string
	}{
		{"zh-CN", "zh-CN"},
		{"zh", "zh-CN"},
		{"zh-TW", "zh-CN"}, // 繁体也归到中文资源
		{"en-US", "en"},
		{"fr", "fr"},
		{"fr-FR,fr;q=0.9,en;q=0.8", "fr"},
		{"ru-RU", "ru"},
		{"es", "es"},
		{"ar", "ar"},
		{"", i18n.DefaultLang},
		{"!!garbage!!", i18n.DefaultLang},
	}
	for _, tc := range cases {
		var got string
		router := gin.New()
		router.Use(Language())
		router.GET("/probe", func(c *gin.Context) {
			got = c.GetString(i18n.ContextKey)
			c.Status(http.StatusOK)
		})
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		if tc.header != "" {
			req.Header.Set("Accept-Language", tc.header)
		}
		router.ServeHTTP(httptest.NewRecorder(), req)
		if got != tc.want {
			t.Fatalf("Accept-Language %q 应协商为 %q，实际 %q", tc.header, tc.want, got)
		}
	}
}
