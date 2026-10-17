package common

import (
	"encoding/json"
	"regexp"
	"strings"
)

// 敏感信息脱敏工具（E5）。
//
// 为什么单独抽一层：审计中间件（middleware/audit.go）里已有一份只作用于「请求体」
// 的字段打码逻辑，但真正会长期留存敏感信息的是另外三类出口 —— 落盘日志、告警通知、
// 错误信息回显（上游报错里经常把密钥原文一起吐回来）。这些出口散落在 relay、model、
// middleware 各处，各写一份正则必然漏；因此在这里提供与出口无关的纯函数：
//
//	MaskSecret      单个凭据值（渠道 Key / API Key）打码，保留首尾少量字符便于运维辨认
//	MaskString      任意文本里的凭据形态（Bearer/sk-/query 参数/头部/字段）打码
//	MaskJSONBody    JSON 请求体按字段名打码，能覆盖嵌套对象、数组与「JSON 字符串套 JSON」
//	MaskBodyForLog  日志专用入口：JSON 走字段级，其它形态走凭据形态兜底并截断
//	IsSensitiveKey  字段名是否属于敏感字段（供其它出口复用同一份清单）
//
// 设计取舍：脱敏宁可过度（多盖一个非机密字段只是少看一点信息），不可漏（漏一个就是
// 密钥进日志/进响应）。因此对无法识别的非 JSON 文本也做兜底匹配，而不是原样输出。

// maskPlaceholder 打码后的统一占位符，便于日志检索与人工核对。
const maskPlaceholder = "***"

// maxLogBodyLen 日志中非 JSON 请求体的长度上限：
// 超过则截断，避免一次 panic 把大文件（音频/图片上传）整段写进日志。
const maxLogBodyLen = 2048

// MaskSecret 对单个凭据值打码，保留前 4 与后 4 个字符。
//
// 为什么保留首尾：渠道 Key 动辄 40+ 字符，保留首尾足以让运维确认「配的是哪一把」，
// 中间部分被遮蔽后无法还原出可用凭据；而短凭据（<=8 字符）保留任何字符都可能暴露
// 大半内容，直接整体打码。空值返回空串，用于区分「未配置」与「已配置但被打码」。
func MaskSecret(secret string) string {
	s := strings.TrimSpace(secret)
	if s == "" {
		return ""
	}
	const keep = 4
	runes := []rune(s)
	if len(runes) <= keep*2 {
		return maskPlaceholder
	}
	return string(runes[:keep]) + maskPlaceholder + string(runes[len(runes)-keep:])
}

// sensitiveKeys 需要打码的字段名（小写比较）。
// 清单在 middleware/audit.go 的基础上做了补充：上游报错与渠道配置里还会出现
// authorization / private_key / adc 这类凭据字段，审计那份清单没有覆盖。
var sensitiveKeys = map[string]bool{
	"key":           true, // 渠道 Key、令牌 Key 都叫这个字段名
	"apikey":        true, // apiKey / api_key 归一化后命中
	"api_key":       true,
	"secret":        true,
	"client_secret": true,
	"clientsecret":  true, // Go 字段名形态（ClientSecret）
	"secret_key":    true,
	"secretkey":     true,
	"apisecret":     true,
	"password":      true,
	"passwd":        true,
	"pwd":           true,
	"access_token":  true,
	"accesstoken":   true, // Go 字段名形态（AccessToken）
	"refreshtoken":  true,
	"refresh_token": true,
	"auth_token":    true,
	"authtoken":     true,
	"token":         true,
	"authorization": true,
	"cookie":        true,
	"session":       true,
	"sk":            true, // 渠道 config 里的 SecretKey
	"private_key":   true,
	"privatekey":    true,
	"adc":           true, // Vertex AI 服务账号 JSON（内含私钥）
	"vertex_ai_adc": true,
	"vertexaiadc":   true,
}

// IsSensitiveKey 判断字段名是否为敏感字段（忽略大小写、下划线与连字符差异）。
// 导出的目的是让后续新增的出口（如新的审计/导出功能）复用同一份清单，而不是各写一份。
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if sensitiveKeys[k] {
		return true
	}
	// 只做「去分隔符」的等价匹配，不做后缀模糊匹配：
	// 后缀匹配会把 SystemName、ModelName 之类正常字段误判成敏感字段。
	normalized := strings.NewReplacer("_", "", "-", "").Replace(k)
	return sensitiveKeys[normalized]
}

// MaskJSONBody 把 JSON 里的敏感字段值打码为 ***，返回打码后的 JSON 字符串。
//
// 与审计中间件的同名逻辑保持一致的两点约定：
//  1. 解析失败（非 JSON）时返回空串，由调用方决定是否丢弃明细 —— 宁可不记，
//     也不能把一段无法识别、可能含密钥的文本原样写进日志或数据库；
//  2. 打码后仍是合法 JSON，保证调用方可以直接落库/落盘。
//
// 额外增强：字符串值里如果再套一层 JSON（渠道 config 就是这种形态，
// 里面含 sk / vertex_ai_adc），会递归解析后再打码，避免「外层字段名命中不了」的漏网。
func MaskJSONBody(body []byte) string {
	masked, ok := maskJSON(body)
	if !ok {
		return ""
	}
	return masked
}

func maskJSON(body []byte) (string, bool) {
	if len(body) == 0 {
		return "", false
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", false
	}
	out, err := json.Marshal(MaskJSONValue(parsed))
	if err != nil {
		return "", false
	}
	return string(out), true
}

// MaskJSONValue 递归打码已经解析出来的 JSON 值（map/slice/标量）。
// 导出是为了让调用方在已经解析过请求体的场景下直接复用，避免重复反序列化。
func MaskJSONValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			if IsSensitiveKey(k) {
				out[k] = maskPlaceholder
				continue
			}
			out[k] = MaskJSONValue(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = MaskJSONValue(vv)
		}
		return out
	case string:
		// 字段名本身不敏感（如 config），但值可能是 JSON 字符串，里面藏着 sk/private key。
		// 只有确认是对象/数组字面量时才递归解析，避免把普通字符串（公告、模型名）改形，
		// 也避免为每个普通字符串都付一次反序列化的代价。
		if looksLikeJSONContainer(val) {
			if nested, ok := maskJSON([]byte(val)); ok {
				return nested
			}
		}
		return val
	default:
		return v
	}
}

// looksLikeJSONContainer 判断字符串是否是一个 JSON 对象/数组字面量。
func looksLikeJSONContainer(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")
}

// maskStringPatterns 文本级凭据形态匹配规则。
//
// 为什么按「形态」兜底：非 JSON 文本（上游报错、结构体 %v 打印、URL）没有字段名可依据，
// 只能识别密钥本身的特征。规则顺序无关，逐条独立生效。
var maskStringPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// Authorization / x-api-key 等头部或键值对形态
	{regexp.MustCompile(`(?i)\b(authorization|x-api-key|x-goog-api-key|api-key|cookie)\s*[:=]\s*[^\s,;"']{4,}`), "$1: " + maskPlaceholder},
	// URL / form 里的凭据参数：上游把 key、access_token 放在 query 时，报错原文会带回整条 URL
	{regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|auth[_-]?token|refresh[_-]?token|client[_-]?secret|secret|password|passwd|token|key)=[^&\s"'<>]{4,}`), "$1=" + maskPlaceholder},
	// OpenAI 风格密钥前缀
	{regexp.MustCompile(`sk-[A-Za-z0-9_\-]{6,}`), "sk-" + maskPlaceholder},
	// AWS Access Key ID（渠道配置里常整串出现）
	{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "AKIA" + maskPlaceholder},
	// 带引号的字段形态："password":"xxx"
	{regexp.MustCompile(`(?i)"(password|passwd|access_token|refresh_token|api_key|apikey|client_secret|private_key|secret|token|key)"\s*:\s*"[^"]{1,}"`), `"$1":"` + maskPlaceholder + `"`},
	// Go 结构体 %v/%+v 打印形态：{Id:1 Password:$2a$10$... Key:sk-xxx}
	// 限定整词（\b）与冒号，避免误伤 CompletionTokens:30 这类正常字段
	{regexp.MustCompile(`(?i)\b(AccessToken|Password|Passwd|APIKey|ApiKey|ClientSecret|SecretKey|PrivateKey|Secret|Token|Sk|Key)\s*:\s*[^\s,;}]+`), "$1: " + maskPlaceholder},
}

// MaskString 对任意文本里的凭据形态做兜底打码。
// 只替换「看起来是凭据」的片段，普通错误信息与 URL 结构保持不变，便于排障。
func MaskString(text string) string {
	if text == "" {
		return ""
	}
	out := text
	for _, p := range maskStringPatterns {
		out = p.re.ReplaceAllString(out, p.repl)
	}
	return out
}

// MaskBodyForLog 生成可以安全写进日志的请求体文本。
//
// 为什么需要它：多次 panic 现场（含管理面登录、渠道增删改）都打过原始 body，
// 里面直接是明文密码或上游密钥，而日志文件会长期留存、还会被同步到日志平台。
//   - JSON：按字段名逐层打码，最准确；
//   - 其它（表单 / multipart / 纯文本）：退回凭据形态兜底，并截断长度，
//     避免大文件体把日志撑爆（也避免二进制内容里的偶然密钥落盘）。
func MaskBodyForLog(body []byte, contentType string) string {
	if len(body) == 0 {
		return ""
	}
	isJSON := strings.Contains(strings.ToLower(contentType), "json")
	if isJSON || looksLikeJSONContainer(string(body)) {
		if masked, ok := maskJSON(body); ok {
			return masked
		}
	}
	text := MaskString(string(body))
	if len(text) > maxLogBodyLen {
		// 按字节截断：日志只用于定位，不需要完整内容
		text = text[:maxLogBodyLen] + "...(truncated)"
	}
	return text
}
