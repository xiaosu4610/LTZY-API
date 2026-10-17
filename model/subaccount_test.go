package model

import (
	"strconv"
	"testing"
)

// setupChildTest 造一个主账号（有一定额度）和两个普通用户（其中一个作为子账号）。
func setupChildTest(t *testing.T) (parentId int, otherId int) {
	t.Helper()
	initTestDB(t)
	if err := DB.AutoMigrate(&User{}); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	mk := func(name string, quota int64) int {
		u := User{
			Username: name, Password: "x", DisplayName: name,
			Role: RoleCommonUser, Status: UserStatusEnabled, Quota: quota,
			AccessToken: name + "-tok", AffCode: name + "-aff",
		}
		if err := DB.Create(&u).Error; err != nil {
			t.Fatalf("建用户 %s 失败：%v", name, err)
		}
		return u.Id
	}
	return mk("parent-"+t.Name(), 100000), mk("other-"+t.Name(), 50000)
}

func quotaOf(t *testing.T, id int) int64 {
	t.Helper()
	q, err := GetUserQuota(id)
	if err != nil {
		t.Fatalf("取额度失败：%v", err)
	}
	return q
}

// TestAddChildAccountAndTransfer 建子账号并拨付额度：主减子增，总额守恒
func TestAddChildAccountAndTransfer(t *testing.T) {
	parentId, _ := setupChildTest(t)
	before := quotaOf(t, parentId)

	childId, err := AddChildAccount(parentId, "child"+strconv.Itoa(parentId), "pass12345", "子账号", 30000)
	if err != nil {
		t.Fatalf("建子账号失败：%v", err)
	}
	if got := quotaOf(t, childId); got != 30000 {
		t.Fatalf("子账号初始额度应为 30000，实际 %d", got)
	}
	if got := quotaOf(t, parentId); got != before-30000 {
		t.Fatalf("主账号应减少 30000，实际 %d", got)
	}
	// 总额守恒（额度是转账不是印钞）
	if quotaOf(t, parentId)+quotaOf(t, childId) != before {
		t.Fatal("拨付后总额应守恒")
	}

	// 归属关系正确
	ok, err := IsChildOf(childId, parentId)
	if err != nil || !ok {
		t.Fatalf("应识别为子账号：ok=%v err=%v", ok, err)
	}
	children, err := GetChildAccounts(parentId)
	if err != nil {
		t.Fatalf("查子账号失败：%v", err)
	}
	if len(children) != 1 || children[0].Id != childId {
		t.Fatalf("子账号列表不符：%v", children)
	}
}

// TestTransferToNonChildRejected 越权核心断言：不能给别人的账号转账
func TestTransferToNonChildRejected(t *testing.T) {
	parentId, otherId := setupChildTest(t)
	beforeOther := quotaOf(t, otherId)

	if err := TransferQuotaToChild(parentId, otherId, 1000); err == nil {
		t.Fatal("给非子账号转账应被拒绝")
	}
	if got := quotaOf(t, otherId); got != beforeOther {
		t.Fatalf("被拒绝的转账不应改变对方额度，实际 %d", got)
	}

	if err := ReclaimQuotaFromChild(parentId, otherId, 1000); err == nil {
		t.Fatal("从非子账号收回额度应被拒绝")
	}
}

// TestTransferInsufficientQuota 余额不足时必须整笔失败（不能出现主账号负数）
func TestTransferInsufficientQuota(t *testing.T) {
	parentId, _ := setupChildTest(t)
	childId, err := AddChildAccount(parentId, "c"+strconv.Itoa(parentId), "pass12345", "子", 0)
	if err != nil {
		t.Fatalf("建子账号失败：%v", err)
	}
	parentQuota := quotaOf(t, parentId)

	if err := TransferQuotaToChild(parentId, childId, parentQuota+1); err == nil {
		t.Fatal("超出余额的拨付应失败")
	}
	if got := quotaOf(t, parentId); got != parentQuota {
		t.Fatalf("失败后主账号额度不应变化，实际 %d", got)
	}
	if got := quotaOf(t, childId); got != 0 {
		t.Fatalf("失败后子账号额度不应变化，实际 %d", got)
	}
}

// TestReclaimQuota 收回额度：子减主增
func TestReclaimQuota(t *testing.T) {
	parentId, _ := setupChildTest(t)
	childId, err := AddChildAccount(parentId, "r"+strconv.Itoa(parentId), "pass12345", "子", 20000)
	if err != nil {
		t.Fatalf("建子账号失败：%v", err)
	}
	parentAfterGive := quotaOf(t, parentId)

	if err := ReclaimQuotaFromChild(parentId, childId, 5000); err != nil {
		t.Fatalf("收回失败：%v", err)
	}
	if got := quotaOf(t, childId); got != 15000 {
		t.Fatalf("子账号应剩 15000，实际 %d", got)
	}
	if got := quotaOf(t, parentId); got != parentAfterGive+5000 {
		t.Fatalf("主账号应增加 5000，实际 %d", got)
	}
}

// TestChildAccountLimit 子账号数量上限
func TestChildAccountLimit(t *testing.T) {
	parentId, _ := setupChildTest(t)
	for i := 0; i < MaxChildAccounts; i++ {
		if _, err := AddChildAccount(parentId, "lim"+strconv.Itoa(parentId)+strconv.Itoa(i), "pass12345", "子", 0); err != nil {
			t.Fatalf("第 %d 个子账号应创建成功：%v", i, err)
		}
	}
	if _, err := AddChildAccount(parentId, "overflow"+strconv.Itoa(parentId), "pass12345", "子", 0); err == nil {
		t.Fatalf("超过上限（%d）应被拒绝", MaxChildAccounts)
	}
}

// TestDisableChildAccount 解绑后不再是子账号，且用户名被释放
func TestDisableChildAccount(t *testing.T) {
	parentId, _ := setupChildTest(t)
	childId, err := AddChildAccount(parentId, "d"+strconv.Itoa(parentId), "pass12345", "子", 0)
	if err != nil {
		t.Fatalf("建子账号失败：%v", err)
	}
	if err := DisableChildAccount(childId); err != nil {
		t.Fatalf("解绑失败：%v", err)
	}
	ok, err := IsChildOf(childId, parentId)
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if ok {
		t.Fatal("解绑后不应再是子账号")
	}
	children, err := GetChildAccounts(parentId)
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if len(children) != 0 {
		t.Fatalf("解绑后列表应为空，实际 %d 个", len(children))
	}
}

// TestUpdateChildPassword 重置子账号密码
func TestUpdateChildPassword(t *testing.T) {
	parentId, _ := setupChildTest(t)
	childId, err := AddChildAccount(parentId, "p"+strconv.Itoa(parentId), "pass12345", "子", 0)
	if err != nil {
		t.Fatalf("建子账号失败：%v", err)
	}
	if err := UpdateChildPassword(childId, "hashed-value"); err != nil {
		t.Fatalf("重置密码失败：%v", err)
	}
	user, err := GetUserById(childId, true)
	if err != nil {
		t.Fatalf("取用户失败：%v", err)
	}
	if user.Password != "hashed-value" {
		t.Fatalf("密码未更新，实际 %q", user.Password)
	}
	if err := UpdateChildPassword(0, "x"); err == nil {
		t.Fatal("id 为 0 应报错")
	}
	if err := UpdateChildPassword(childId, ""); err == nil {
		t.Fatal("空密码应报错")
	}
}
