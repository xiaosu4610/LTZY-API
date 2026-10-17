package middleware

import (
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/config"
)

// CORS 跨域中间件（LTZY-API 改造）。
//
// 安全说明：
//   - 原实现无条件 AllowAllOrigins，任何站点都能携带凭据跨域调用，存在 CSRF 风险；
//   - 现在支持通过环境变量 CORS_ALLOW_ORIGINS 配置白名单（逗号分隔）；
//   - 未配置白名单时保持原行为（允许所有来源），保证开箱即用与平滑迁移。
func CORS() gin.HandlerFunc {
	cfg := cors.DefaultConfig()
	cfg.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	cfg.AllowHeaders = []string{"*"}

	origins := parseCorsAllowOrigins(config.CorsAllowOrigins)
	if len(origins) == 0 {
		// 未配置白名单：沿用原行为
		cfg.AllowAllOrigins = true
		cfg.AllowCredentials = true
		return cors.New(cfg)
	}

	cfg.AllowOrigins = origins
	// 配置了具体来源时允许携带凭据；gin-contrib/cors 会自动回显匹配到的 Origin
	cfg.AllowCredentials = true
	return cors.New(cfg)
}

// parseCorsAllowOrigins 解析逗号分隔的来源白名单，自动去除空白与末尾斜杠。
func parseCorsAllowOrigins(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(p), "/"))
		if p != "" {
			origins = append(origins, p)
		}
	}
	return origins
}
