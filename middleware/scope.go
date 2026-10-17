package middleware

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/relay/relaymode"
)

// 令牌接口 scope 中间件（LTZY-API 新增，C3）。
//
// 目的：把「一个令牌只能干某些事」变成可配置能力。典型场景：
//   - 只发给聊天客户端用的令牌，禁止其调用图像/视频等昂贵接口；
//   - 只做 embedding 的令牌，避免被拿去跑对话。
//
// 设计：
//   - 依据请求路径解析中继模式（relaymode.GetByPath），与实现细节解耦，
//     因此新增 /v1 路由时天然被覆盖，不会漏防；
//   - 令牌未配置 scope（空）时直接放行，保证既有令牌行为不变；
//   - 配置存在但内容全是非法模式名时按"未限制"处理（relaymode.ParseAllowedModes
//     返回 nil），避免管理员写错一个词就把令牌彻底锁死。
//
// 必须放在 TokenAuth 之后（依赖其写入的 ctxkey.TokenAllowedModes）。
func TokenScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, exists := c.Get(ctxkey.TokenAllowedModes)
		if !exists {
			c.Next()
			return
		}
		rawStr, _ := raw.(string)
		allowed := relaymode.ParseAllowedModes(rawStr)
		if allowed == nil {
			// 未配置或配置无效 → 不限制
			c.Next()
			return
		}

		mode := relaymode.GetByPath(c.Request.URL.Path)
		if relaymode.Name(mode) == "" {
			// 未知路径（例如仍是 501 占位的 /v1/images/edits）：交给后续处理器决定，
			// 不在这里拦，避免把「未实现」误报成「无权限」。
			// 注意：/v1/files、/v1/assistants、/v1/threads、/v1/fine_tuning、
			// /v1/models/{model} 自 A6 起已是受 scope 约束的正常中继模式，不再走这一分支。
			c.Next()
			return
		}
		if allowed[mode] {
			c.Next()
			return
		}

		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("该令牌不允许调用此接口（%s），如需使用请让管理员在令牌配置中放开",
					relaymode.Name(mode)),
				"type": "one_api_error",
				"code": "token_scope_denied",
			},
		})
		c.Abort()
	}
}
