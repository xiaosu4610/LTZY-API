package middleware

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMaskSensitiveFields 验证审计明细中的敏感字段被打码（D4）。
// 渠道密钥、密码、令牌一旦进入审计表就是二次泄露，必须锁死。
func TestMaskSensitiveFields(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantHas []string
		wantNot []string
	}{
		{
			name:    "渠道密钥打码",
			body:    `{"name":"测试渠道","key":"sk-abcdef123456","type":1}`,
			wantHas: []string{"测试渠道", `"key":"***"`},
			wantNot: []string{"sk-abcdef123456"},
		},
		{
			name:    "密码打码",
			body:    `{"username":"root","password":"P@ssw0rd"}`,
			wantHas: []string{"root", `"password":"***"`},
			wantNot: []string{"P@ssw0rd"},
		},
		{
			name:    "大小写不敏感",
			body:    `{"Key":"secret-key-value"}`,
			wantHas: []string{`"Key":"***"`},
			wantNot: []string{"secret-key-value"},
		},
		{
			name:    "嵌套对象内的密钥也打码",
			body:    `{"config":{"secret":"nested-secret"},"name":"ok"}`,
			wantHas: []string{`"secret":"***"`, `"name":"ok"`},
			wantNot: []string{"nested-secret"},
		},
		{
			name:    "数组元素内的密钥也打码",
			body:    `[{"key":"a-secret"}]`,
			wantHas: []string{`"key":"***"`},
			wantNot: []string{"a-secret"},
		},
		{
			name:    "access_token 打码",
			body:    `{"access_token":"tok-123456"}`,
			wantHas: []string{`"access_token":"***"`},
			wantNot: []string{"tok-123456"},
		},
		{
			// 渠道的 config 是「JSON 字符串套 JSON」，内含 sk 与 Vertex 服务账号私钥。
			// 早期只按字段名打码的实现会让这串凭据原样落库（E5 发现的泄露点）。
			name:    "JSON 字符串套 JSON 也要递归打码",
			body:    `{"name":"c1","config":"{\"region\":\"us\",\"sk\":\"vertex-secret-key\",\"vertex_ai_adc\":\"-----BEGIN PRIVATE KEY-----\"}"}`,
			wantHas: []string{"c1", "us"},
			wantNot: []string{"vertex-secret-key", "BEGIN PRIVATE KEY"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := maskSensitiveFields(tc.body)
			for _, want := range tc.wantHas {
				if !strings.Contains(got, want) {
					t.Fatalf("应保留 %q，实际输出: %s", want, got)
				}
			}
			for _, notWant := range tc.wantNot {
				if strings.Contains(got, notWant) {
					t.Fatalf("敏感内容 %q 未被打码，实际输出: %s", notWant, got)
				}
			}
			// 结果必须是可解析的 JSON（审计明细要能被人和程序读）
			if !json.Valid([]byte(got)) {
				t.Fatalf("脱敏结果不是合法 JSON: %s", got)
			}
		})
	}
}

// TestMaskSensitiveFieldsNonJSON 验证非 JSON body 不会 panic，且不落敏感内容
func TestMaskSensitiveFieldsNonJSON(t *testing.T) {
	got := maskSensitiveFields("this is not json, key=sk-leak")
	if got != "" {
		t.Fatalf("非 JSON body 应丢弃明细（返回空），got %q", got)
	}
	if got := maskSensitiveFields(""); got != "" {
		t.Fatalf("空 body 应返回空，got %q", got)
	}
}

// TestExtractIdFromBody 验证能从请求体取对象 id，供审计回溯「改了哪个对象」
func TestExtractIdFromBody(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{`{"id":3,"name":"c"}`, "3"},
		{`{"id":12.0}`, "12"},
		{`{"id":"abc"}`, "abc"},
		{`{"name":"no-id"}`, ""},
		{`not-json`, ""},
	}
	for _, tc := range cases {
		if got := extractIdFromBody([]byte(tc.body)); got != tc.want {
			t.Fatalf("body %s: got %q, want %q", tc.body, got, tc.want)
		}
	}
}
