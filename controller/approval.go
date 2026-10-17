package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 审批流接口（LTZY-API 新增，C7）。
//
// 权限划分：
//   - 提交申请：任何登录用户都能提交（提额请求本来就得由用户发起）；
//   - 查看列表与审批：管理能力，走 AdminAuth（在 router 层限制）。

func CreateApproval(c *gin.Context) {
	var req struct {
		Type         string `json:"type"`
		TargetUserId int    `json:"target_user_id"`
		Quota        int64  `json:"quota"`
		Reason       string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	// 普通用户只能为自己申请：否则任何人都能替别人要额度
	callerId := c.GetInt(ctxkey.Id)
	role := c.GetInt(ctxkey.Role)
	if role < model.RoleAdminUser {
		req.TargetUserId = callerId
	}
	if req.TargetUserId == 0 {
		req.TargetUserId = callerId
	}
	approval := &model.Approval{
		Type:         req.Type,
		TargetUserId: req.TargetUserId,
		Quota:        req.Quota,
		Reason:       req.Reason,
		ApplicantId:  callerId,
		Applicant:    c.GetString(ctxkey.Username),
	}
	if err := model.CreateApproval(approval); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": approval.Id})
}

func GetApprovals(c *gin.Context) {
	p, _ := strconv.Atoi(c.Query("p"))
	if p < 0 {
		p = 0
	}
	status, _ := strconv.Atoi(c.Query("status"))
	list, err := model.GetApprovals(status, p*config.ItemsPerPage, config.ItemsPerPage)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if list == nil {
		list = []*model.Approval{}
	}
	pending, _ := model.CountPendingApprovals()
	c.JSON(http.StatusOK, gin.H{
		"success": true, "message": "",
		"data": gin.H{"items": list, "pending": pending},
	})
}

// GetMyApprovals 我提交的申请（普通用户也能看到自己的进度）。
func GetMyApprovals(c *gin.Context) {
	p, _ := strconv.Atoi(c.Query("p"))
	if p < 0 {
		p = 0
	}
	list, err := model.GetApprovalsByApplicant(c.GetInt(ctxkey.Id), p*config.ItemsPerPage, config.ItemsPerPage)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if list == nil {
		list = []*model.Approval{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": list})
}

func reviewApprovalHandler(c *gin.Context, approve bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "id 格式错误"})
		return
	}
	var req struct {
		Remark string `json:"remark"`
	}
	_ = c.ShouldBindJSON(&req)

	reviewerId := c.GetInt(ctxkey.Id)
	reviewerName := c.GetString(ctxkey.Username)
	if approve {
		err = model.ApproveApproval(id, reviewerId, reviewerName, req.Remark)
	} else {
		err = model.RejectApproval(id, reviewerId, reviewerName, req.Remark)
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func ApproveApproval(c *gin.Context) {
	reviewApprovalHandler(c, true)
}

func RejectApproval(c *gin.Context) {
	reviewApprovalHandler(c, false)
}
