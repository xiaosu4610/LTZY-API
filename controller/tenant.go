package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 租户管理接口（LTZY-API 新增，C5）。
//
// 全部要求 RootAuth：租户的创建与归属变更会直接改变数据可见范围，
// 属于最高危操作，不授权给普通管理员（与系统设置同级）。

func GetAllTenants(c *gin.Context) {
	p, _ := strconv.Atoi(c.Query("p"))
	if p < 0 {
		p = 0
	}
	tenants, err := model.GetAllTenants(p*config.ItemsPerPage, config.ItemsPerPage)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if tenants == nil {
		tenants = []*model.Tenant{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": tenants})
}

func GetTenant(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "id 格式错误"})
		return
	}
	tenant, err := model.GetTenantById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": tenant})
}

func AddTenant(c *gin.Context) {
	var tenant model.Tenant
	if err := c.ShouldBindJSON(&tenant); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	if err := common.Validate.Struct(&tenant); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "输入不合法：" + err.Error()})
		return
	}
	// 同名租户会让"按名字找租户"这类运维操作变得不确定，直接拒绝
	if _, err := model.GetTenantByName(tenant.Name); err == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "同名租户已存在"})
		return
	}
	if err := tenant.Insert(); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": tenant.Id})
}

func UpdateTenant(c *gin.Context) {
	var tenant model.Tenant
	if err := c.ShouldBindJSON(&tenant); err != nil || tenant.Id == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	if err := common.Validate.Struct(&tenant); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "输入不合法：" + err.Error()})
		return
	}
	if err := tenant.Update(); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func DeleteTenant(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "id 格式错误"})
		return
	}
	tenant, err := model.GetTenantById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := tenant.Delete(); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}
