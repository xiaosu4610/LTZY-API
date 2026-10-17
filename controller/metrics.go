package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/metrics"
)

// Metrics Prometheus 抓取端点（LTZY-API 新增，D3）。
// 暴露为顶层 /metrics，无鉴权：Prometheus 抓取器不带会话，
// 指标内容不含任何密钥/用户数据，仅有聚合计数与运行时信息。
// 如需对外屏蔽，建议在反向代理层限制来源 IP。
func Metrics(c *gin.Context) {
	c.Data(http.StatusOK, "text/plain; version=0.0.4; charset=utf-8", []byte(metrics.RenderPrometheus()))
}
