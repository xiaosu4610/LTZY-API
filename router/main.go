package router

import (
	"embed"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/controller"
	"net/http"
	"os"
	"strings"
)

func SetRouter(router *gin.Engine, buildFS embed.FS) {
	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	// 健康检查（LTZY-API 新增，D7）：注册为顶层路由，不带 gzip/限流/鉴权，
	// 保证探针在任何中间件异常时仍可探测到进程本身
	router.GET("/healthz", controller.Healthz)
	router.GET("/readyz", controller.Readyz)
	// Prometheus 抓取端点（LTZY-API 新增，D3）
	router.GET("/metrics", controller.Metrics)
	frontendBaseUrl := os.Getenv("FRONTEND_BASE_URL")
	if config.IsMasterNode && frontendBaseUrl != "" {
		frontendBaseUrl = ""
		logger.SysLog("FRONTEND_BASE_URL is ignored on master node")
	}
	if frontendBaseUrl == "" {
		SetWebRouter(router, buildFS)
	} else {
		frontendBaseUrl = strings.TrimSuffix(frontendBaseUrl, "/")
		router.NoRoute(func(c *gin.Context) {
			c.Redirect(http.StatusMovedPermanently, fmt.Sprintf("%s%s", frontendBaseUrl, c.Request.RequestURI))
		})
	}
}
