package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 租户作用域中间件（LTZY-API 新增，C5）。
//
// 只挂在**需要按租户隔离的管理类路由**上（用户列表、日志列表与统计等），
// 而不是全局挂载。原因：
//   - 自助类接口（/api/user/self、/api/token 等）本来就只操作本人数据，
//     再插一次租户判定只是白白多一次查询；
//   - 显式挂载让"哪些接口受租户隔离"一目了然，而不是隐含在全局中间件里。
//
// 语义：把调用者所属租户写入 context；model 层的查询函数都要求显式传入 tenantId，
// 因此"忘记过滤"会直接变成编译错误，而不是悄悄泄漏数据。
//
// 失败策略：**宁可拒绝也不放行**。查不到调用者的租户时按 500 拒绝，
// 绝不降级为 0（0 = 平台视角 = 能看到所有租户的数据），否则一次数据库抖动
// 就会变成跨租户数据泄漏。
func TenantScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		userId := c.GetInt(ctxkey.Id)
		if userId == 0 {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "无法确定调用者身份，拒绝访问",
				"code":    "tenant_unresolved",
			})
			c.Abort()
			return
		}
		tenantId, err := model.GetUserTenantId(userId)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "无法确定调用者所属租户，已拒绝访问",
				"code":    "tenant_unresolved",
			})
			c.Abort()
			return
		}
		c.Set(ctxkey.TenantId, tenantId)
		c.Next()
	}
}
