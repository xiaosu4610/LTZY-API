package model

import (
	"testing"
)

// setupApprovalTest 造两个用户：申请人（普通用户）与目标用户。
func setupApprovalTest(t *testing.T) (applicantId int, targetId int) {
	t.Helper()
	initTestDB(t)
	if err := DB.AutoMigrate(&User{}, &Approval{}); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	mk := func(name string, quota int64) int {
		u := User{
			Username: name, Password: "x", DisplayName: name,
			Role: RoleCommonUser, Status: UserStatusEnabled, Quota: quota,
			AccessToken: name + "-tok", AffCode: name + "-aff",
		}
		if err := DB.Create(&u).Error; err != nil {
			t.Fatalf("建用户失败：%v", err)
		}
		return u.Id
	}
	return mk("app-"+t.Name(), 0), mk("tgt-"+t.Name(), 1000)
}

func newTopUpApproval(t *testing.T, applicantId int, targetId int, quota int64) *Approval {
	t.Helper()
	a := &Approval{
		Type:         ApprovalTypeTopUp,
		TargetUserId: targetId,
		Quota:        quota,
		Reason:       "客户已付款",
		ApplicantId:  applicantId,
		Applicant:    "app",
	}
	if err := CreateApproval(a); err != nil {
		t.Fatalf("提交申请失败：%v", err)
	}
	return a
}

// TestCreateApprovalValidation 提交时的字段校验
func TestCreateApprovalValidation(t *testing.T) {
	applicantId, targetId := setupApprovalTest(t)

	bad := []*Approval{
		{Type: ApprovalTypeTopUp, TargetUserId: targetId, Quota: 100, Reason: "", ApplicantId: applicantId},
		{Type: "unknown", TargetUserId: targetId, Quota: 100, Reason: "x", ApplicantId: applicantId},
		{Type: ApprovalTypeTopUp, TargetUserId: 0, Quota: 100, Reason: "x", ApplicantId: applicantId},
		{Type: ApprovalTypeTopUp, TargetUserId: targetId, Quota: 0, Reason: "x", ApplicantId: applicantId},
		{Type: ApprovalTypeTopUp, TargetUserId: 999999, Quota: 100, Reason: "x", ApplicantId: applicantId},
	}
	for i, a := range bad {
		if err := CreateApproval(a); err == nil {
			t.Fatalf("第 %d 条非法申请应被拒绝", i)
		}
	}
}

// TestApproveAddsQuota 审批通过后额度真的加上了
func TestApproveAddsQuota(t *testing.T) {
	applicantId, targetId := setupApprovalTest(t)
	before := quotaOf(t, targetId)
	a := newTopUpApproval(t, applicantId, targetId, 5000)

	// 用一个不同于申请人的审批人
	if err := ApproveApproval(a.Id, 999, "root", "同意"); err != nil {
		t.Fatalf("审批失败：%v", err)
	}
	if got := quotaOf(t, targetId); got != before+5000 {
		t.Fatalf("审批通过后应加 5000，实际 %d", got)
	}
	got, err := GetApprovalById(a.Id)
	if err != nil {
		t.Fatalf("取审批失败：%v", err)
	}
	if got.Status != ApprovalStatusApproved || got.ReviewerId != 999 || got.ReviewedAt == 0 {
		t.Fatalf("审批状态/审批人/时间未正确落库：%+v", got)
	}
}

// TestRejectDoesNotAddQuota 驳回不加额度
func TestRejectDoesNotAddQuota(t *testing.T) {
	applicantId, targetId := setupApprovalTest(t)
	before := quotaOf(t, targetId)
	a := newTopUpApproval(t, applicantId, targetId, 5000)

	if err := RejectApproval(a.Id, 999, "root", "金额不符"); err != nil {
		t.Fatalf("驳回失败：%v", err)
	}
	if got := quotaOf(t, targetId); got != before {
		t.Fatalf("驳回不应改变额度，实际 %d", got)
	}
	got, _ := GetApprovalById(a.Id)
	if got.Status != ApprovalStatusRejected {
		t.Fatalf("状态应为已驳回，实际 %d", got.Status)
	}
}

// TestDoubleApproveIsIdempotent 重复审批必须失败（否则就是重复加额度的资损）
func TestDoubleApproveIsIdempotent(t *testing.T) {
	applicantId, targetId := setupApprovalTest(t)
	a := newTopUpApproval(t, applicantId, targetId, 5000)

	if err := ApproveApproval(a.Id, 999, "root", ""); err != nil {
		t.Fatalf("首次审批应成功：%v", err)
	}
	afterFirst := quotaOf(t, targetId)

	if err := ApproveApproval(a.Id, 999, "root", ""); err == nil {
		t.Fatal("重复审批应被拒绝")
	}
	if got := quotaOf(t, targetId); got != afterFirst {
		t.Fatalf("重复审批不应再次加额度，实际 %d（首次后应为 %d）", got, afterFirst)
	}
	// 先驳回再通过同样不行
	if err := RejectApproval(a.Id, 998, "root2", ""); err == nil {
		t.Fatal("已批准的申请不能再驳回")
	}
}

// TestCannotApproveOwnRequest 不能审批自己的申请
func TestCannotApproveOwnRequest(t *testing.T) {
	applicantId, targetId := setupApprovalTest(t)
	a := newTopUpApproval(t, applicantId, targetId, 5000)

	if err := ApproveApproval(a.Id, applicantId, "app", ""); err == nil {
		t.Fatal("审批自己的申请应被拒绝")
	}
	if got, _ := GetApprovalById(a.Id); got.Status != ApprovalStatusPending {
		t.Fatalf("被拒后状态应仍为待审批，实际 %d", got.Status)
	}
}

// TestApprovalListing 列表与待办计数
func TestApprovalListing(t *testing.T) {
	applicantId, targetId := setupApprovalTest(t)
	a1 := newTopUpApproval(t, applicantId, targetId, 1000)
	_ = newTopUpApproval(t, applicantId, targetId, 2000)

	all, err := GetApprovals(0, 0, 10)
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if len(all) != 2 {
		t.Fatalf("应有 2 条，实际 %d", len(all))
	}
	pending, err := CountPendingApprovals()
	if err != nil {
		t.Fatalf("计数失败：%v", err)
	}
	if pending != 2 {
		t.Fatalf("待审批应为 2，实际 %d", pending)
	}

	if err := ApproveApproval(a1.Id, 999, "root", ""); err != nil {
		t.Fatalf("审批失败：%v", err)
	}
	pending, _ = CountPendingApprovals()
	if pending != 1 {
		t.Fatalf("审批一条后待办应为 1，实际 %d", pending)
	}
	approved, err := GetApprovals(ApprovalStatusApproved, 0, 10)
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if len(approved) != 1 {
		t.Fatalf("已批准应为 1 条，实际 %d", len(approved))
	}

	mine, err := GetApprovalsByApplicant(applicantId, 0, 10)
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if len(mine) != 2 {
		t.Fatalf("我提交的应有 2 条，实际 %d", len(mine))
	}
}

// TestApprovalNotFound 不存在的 id
func TestApprovalNotFound(t *testing.T) {
	setupApprovalTest(t)
	if err := ApproveApproval(999999, 1, "root", ""); err == nil {
		t.Fatal("不存在的审批应报错")
	}
	if _, err := GetApprovalById(0); err == nil {
		t.Fatal("id 为 0 应报错")
	}
}
