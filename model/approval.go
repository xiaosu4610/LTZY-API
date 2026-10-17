package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/xiaosu4610/LTZY-API/common/helper"
)

// 审批流（LTZY-API 新增，C7）。
//
// 目标：把「需要人工把关」的动作从"管理员直接执行"变成"申请 → 审批 → 执行"，
// 留下完整的申请理由与审批人记录。典型场景是充值/提额：
// 让运营发起、财务或老板确认，比直接改额度可追溯得多。
//
// 设计要点：
//   - **单一审批表 + 类型字段**，不为每种申请建一张表。审批的共性（谁申请、
//     要什么、批没批、谁批的）远多于差异，多表会让"待办列表"变成 union 查询。
//   - **审批通过才执行，且执行与状态流转在同一事务里**：否则会出现
//     "已批准但额度没加上"这种最难排查的状态。
//   - **幂等**：同一申请只能被审批一次；重复审批返回错误而不是重复执行
//     （重复执行 = 重复加额度，是直接的资损）。
//   - 申请人不能审批自己的申请（否则审批流形同虚设）。

const (
	ApprovalTypeTopUp = "topup" // 充值（管理员给用户加额度）
	// ApprovalTypeQuotaAdjust 提额（管理员调整用户额度上限，语义上等同充值但保留来源区分）
	ApprovalTypeQuotaAdjust = "quota_adjust"

	ApprovalStatusPending  = 1
	ApprovalStatusApproved = 2
	ApprovalStatusRejected = 3
)

// Approval 审批申请。
type Approval struct {
	Id           int    `json:"id" gorm:"primaryKey"`
	Type         string `json:"type" gorm:"type:varchar(32);index"`
	Status       int    `json:"status" gorm:"type:int;default:1;index"`
	TargetUserId int    `json:"target_user_id" gorm:"type:int;index"`
	// 申请涉及的额度（充值/提额）。0 表示不涉及额度。
	Quota int64 `json:"quota" gorm:"bigint;default:0"`
	// 申请理由，必填：没有理由的审批没有复核价值
	Reason       string `json:"reason" gorm:"type:varchar(500)"`
	ApplicantId  int    `json:"applicant_id" gorm:"type:int;index"`
	Applicant    string `json:"applicant" gorm:"type:varchar(64)"`
	ReviewerId   int    `json:"reviewer_id" gorm:"type:int;index"`
	Reviewer     string `json:"reviewer" gorm:"type:varchar(64)"`
	ReviewRemark string `json:"review_remark" gorm:"type:varchar(500)"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint;index"`
	// 审批时间（未审批为 0）
	ReviewedAt int64 `json:"reviewed_at" gorm:"bigint;default:0"`
}

func (a *Approval) TableName() string {
	return "approvals"
}

// CreateApproval 提交一条审批申请。
func CreateApproval(approval *Approval) error {
	if approval.Reason == "" {
		return errors.New("申请理由不能为空")
	}
	if approval.Type != ApprovalTypeTopUp && approval.Type != ApprovalTypeQuotaAdjust {
		return fmt.Errorf("未知的审批类型：%s", approval.Type)
	}
	if approval.TargetUserId == 0 {
		return errors.New("目标用户不能为空")
	}
	if approval.Quota <= 0 {
		return errors.New("申请额度必须大于 0")
	}
	if _, err := GetUserById(approval.TargetUserId, false); err != nil {
		return errors.New("目标用户不存在")
	}
	approval.Status = ApprovalStatusPending
	approval.CreatedAt = helper.GetTimestamp()
	return DB.Create(approval).Error
}

// GetApprovals 分页查询审批列表（status=0 表示全部）。
func GetApprovals(status int, startIdx int, num int) ([]*Approval, error) {
	var list []*Approval
	tx := DB.Model(&Approval{})
	if status != 0 {
		tx = tx.Where("status = ?", status)
	}
	err := tx.Order("id desc").Limit(num).Offset(startIdx).Find(&list).Error
	return list, err
}

// GetApprovalsByApplicant 某人提交的申请（普通用户看自己的进度）。
func GetApprovalsByApplicant(applicantId int, startIdx int, num int) ([]*Approval, error) {
	if applicantId == 0 {
		return nil, errors.New("申请人 id 为空")
	}
	var list []*Approval
	err := DB.Where("applicant_id = ?", applicantId).
		Order("id desc").Limit(num).Offset(startIdx).Find(&list).Error
	return list, err
}

// GetApprovalById 取单条审批。
func GetApprovalById(id int) (*Approval, error) {
	if id == 0 {
		return nil, errors.New("审批 id 为空")
	}
	a := Approval{Id: id}
	if err := DB.First(&a, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// ApproveApproval 审批通过并**在同一事务内执行**对应的额度变更。
//
// 为什么执行与状态流转必须同事务：否则一旦执行失败（或进程中断），
// 会留下"状态=已批准但额度没加"的记录，事后极难判断到底该不该补。
func ApproveApproval(id int, reviewerId int, reviewerName string, remark string) error {
	return reviewApproval(id, reviewerId, reviewerName, remark, true)
}

// RejectApproval 驳回申请（不执行任何额度变更）。
func RejectApproval(id int, reviewerId int, reviewerName string, remark string) error {
	return reviewApproval(id, reviewerId, reviewerName, remark, false)
}

func reviewApproval(id int, reviewerId int, reviewerName string, remark string, approve bool) error {
	approval, err := GetApprovalById(id)
	if err != nil {
		return errors.New("审批记录不存在")
	}
	if approval.Status != ApprovalStatusPending {
		// 幂等保护：重复审批会重复加额度，属于直接资损
		return errors.New("该申请已被处理，请勿重复审批")
	}
	if reviewerId == approval.ApplicantId {
		return errors.New("不能审批自己提交的申请")
	}

	targetStatus := ApprovalStatusRejected
	if approve {
		targetStatus = ApprovalStatusApproved
	}
	now := helper.GetTimestamp()

	return DB.Transaction(func(tx *gorm.DB) error {
		// 用带状态条件的 UPDATE 抢占这条记录：并发双击审批时只有一个能成功
		result := tx.Model(&Approval{}).
			Where("id = ? AND status = ?", id, ApprovalStatusPending).
			Updates(map[string]any{
				"status":        targetStatus,
				"reviewer_id":   reviewerId,
				"reviewer":      reviewerName,
				"review_remark": remark,
				"reviewed_at":   now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("该申请已被处理，请勿重复审批")
		}
		if !approve {
			return nil
		}
		// 执行额度变更
		if err := tx.Model(&User{}).Where("id = ?", approval.TargetUserId).
			Update("quota", gorm.Expr("quota + ?", approval.Quota)).Error; err != nil {
			return err
		}
		return nil
	})
}

// CountPendingApprovals 待审批数量（供管理端显示待办）。
func CountPendingApprovals() (int64, error) {
	var count int64
	err := DB.Model(&Approval{}).Where("status = ?", ApprovalStatusPending).Count(&count).Error
	return count, err
}
