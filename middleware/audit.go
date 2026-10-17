package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 审计中间件（LTZY-API 新增，D4）。
//
// 设计：透明包一层管理端点 —— 请求处理完成后把「谁 / 什么动作 / 对象 / 来源」
// 异步写入 audit_logs 表。放在 c.Next() 之后按响应码判定结果，不侵入业务逻辑。
//
// 关键细节：gin 的 ShouldBindJSON 会读空 body 且不写回缓存，因此必须在 c.Next()
// 之前抢先读取并还原 body（captureBodyForAudit），否则审计明细永远为空。
//
// 脱敏：body 中的 key/secret/password 等字段在写入前统一打码，
// 审计明细只用于人读回溯，宁可丢失细节也不能把渠道密钥写进审计表。

// maxAuditBodySize 审计明细的 body 上限：超过则不记明细（管理面 JSON 通常很小，
// 这个阈值只用于防御异常大请求把审计表撑大）。
const maxAuditBodySize = 8192

// AuditOptionUpdate 系统设置写入的专用审计（D4）。
//
// 为什么要单独处理：UpdateOption 的 body 是 {"key":"SystemName","value":"xxx"}，
// 这里的 key 是「配置项名称」而非机密，若走通用打码会被误判成 key 字段而打码，
// 导致「改了哪个设置」这一审计核心信息丢失；而 value 恰恰可能是密钥
// （SMTPToken / GitHubClientSecret / TurnstileSecretKey 等），必须打码。
// 因此：名称记为对象标识，值一律打码。
func AuditOptionUpdate() gin.HandlerFunc {
	return func(c *gin.Context) {
		rawBody := captureBodyForAudit(c)
		c.Next()
		target := ""
		detail := ""
		var payload struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(rawBody, &payload); err == nil && payload.Key != "" {
			target = payload.Key
			detail = `{"value":"***"}`
		}
		recordAuditWithDetail(c, "option.update", target, detail)
	}
}

// AuditAction 用给定动作名包装 handler，自动记录审计日志。
// target 为固定对象标识；动作名建议用 resource.verb 形式（channel.create）。
func AuditAction(action string, target string) gin.HandlerFunc {
	return newAuditHandler(action, func(c *gin.Context) string { return target })
}

// AuditActionFromParam 同上，但对象标识取自路径参数（如 /api/channel/:id 的 id）。
func AuditActionFromParam(action string, paramName string) gin.HandlerFunc {
	return newAuditHandler(action, func(c *gin.Context) string { return c.Param(paramName) })
}

func newAuditHandler(action string, targetFunc func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawBody := captureBodyForAudit(c)
		c.Next()
		recordAudit(c, action, targetFunc(c), rawBody)
	}
}

// captureBodyForAudit 在业务 handler 消费 body 之前读取并还原请求体。
// 仅处理 application/json：multipart（音频/图片上传）直接跳过，避免把大文件读进内存。
// 读取结果同时写入 ctxkey 缓存，使后续 UseBodyReusable 的 handler 命中缓存而不是重复读。
func captureBodyForAudit(c *gin.Context) []byte {
	if !strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json") {
		return nil
	}
	if c.Request.Body == nil {
		return nil
	}
	body, err := common.GetRequestBody(c)
	if err != nil {
		return nil
	}
	// GetRequestBody 只读取不还原，这里补上，否则业务 handler 会读到空 body
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
	if len(body) == 0 || len(body) > maxAuditBodySize {
		return nil
	}
	return body
}

func recordAudit(c *gin.Context, action string, target string, rawBody []byte) {
	detail := ""
	if len(rawBody) > 0 {
		detail = maskSensitiveFields(string(rawBody))
		// 对象标识缺省时尝试从 body 里取 id，让「改了哪个渠道/令牌」可回溯
		if target == "" {
			target = extractIdFromBody(rawBody)
		}
	}
	recordAuditWithDetail(c, action, target, detail)
}

// recordAuditWithDetail 审计写入的最终出口。
// 只审计已鉴权的管理面请求；authHelper 会把 id/username 写进 context。
func recordAuditWithDetail(c *gin.Context, action string, target string, detail string) {
	userId := c.GetInt("id")
	if userId == 0 {
		return
	}
	go model.RecordAuditLog(c.Request.Context(), &model.AuditLog{
		UserId:   userId,
		Username: c.GetString("username"),
		Action:   action,
		Target:   target,
		Detail:   detail,
		IP:       c.ClientIP(),
	})
}

// extractIdFromBody 从请求体里取数值型 id（管理面 PUT 请求普遍带 id）。
func extractIdFromBody(body []byte) string {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	switch id := parsed["id"].(type) {
	case float64:
		return fmt.Sprintf("%d", int(id))
	case string:
		return id
	}
	return ""
}

// maskSensitiveFields 把 JSON 顶层与嵌套对象里的敏感字段值打码为 ***。
// 解析失败（如 multipart、非 JSON）时丢弃明细，只保留动作与对象。
func maskSensitiveFields(body string) string {
	var parsed any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return ""
	}
	out, err := json.Marshal(maskValue(parsed))
	if err != nil {
		return ""
	}
	return string(out)
}

// sensitiveKeys 需要打码的字段名（小写比较）。
var sensitiveKeys = map[string]bool{
	"key":          true,
	"secret":       true,
	"password":     true,
	"access_token": true,
	"api_key":      true,
	"session":      true,
	"sk":           true,
}

func maskValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			if sensitiveKeys[strings.ToLower(k)] {
				out[k] = "***"
				continue
			}
			out[k] = maskValue(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = maskValue(vv)
		}
		return out
	default:
		return v
	}
}
