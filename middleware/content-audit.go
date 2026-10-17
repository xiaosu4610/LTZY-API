package middleware

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/logger"
)

// 内容审核 / 敏感词拦截（LTZY-API 新增，E3）。
//
// 目的：在网关侧先拦一道违规输入，而不是把问题全丢给上游。
// 合规场景下（境内提供生成式服务）这是最低成本的自我约束手段。
//
// 设计取舍：
//   - **只取用户输入字段**（content / prompt / input / text）做审核，而不是整包扫字符串。
//     整包扫描会把模型名、base64 图片、参数值一起算进去，既误伤又浪费 CPU。
//   - 命中后默认**直接拒绝**（400）；也可通过 SensitiveWordsAction=log 只记日志，
//     便于上线前先观察一段时间再切拦截，避免误伤正常业务。
//   - 词表为空时零开销直接放行（不做任何解析）。
//   - 命中时在响应里回显命中的词：词表由管理员自己配置，不是秘密；
//     明确告知比含糊拒绝更利于用户改正。若你不这么认为，把该字段去掉即可。
//
// 范围说明：本中间件只审「请求」；**响应侧审核未做** —— 流式响应需要在
// SSE 逐块解析时审核，改动面大且会引入缓冲延迟，建议作为独立任务评估。

// maxAuditTextLength 单次审核的文本上限，防止超长输入把正则/扫描变成 CPU 打满的入口。
const maxAuditTextLength = 256 * 1024

// auditableKeys 认定为「用户输入」的字段名（小写比较）。
// 覆盖 OpenAI 系列接口的常见形态：
//   - chat/completions: messages[].content（字符串或 parts 数组）
//   - completions:      prompt
//   - embeddings:       input
//   - moderations:      input
//   - images:           prompt
var auditableKeys = map[string]bool{
	"content": true,
	"prompt":  true,
	"input":   true,
	"text":    true,
}

// ContentAudit 敏感词审核中间件。
// 必须放在能读到请求体的位置（本项目在 SignatureVerify 之后、Distribute 之前）。
func ContentAudit() gin.HandlerFunc {
	return func(c *gin.Context) {
		words := sensitiveWords()
		if len(words) == 0 {
			c.Next()
			return
		}

		body, err := common.GetRequestBody(c)
		if err != nil || len(body) == 0 {
			c.Next()
			return
		}
		text := strings.ToLower(extractAuditableText(body))
		if text == "" {
			c.Next()
			return
		}

		hit := matchSensitiveWord(text, words)
		if hit == "" {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		if sensitiveWordsAction() == "log" {
			// 观察模式：只记日志，放行。上线初期用它评估误伤率。
			logger.Warnf(ctx, "sensitive content detected (log only): user %d, word %q, path %s",
				c.GetInt("id"), hit, c.Request.URL.Path)
			c.Next()
			return
		}

		logger.Warnf(ctx, "sensitive content blocked: user %d, word %q, path %s",
			c.GetInt("id"), hit, c.Request.URL.Path)
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "请求内容包含不被允许的词汇：" + hit,
				"type":    "one_api_error",
				"code":    "sensitive_content_detected",
			},
		})
		c.Abort()
	}
}

// sensitiveWords 读取并规整敏感词表（支持逗号与换行分隔、去空白、转小写、去重）。
func sensitiveWords() []string {
	config.OptionMapRWMutex.RLock()
	raw := config.OptionMap["SensitiveWords"]
	config.OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	seen := map[string]bool{}
	words := make([]string, 0, 16)
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ';'
	}) {
		word := strings.ToLower(strings.TrimSpace(part))
		if word == "" || seen[word] {
			continue
		}
		seen[word] = true
		words = append(words, word)
	}
	return words
}

// sensitiveWordsAction 审核动作：block（默认）或 log。
func sensitiveWordsAction() string {
	config.OptionMapRWMutex.RLock()
	action := config.OptionMap["SensitiveWordsAction"]
	config.OptionMapRWMutex.RUnlock()
	if strings.EqualFold(strings.TrimSpace(action), "log") {
		return "log"
	}
	return "block"
}

// matchSensitiveWord 返回第一个命中的词（空串表示未命中）。
// 用朴素子串匹配而非正则：词表是管理员维护的普通词，正则会带来
// 编译开销与 ReDoS 风险，收益却几乎没有。
func matchSensitiveWord(text string, words []string) string {
	for _, word := range words {
		if strings.Contains(text, word) {
			return word
		}
	}
	return ""
}

// extractAuditableText 从请求体里抽出需要审核的文本，用换行拼接。
// 解析失败（非 JSON、multipart）时返回空串 —— 宁可漏审也不误判整包二进制。
func extractAuditableText(body []byte) string {
	if len(body) > maxAuditTextLength {
		body = body[:maxAuditTextLength]
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	var b strings.Builder
	collectAuditableText(parsed, &b)
	return b.String()
}

func collectAuditableText(v any, b *strings.Builder) {
	switch val := v.(type) {
	case map[string]any:
		for key, child := range val {
			if auditableKeys[strings.ToLower(key)] {
				// 命中用户输入字段：把这个值里所有字符串收集起来即可
				collectAllStrings(child, b)
				continue
			}
			// 其他字段（如 messages）继续下钻，以便找到里面的 content
			collectAuditableText(child, b)
		}
	case []any:
		for _, child := range val {
			collectAuditableText(child, b)
		}
	}
}

// textLikeKeys 在 content parts 数组里真正承载用户文本的字段名。
// 只认这两个：parts 里还可能有 image_url（base64 大块）等非文本内容，
// 把它们一起扫既浪费 CPU，也容易撞出莫名其妙的"敏感词"。
var textLikeKeys = map[string]bool{
	"text":    true,
	"content": true,
}

// collectAllStrings 收集结构里的用户文本（用于 content 可能是字符串
// 或 parts 数组两种形态）。
func collectAllStrings(v any, b *strings.Builder) {
	switch val := v.(type) {
	case string:
		b.WriteString(val)
		b.WriteByte('\n')
	case []any:
		for _, child := range val {
			collectAllStrings(child, b)
		}
	case map[string]any:
		for key, child := range val {
			if textLikeKeys[strings.ToLower(key)] {
				collectAllStrings(child, b)
			}
		}
	}
}
