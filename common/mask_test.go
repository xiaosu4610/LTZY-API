package common

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMaskSecret 覆盖单值打码的边界：空值、短凭据、长凭据。
// 重点是「短凭据不留任何原文」，否则 8 位密钥露出 4 位等于泄露一半。
func TestMaskSecret(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"空输入返回空串", "", ""},
		{"纯空白视为空输入", "   ", ""},
		{"短凭据整体打码", "abc123", "***"},
		{"刚好等于保留长度的凭据也整体打码", "12345678", "***"},
		{"长凭据保留首尾各 4 位", "sk-1234567890abcdef", "sk-1***cdef"},
		{"多字节字符按字符截断", "密钥一二三四五六七八九十", "密钥一二***七八九十"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaskSecret(tc.input); got != tc.want {
				t.Fatalf("MaskSecret(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestIsSensitiveKey 验证字段名清单：各种写法都要命中，正常字段名不能被误判。
func TestIsSensitiveKey(t *testing.T) {
	sensitive := []string{
		"key", "Key", "KEY",
		"password", "Password", "passwd", "pwd",
		"access_token", "AccessToken", "api_key", "apiKey", "API-KEY",
		"secret", "client_secret", "clientSecret", "secret_key",
		"authorization", "cookie", "session",
		"sk", "private_key", "vertex_ai_adc",
	}
	for _, k := range sensitive {
		if !IsSensitiveKey(k) {
			t.Fatalf("字段 %q 应判定为敏感字段", k)
		}
	}
	// 这些字段名容易被「后缀模糊匹配」误伤（TokenName 里有 Token、ModelName 里有 Name），
	// 一旦误判，日志里就看不到「哪个令牌/哪个模型」了，所以必须保持不敏感。
	notSensitive := []string{"SystemName", "ModelName", "TokenName", "username", "quota", "models", "id", ""}
	for _, k := range notSensitive {
		if IsSensitiveKey(k) {
			t.Fatalf("字段 %q 不应判定为敏感字段", k)
		}
	}
}

// TestMaskJSONBodySensitiveKeys 覆盖「各类敏感 key」：顶层平铺形态。
func TestMaskJSONBodySensitiveKeys(t *testing.T) {
	body := `{
		"key": "sk-channel-secret",
		"api_key": "api-key-secret",
		"apiKey": "camel-api-key-secret",
		"password": "plain-password",
		"passwd": "plain-passwd",
		"secret": "top-secret",
		"client_secret": "client-secret",
		"access_token": "access-token-value",
		"authorization": "Bearer whatever",
		"token": "token-value",
		"sk": "sk-value",
		"private_key": "-----BEGIN PRIVATE KEY-----",
		"vertex_ai_adc": "{\"private_key\":\"x\"}",
		"cookie": "session=abc",
		"id": 7,
		"username": "root",
		"model": "gpt-4o",
		"quota": 100,
		"TokenName": "default"
	}`
	masked := MaskJSONBody([]byte(body))
	if masked == "" {
		t.Fatal("合法 JSON 不应返回空串")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(masked), &parsed); err != nil {
		t.Fatalf("脱敏结果必须是合法 JSON，实际: %s", masked)
	}
	for _, k := range []string{"key", "api_key", "apiKey", "password", "passwd", "secret",
		"client_secret", "access_token", "authorization", "token", "sk", "private_key",
		"vertex_ai_adc", "cookie"} {
		if parsed[k] != maskPlaceholder {
			t.Fatalf("敏感字段 %q 未被打码，实际值: %v", k, parsed[k])
		}
	}
	// 非敏感字段必须原样保留，否则日志/审计会丢可读信息
	if parsed["username"] != "root" || parsed["model"] != "gpt-4o" {
		t.Fatalf("非敏感字段被误改: %v", parsed)
	}
	if parsed["id"] != float64(7) || parsed["quota"] != float64(100) || parsed["TokenName"] != "default" {
		t.Fatalf("非敏感字段被误改: %v", parsed)
	}
	// 原文里的任何一个密钥串都不能残留
	for _, leaked := range []string{"sk-channel-secret", "api-key-secret", "camel-api-key-secret",
		"plain-password", "top-secret", "access-token-value", "token-value", "session=abc"} {
		if strings.Contains(masked, leaked) {
			t.Fatalf("脱敏结果仍包含敏感内容 %q: %s", leaked, masked)
		}
	}
}

// TestMaskJSONBodyNested 覆盖「嵌套结构」：对象套对象、对象数组、字符串里再套 JSON。
func TestMaskJSONBodyNested(t *testing.T) {
	body := `{
		"channel": {"id": 3, "name": "主力渠道", "key": "sk-nested-key", "group": "default"},
		"user": {"id": 1, "password": "hash-value", "profile": {"access_token": "at-value"}},
		"tokens": [{"id": 1, "key": "sk-array-key"}, {"id": 2, "key": "sk-array-key-2"}],
		"config": "{\"region\":\"cn\",\"sk\":\"config-sk\",\"ak\":\"ak-visible\"}"
	}`
	masked := MaskJSONBody([]byte(body))
	if masked == "" {
		t.Fatal("合法 JSON 不应返回空串")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(masked), &parsed); err != nil {
		t.Fatalf("脱敏结果必须是合法 JSON，实际: %s", masked)
	}

	channel := parsed["channel"].(map[string]any)
	if channel["key"] != maskPlaceholder || channel["name"] != "主力渠道" {
		t.Fatalf("嵌套对象打码错误: %v", channel)
	}
	user := parsed["user"].(map[string]any)
	if user["password"] != maskPlaceholder {
		t.Fatalf("嵌套对象内的密码未打码: %v", user)
	}
	if user["profile"].(map[string]any)["access_token"] != maskPlaceholder {
		t.Fatalf("二层嵌套内的 access_token 未打码: %v", user)
	}
	for i, item := range parsed["tokens"].([]any) {
		if item.(map[string]any)["key"] != maskPlaceholder {
			t.Fatalf("数组第 %d 个元素的 key 未打码: %v", i, item)
		}
	}
	// config 是「JSON 字符串套 JSON」：字段名 config 本身不敏感，但里面的 sk 必须打码，
	// 这是渠道编辑接口最容易漏的一条（Vertex AI 的 adc 私钥也藏在这种字符串里）
	config, _ := parsed["config"].(string)
	if !strings.Contains(config, `"sk":"`+maskPlaceholder+`"`) {
		t.Fatalf("字符串内嵌 JSON 的 sk 未打码: %s", config)
	}
	if strings.Contains(masked, "config-sk") {
		t.Fatalf("脱敏结果仍包含嵌套密钥: %s", masked)
	}
	// ak 只是访问键标识（不是机密），保留便于运维辨认用的是哪把钥匙
	if !strings.Contains(masked, "ak-visible") {
		t.Fatalf("非机密字段 ak 被误打码: %s", masked)
	}
}

// TestMaskJSONBodyInvalidInput 覆盖「非 JSON 输入」与空输入。
// 约定：无法解析时返回空串，由调用方决定丢弃明细 —— 绝不能原样返回，
// 否则一段含密钥的乱码会被直接写进日志/审计表。
func TestMaskJSONBodyInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"nil 输入", nil},
		{"空输入", []byte("")},
		{"纯文本", []byte("this is not json, key=sk-leak")},
		{"截断的 JSON", []byte(`{"key": "sk-leak"`)},
		{"multipart 片段", []byte("--boundary\r\nContent-Disposition: form-data\r\n\r\npassword=secret123")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskJSONBody(tc.body)
			if got != "" {
				t.Fatalf("非 JSON/空输入应返回空串，实际: %q", got)
			}
			if strings.Contains(got, "sk-leak") || strings.Contains(got, "secret123") {
				t.Fatalf("非 JSON 输入不得返回原文: %q", got)
			}
		})
	}
}

// TestMaskString 覆盖文本级兜底：上游报错、URL、header、结构体打印形态。
func TestMaskString(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantStay  []string // 必须保留（便于排障的结构信息）
		wantGone  []string // 必须消失（凭据原文）
		wantExact string
	}{
		{
			name:     "Bearer 头",
			input:    "Authorization: Bearer sk-abcdef123456",
			wantGone: []string{"abcdef123456"},
			wantStay: []string{"Authorization"},
		},
		{
			name:     "上游把 api_key 放在 query（*url.Error 会带回整条 URL）",
			input:    `Get "https://api.openai-sb.com/sb-api/user/status?api_key=sk-live-abcdef123456": dial tcp: i/o timeout`,
			wantGone: []string{"sk-live-abcdef123456"},
			wantStay: []string{"api.openai-sb.com", "dial tcp"},
		},
		{
			name:     "百度渠道 access_token 在 query 上",
			input:    "do request failed: Get \"https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/ernie_speed?access_token=24.abcdefghijklmnop.123456-789\": EOF",
			wantGone: []string{"abcdefghijklmnop"},
			wantStay: []string{"aip.baidubce.com", "access_token="},
		},
		{
			name:     "Go 结构体 %+v 打印",
			input:    "record log: {Id:1 Username:root Password:$2a$10$abcdefghijklmnopqrstuv Key:sk-live-xyz}",
			wantGone: []string{"$2a$10$abcdefghijklmnopqrstuv", "live-xyz"},
			wantStay: []string{"Username:root"},
		},
		{
			name:      "正常日志原样保留",
			input:     "channel #3 has been disabled: 余额不足",
			wantExact: "channel #3 has been disabled: 余额不足",
		},
		{
			name:      "token 统计字段不能被误伤",
			input:     "usage: PromptTokens:30 CompletionTokens:128 TotalTokens:158",
			wantExact: "usage: PromptTokens:30 CompletionTokens:128 TotalTokens:158",
		},
		{
			name:      "空输入返回空串",
			input:     "",
			wantExact: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskString(tc.input)
			if tc.wantExact != "" || tc.input == "" {
				if got != tc.wantExact {
					t.Fatalf("MaskString(%q) = %q, want %q", tc.input, got, tc.wantExact)
				}
				return
			}
			for _, gone := range tc.wantGone {
				if strings.Contains(got, gone) {
					t.Fatalf("凭据 %q 未被脱敏: %s", gone, got)
				}
			}
			for _, stay := range tc.wantStay {
				if !strings.Contains(got, stay) {
					t.Fatalf("可排障信息 %q 不应被脱敏: %s", stay, got)
				}
			}
		})
	}
}

// TestMaskBodyForLog 覆盖日志入口的四种形态：JSON、非 JSON、空、超长。
func TestMaskBodyForLog(t *testing.T) {
	t.Run("JSON 走字段级打码", func(t *testing.T) {
		got := MaskBodyForLog([]byte(`{"username":"root","password":"plain-pwd"}`), "application/json")
		if strings.Contains(got, "plain-pwd") {
			t.Fatalf("请求体中的明文密码未脱敏: %s", got)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("脱敏后的 JSON 必须仍可解析: %s", got)
		}
		if parsed["username"] != "root" {
			t.Fatalf("非敏感字段被误改: %v", parsed)
		}
	})

	t.Run("未声明 Content-Type 但内容是 JSON", func(t *testing.T) {
		got := MaskBodyForLog([]byte(`{"channel":{"key":"sk-live-abc"}}`), "")
		if strings.Contains(got, "sk-live-abc") {
			t.Fatalf("JSON 请求体中的渠道密钥未脱敏: %s", got)
		}
	})

	t.Run("非 JSON 走凭据形态兜底", func(t *testing.T) {
		got := MaskBodyForLog([]byte("password=secret123&token=abcdefgh"), "application/x-www-form-urlencoded")
		if strings.Contains(got, "secret123") || strings.Contains(got, "abcdefgh") {
			t.Fatalf("表单体中的凭据未脱敏: %s", got)
		}
		if got == "" {
			t.Fatal("非 JSON 请求体不应被整体丢弃（否则排障没有线索）")
		}
	})

	t.Run("空输入返回空串", func(t *testing.T) {
		if got := MaskBodyForLog(nil, "application/json"); got != "" {
			t.Fatalf("空输入应返回空串，实际: %q", got)
		}
	})

	t.Run("超长非 JSON 请求体截断", func(t *testing.T) {
		got := MaskBodyForLog([]byte(strings.Repeat("a", maxLogBodyLen*2)), "application/octet-stream")
		if !strings.HasSuffix(got, "(truncated)") {
			t.Fatalf("超长请求体应被截断: len=%d", len(got))
		}
		if len(got) > maxLogBodyLen+32 {
			t.Fatalf("截断后长度异常: %d", len(got))
		}
	})
}
