package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
)

func setAuditOptions(t *testing.T, words string, action string) {
	t.Helper()
	config.OptionMapRWMutex.Lock()
	if config.OptionMap == nil {
		config.OptionMap = make(map[string]string)
	}
	config.OptionMap["SensitiveWords"] = words
	config.OptionMap["SensitiveWordsAction"] = action
	config.OptionMapRWMutex.Unlock()
}

func newAuditRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ContentAudit())
	router.POST("/v1/chat/completions", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return router
}

func postAudit(router *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestContentAuditDisabled 词表为空时零开销放行
func TestContentAuditDisabled(t *testing.T) {
	setAuditOptions(t, "", "block")
	defer setAuditOptions(t, "", "block")

	router := newAuditRouter()
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"任何内容都应放行"}]}`
	if rec := postAudit(router, body); rec.Code != http.StatusOK {
		t.Fatalf("未配置词表时应放行，实际 %d", rec.Code)
	}
}

// TestContentAuditBlock 命中敏感词时拒绝
func TestContentAuditBlock(t *testing.T) {
	setAuditOptions(t, "违禁词,另一个词", "block")
	defer setAuditOptions(t, "", "block")

	router := newAuditRouter()

	rec := postAudit(router, `{"model":"gpt-4o","messages":[{"role":"user","content":"这里包含违禁词内容"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("命中敏感词应 400，实际 %d", rec.Code)
	}
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非法 JSON：%s", rec.Body.String())
	}
	if resp.Error.Code != "sensitive_content_detected" {
		t.Fatalf("错误码应为 sensitive_content_detected，实际 %s", resp.Error.Code)
	}

	// 未命中的请求必须放行
	if rec := postAudit(router, `{"model":"gpt-4o","messages":[{"role":"user","content":"正常提问"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("未命中应放行，实际 %d", rec.Code)
	}
}

// TestContentAuditLogOnly log 模式只记录不拦截（上线前观察误伤率用）
func TestContentAuditLogOnly(t *testing.T) {
	setAuditOptions(t, "违禁词", "log")
	defer setAuditOptions(t, "", "block")

	router := newAuditRouter()
	if rec := postAudit(router, `{"messages":[{"role":"user","content":"包含违禁词"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("log 模式不应拦截，实际 %d", rec.Code)
	}
}

// TestExtractAuditableText 只取用户输入字段，不把模型名/参数值算进去
func TestExtractAuditableText(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
		not  []string
	}{
		{
			name: "chat 字符串 content",
			body: `{"model":"gpt-4o","temperature":0.7,"messages":[{"role":"user","content":"你好"}]}`,
			want: []string{"你好"},
			not:  []string{"gpt-4o", "0.7"},
		},
		{
			name: "chat parts 数组 content",
			body: `{"messages":[{"role":"user","content":[{"type":"text","text":"图片说明"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`,
			want: []string{"图片说明"},
			not:  []string{"AAAA"},
		},
		{
			name: "completions prompt",
			body: `{"model":"m","prompt":"续写这段文字"}`,
			want: []string{"续写这段文字"},
		},
		{
			name: "embeddings input",
			body: `{"input":"要嵌入的文本"}`,
			want: []string{"要嵌入的文本"},
		},
		{
			name: "非 JSON 不误判",
			body: `并不是 JSON`,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAuditableText([]byte(tc.body))
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("应提取到 %q，实际 %q", want, got)
				}
			}
			for _, notWant := range tc.not {
				if strings.Contains(got, notWant) {
					t.Fatalf("不应提取到 %q，实际 %q", notWant, got)
				}
			}
		})
	}
}

// TestSensitiveWordsParsing 词表解析：逗号/换行/分号分隔、去重、大小写不敏感
func TestSensitiveWordsParsing(t *testing.T) {
	setAuditOptions(t, " Bad, bad\n词二;词三 ,, ", "block")
	defer setAuditOptions(t, "", "block")

	words := sensitiveWords()
	if len(words) != 3 {
		t.Fatalf("应解析出 3 个词（去重后），实际 %d：%v", len(words), words)
	}
	for _, w := range words {
		if w != strings.ToLower(w) {
			t.Fatalf("词应统一小写，实际 %q", w)
		}
	}
	// 大小写不敏感匹配
	router := newAuditRouter()
	if rec := postAudit(router, `{"messages":[{"role":"user","content":"这里有 BAD 字样"}]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("大小写不敏感匹配应命中，实际 %d", rec.Code)
	}
}

// TestContentAuditDoesNotConsumeBody 审核不应吞掉下游请求体
func TestContentAuditDoesNotConsumeBody(t *testing.T) {
	setAuditOptions(t, "违禁词", "block")
	defer setAuditOptions(t, "", "block")

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(ctxkey.Id, 1) })
	router.Use(ContentAudit())
	var seen string
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		body, _ := common.GetRequestBody(c)
		seen = string(body)
		c.String(http.StatusOK, "ok")
	})

	body := `{"messages":[{"role":"user","content":"正常内容"}]}`
	if rec := postAudit(router, body); rec.Code != http.StatusOK {
		t.Fatalf("应放行，实际 %d", rec.Code)
	}
	if seen != body {
		t.Fatalf("下游应读到完整请求体，实际 %q", seen)
	}
}
