package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 权限点校验中间件（LTZY-API 新增，C4）。
//
// 用法：`middleware.UserAuth(), middleware.PermissionAuth(model.PermChannelRead), handler`
//
// 为什么在 UserAuth 之后：UserAuth（authHelper）负责验身份并把 role 写进 context，
// 本中间件只做「这个角色有没有这个权限点」的判断。
//
// 与 AdminAuth 的关系：AdminAuth 是「角色数值 >= 10」的粗粒度判断，
// 在内置默认配置下，PermissionAuth 与它的行为**完全等价**（见 model/permission.go
// 的 defaultRolePermissions），因此可以逐路由平滑替换，不会改变现有权限。
// 替换之后，管理员就能通过 RolePermissions 精细调整、甚至新建自定义角色了。
//
// 安全底线：root 角色恒有全部权限（由 model.HasPermission 保证），
// 因此不存在「配置写错把自己锁在系统外面」的风险。
func PermissionAuth(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := c.GetInt(ctxkey.Role)
		if model.HasPermission(role, permission) {
			c.Next()
			return
		}
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "无权进行此操作，缺少权限：" + permission,
			"code":    "permission_denied",
		})
		c.Abort()
	}
}
