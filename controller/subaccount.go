package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 子账号接口（LTZY-API 新增，C6）。
//
// 全部走 UserAuth：这是"个人/团队内部的钱包管理"，不是平台管理能力。
// 每个接口都必须校验「目标用户确实是调用者的子账号」，
// 否则任何一个登录用户都能改别人的额度。

func GetChildAccounts(c *gin.Context) {
	parentId := c.GetInt(ctxkey.Id)
	children, err := model.GetChildAccounts(parentId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if children == nil {
		children = []*model.User{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": children})
}

func AddChildAccount(c *gin.Context) {
	parentId := c.GetInt(ctxkey.Id)
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		InitQuota   int64  `json:"init_quota"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	// 复用与建用户相同的字段校验规则（用户名长度、密码长度等）
	probe := model.User{Username: req.Username, Password: req.Password, DisplayName: req.DisplayName}
	if err := common.Validate.Struct(&probe); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "输入不合法：" + err.Error()})
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = req.Username
	}
	childId, err := model.AddChildAccount(parentId, req.Username, req.Password, req.DisplayName, req.InitQuota)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": childId})
}

// childIdFromParam 解析并校验「子账号归属」，是下面所有接口的公共前置检查。
func childIdFromParam(c *gin.Context) (int, bool) {
	parentId := c.GetInt(ctxkey.Id)
	childId, err := strconv.Atoi(c.Param("id"))
	if err != nil || childId == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "id 格式错误"})
		return 0, false
	}
	ok, err := model.IsChildOf(childId, parentId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return 0, false
	}
	if !ok {
		// 不区分"不存在"与"不是你的子账号"，避免被用来探测别人的用户 id
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "该用户不是你的子账号"})
		return 0, false
	}
	return childId, true
}

func TransferQuotaToChild(c *gin.Context) {
	childId, ok := childIdFromParam(c)
	if !ok {
		return
	}
	var req struct {
		Quota int64 `json:"quota"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	if err := model.TransferQuotaToChild(c.GetInt(ctxkey.Id), childId, req.Quota); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func ReclaimQuotaFromChild(c *gin.Context) {
	childId, ok := childIdFromParam(c)
	if !ok {
		return
	}
	var req struct {
		Quota int64 `json:"quota"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	if err := model.ReclaimQuotaFromChild(c.GetInt(ctxkey.Id), childId, req.Quota); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

// ResetChildPassword 主账号为子账号重置密码（子账号密码丢了不必找管理员）。
func ResetChildPassword(c *gin.Context) {
	childId, ok := childIdFromParam(c)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	probe := model.User{Username: "x", Password: req.Password}
	if err := common.Validate.Struct(&probe); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "密码长度需为 8-20 位"})
		return
	}
	hashed, err := common.Password2Hash(req.Password)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := model.UpdateChildPassword(childId, hashed); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

// DeleteChildAccount 解绑子账号（软删除）。
// 不物理删除：历史消费日志仍引用该 user_id，硬删会让报表出现空洞。
func DeleteChildAccount(c *gin.Context) {
	childId, ok := childIdFromParam(c)
	if !ok {
		return
	}
	if err := model.DisableChildAccount(childId); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}
