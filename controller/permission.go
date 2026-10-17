package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 权限查询接口（LTZY-API 新增，C4）。
//
// 用途：前端据此决定菜单/按钮的可见性，避免"点了才被告知没权限"。

// GetPermissionList 返回全部可配置的权限点，以及当前登录者自己的权限。
// 走 UserAuth 即可：权限点清单不是机密，自己的权限更是本人信息。
func GetPermissionList(c *gin.Context) {
	role := c.GetInt(ctxkey.Role)
	permissions := model.GetRolePermissions(role)
	if permissions == nil {
		permissions = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			// 全部可选权限点（管理员配置角色时用）
			"all": model.AllPermissions(),
			// 当前角色的权限（前端用它控制菜单显隐）
			"self": permissions,
			"role": role,
		},
	})
}
