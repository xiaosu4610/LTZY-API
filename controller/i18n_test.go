package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/i18n"
	"github.com/xiaosu4610/LTZY-API/model"
)

// newI18nTestContext 构造指定请求语言的 gin 上下文。
// 这里直接写 ContextKey 而不再走 middleware.Language()：
// 语言协商本身由 common/i18n 的测试覆盖，本文件只关心「controller 的响应文案是否跟着请求语言走」。
func newI18nTestContext(t *testing.T, lang string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/token", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	if lang != "" {
		c.Set(i18n.ContextKey, lang)
	}
	return c, w
}

// validateToken 是纯校验函数（不碰数据库），适合用来锁死「参数校验错误文案」的本地化行为。
func TestValidateTokenMessageFollowsLanguage(t *testing.T) {
	longName := strings.Repeat("a", 31)
	cases := []struct {
		lang string
		want string
	}{
		{"zh-CN", "令牌名称过长"},
		{"en", "The token name is too long"},
		{"fr", "[fr] The token name is too long"},
		{"de-DE", "The token name is too long"}, // 未支持的语言回退英文
		{"", "The token name is too long"},      // 没写语言 -> 默认英文
	}
	for _, tc := range cases {
		c, _ := newI18nTestContext(t, tc.lang)
		err := validateToken(c, model.Token{Name: longName})
		if err == nil {
			t.Fatalf("lang=%q: 期望校验失败", tc.lang)
		}
		if err.Error() != tc.want {
			t.Errorf("lang=%q: message = %q，期望 %q", tc.lang, err.Error(), tc.want)
		}
		if strings.Contains(err.Error(), "token.name_too_long") {
			t.Errorf("lang=%q: 键名泄漏到文案：%q", tc.lang, err.Error())
		}
	}
}

func TestValidateTokenFormattedMessageFollowsLanguage(t *testing.T) {
	notASubnet := "not-a-subnet"

	c, _ := newI18nTestContext(t, "zh-CN")
	err := validateToken(c, model.Token{Subnet: &notASubnet})
	if err == nil {
		t.Fatal("非法网段应当校验失败")
	}
	if !strings.HasPrefix(err.Error(), "无效的网段：") || !strings.Contains(err.Error(), notASubnet) {
		t.Errorf("中文网段文案不正确：%q", err.Error())
	}

	c, _ = newI18nTestContext(t, "en")
	err = validateToken(c, model.Token{Subnet: &notASubnet})
	if err == nil {
		t.Fatal("非法网段应当校验失败")
	}
	// 前缀是本地化文案，后面拼接的是 network.IsValidSubnets 返回的底层原因（不翻译）
	if !strings.HasPrefix(err.Error(), "Invalid subnet: ") || !strings.Contains(err.Error(), notASubnet) {
		t.Errorf("英文网段文案不正确：%q", err.Error())
	}
}

func TestValidateTokenAllowedModesMessageFollowsLanguage(t *testing.T) {
	badMode := "not-a-mode"

	c, _ := newI18nTestContext(t, "zh-CN")
	err := validateToken(c, model.Token{AllowedModes: &badMode})
	if err == nil {
		t.Fatal("非法接口范围应当校验失败")
	}
	// NamesHelp() 由 map 遍历生成，取得值集合无序，因此只断言前缀与合法值存在
	if !strings.HasPrefix(err.Error(), "无效的接口范围：not-a-mode（可选值：") || !strings.Contains(err.Error(), "chat") {
		t.Errorf("中文接口范围文案不正确：%q", err.Error())
	}

	c, _ = newI18nTestContext(t, "en")
	err = validateToken(c, model.Token{AllowedModes: &badMode})
	if err == nil {
		t.Fatal("非法接口范围应当校验失败")
	}
	if !strings.HasPrefix(err.Error(), "Invalid API scope: not-a-mode (available values: ") {
		t.Errorf("英文接口范围文案不正确：%q", err.Error())
	}
}

func TestValidateTokenNumericLimitsFollowLanguage(t *testing.T) {
	cases := []struct {
		name  string
		token model.Token
		zh    string
		en    string
	}{
		{"限速为负", model.Token{Rpm: -1}, "限速值不能为负数", "The rate limit cannot be negative"},
		{"并发为负", model.Token{Concurrency: -1}, "并发上限不能为负数", "The concurrency limit cannot be negative"},
		{"预算为负", model.Token{DailyQuotaLimit: -1}, "预算上限不能为负数", "The budget limit cannot be negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newI18nTestContext(t, "zh-CN")
			if err := validateToken(c, tc.token); err == nil || err.Error() != tc.zh {
				t.Errorf("中文文案 = %v，期望 %q", err, tc.zh)
			}
			c, _ = newI18nTestContext(t, "en")
			if err := validateToken(c, tc.token); err == nil || err.Error() != tc.en {
				t.Errorf("英文文案 = %v，期望 %q", err, tc.en)
			}
		})
	}
}

// TestAddTokenResponseMessageIsLocalized 走一次真实的 handler + JSON 响应，
// 锁死「c.JSON 里的 message 字段」也随请求语言变化（且参数校验失败时不会触碰数据库）。
func TestAddTokenResponseMessageIsLocalized(t *testing.T) {
	body := `{"name":"` + strings.Repeat("a", 31) + `"}`
	cases := []struct{ lang, want string }{
		{"zh-CN", "参数错误：令牌名称过长"},
		{"en", "Invalid parameters: The token name is too long"},
	}
	for _, tc := range cases {
		c, w := newI18nTestContext(t, tc.lang)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/token", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")

		AddToken(c)

		var resp struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("lang=%q: 解析响应失败: %v（原始内容 %s）", tc.lang, err, w.Body.String())
		}
		if resp.Success {
			t.Errorf("lang=%q: 期望 success=false，响应 %s", tc.lang, w.Body.String())
		}
		if resp.Message != tc.want {
			t.Errorf("lang=%q: message = %q，期望 %q", tc.lang, resp.Message, tc.want)
		}
	}
}
