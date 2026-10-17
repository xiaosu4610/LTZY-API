package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/model"
)

// GetAuditLogs 审计日志分页查询（LTZY-API 新增，D4）。
// 参数：p（页码，默认 1）、page_size（默认 10）、action（可选过滤）。
func GetAuditLogs(c *gin.Context) {
	pageNum, _ := strconv.Atoi(c.Query("p"))
	if pageNum <= 0 {
		pageNum = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	if pageSize <= 0 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100 // 防止一次拖垮日志库
	}
	action := c.Query("action")

	logs, total, err := model.GetAuditLogs(pageNum, pageSize, action)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items": logs,
			"total": total,
			"p":     pageNum,
		},
	})
}
