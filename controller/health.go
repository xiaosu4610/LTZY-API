package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 健康检查端点（LTZY-API 新增，D7）。
//
// 设计：
//   - /healthz  进程存活探针（liveness）：只要 HTTP 服务在响应就返回 200，
//     不依赖任何下游组件，避免 DB 抖动导致容器被无谓重启；
//   - /readyz   就绪探针（readiness）：检查数据库（及可选的 Redis）连通性，
//     任一失败返回 503，负载均衡/网关据此摘除流量。
//
// 两个端点都不鉴权：K8s/Nginx 探针不携带会话，且响应里不包含任何敏感信息。

// Healthz 存活探针。
func Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

// Readyz 就绪探针：主库可查 + Redis（若启用）可 PING。
func Readyz(c *gin.Context) {
	checks := gin.H{}
	healthy := true

	// 主库：轻量查询验证连通
	if err := model.PingDB(); err != nil {
		checks["database"] = "down: " + err.Error()
		healthy = false
	} else {
		checks["database"] = "up"
	}

	// Redis：仅在启用时检查；未启用不算失败（降级运行是合法状态）
	if common.RedisEnabled {
		if err := common.PingRedis(); err != nil {
			checks["redis"] = "down: " + err.Error()
			healthy = false
		} else {
			checks["redis"] = "up"
		}
	} else {
		checks["redis"] = "disabled"
	}

	code := http.StatusOK
	if !healthy {
		code = http.StatusServiceUnavailable
	}
	c.JSON(code, gin.H{
		"status": map[bool]string{true: "ok", false: "unavailable"}[healthy],
		"node":  map[bool]string{true: "master", false: "slave"}[config.IsMasterNode],
		"checks": checks,
	})
}
