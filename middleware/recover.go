package middleware

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"net/http"
	"runtime/debug"
)

func RelayPanicRecover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				ctx := c.Request.Context()
				logger.Errorf(ctx, fmt.Sprintf("panic detected: %v", err))
				logger.Errorf(ctx, fmt.Sprintf("stacktrace from panic: %s", string(debug.Stack())))
				logger.Errorf(ctx, fmt.Sprintf("request: %s %s", c.Request.Method, c.Request.URL.Path))
				body, _ := common.GetRequestBody(c)
				// panic 现场常常出现在管理面请求（登录、渠道增删改）上，而这些请求体里
				// 直接是明文密码或上游渠道密钥；日志文件会长期留存并被日志平台采集，
				// 所以先脱敏再落盘（JSON 按字段打码，其它形态退回凭据形态兜底）。
				logger.Errorf(ctx, fmt.Sprintf("request body: %s", common.MaskBodyForLog(body, c.Request.Header.Get("Content-Type"))))
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": gin.H{
						"message": fmt.Sprintf("Panic detected, error: %v. Please submit an issue with the related log here: https://github.com/xiaosu4610/LTZY-API", err),
						"type":    "one_api_panic",
					},
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}
