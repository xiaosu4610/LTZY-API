package model

import (
	"strconv"
	"testing"
)

func itoa(i int) string { return strconv.Itoa(i) }

// setupTenantTest 建好租户相关的表，并造两个租户的数据用于隔离断言。
func setupTenantTest(t *testing.T) (tenantA int, tenantB int, userA int, userB int, platformUser int) {
	t.Helper()
	initTestDB(t)
	if err := DB.AutoMigrate(&Tenant{}, &User{}, &Log{}); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}

	ta := Tenant{Name: "tenant-a-" + t.Name()}
	tb := Tenant{Name: "tenant-b-" + t.Name()}
	if err := ta.Insert(); err != nil {
		t.Fatalf("建租户 A 失败：%v", err)
	}
	if err := tb.Insert(); err != nil {
		t.Fatalf("建租户 B 失败：%v", err)
	}

	// 直接写 users 表：不经过 Insert，避免触发赠送额度/默认令牌等副作用。
	// access_token 与 aff_code 有唯一索引，必须给出互不相同的值，否则第二个用户就会撞索引。
	seq := 0
	mk := func(name string, tenantId int) int {
		seq++
		u := User{
			Username: name, Password: "x", DisplayName: name,
			Role: RoleCommonUser, Status: UserStatusEnabled, TenantId: tenantId,
			AccessToken: name + "-token-" + itoa(seq),
			AffCode:     name + "-aff-" + itoa(seq),
		}
		if err := DB.Create(&u).Error; err != nil {
			t.Fatalf("建用户 %s 失败：%v", name, err)
		}
		return u.Id
	}
	return ta.Id, tb.Id,
		mk("ua-"+t.Name(), ta.Id),
		mk("ub-"+t.Name(), tb.Id),
		mk("up-"+t.Name(), 0)
}

// TestTenantUserIsolation 用户列表必须按租户隔离（C5 的核心断言）
func TestTenantUserIsolation(t *testing.T) {
	tenantA, _, userA, userB, platformUser := setupTenantTest(t)

	// 租户 A 视角：只能看到自己租户的用户
	usersA, err := GetAllUsers(0, 100, "id", tenantA)
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if !containsUserId(usersA, userA) {
		t.Fatal("租户 A 应能看到自己的用户")
	}
	for _, u := range usersA {
		if u.Id == userB {
			t.Fatal("租户 A 不应看到租户 B 的用户（跨租户泄漏）")
		}
		if u.Id == platformUser {
			t.Fatal("租户 A 不应看到平台用户")
		}
	}

	// 平台视角（tenantId=0）：应能看到全部
	all, err := GetAllUsers(0, 100, "id", 0)
	if err != nil {
		t.Fatalf("平台查询失败：%v", err)
	}
	if !containsUserId(all, userA) || !containsUserId(all, userB) || !containsUserId(all, platformUser) {
		t.Fatal("平台视角应能看到所有租户的用户")
	}
}

// TestTenantSearchIsolation 搜索同样不能跨租户
func TestTenantSearchIsolation(t *testing.T) {
	tenantA, _, userA, userB, _ := setupTenantTest(t)

	got, err := SearchUsers("u", tenantA)
	if err != nil {
		t.Fatalf("搜索失败：%v", err)
	}
	if !containsUserId(got, userA) {
		t.Fatal("租户 A 的搜索应命中自己的用户")
	}
	if containsUserId(got, userB) {
		t.Fatal("租户 A 的搜索不应命中租户 B 的用户")
	}
}

// TestTenantLogScope 日志范围过滤：含"租户无用户"这个最危险的边界
func TestTenantLogScope(t *testing.T) {
	tenantA, tenantB, userA, _, _ := setupTenantTest(t)

	for _, tc := range []struct {
		name     string
		tenantId int
		wantRows bool
	}{
		{"平台视角不过滤", 0, true},
		{"租户 A 能查到自己的日志", tenantA, true},
		{"租户 B 查不到 A 的日志", tenantB, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 造一条属于租户 A 用户的消费日志
			if err := LOG_DB.Create(&Log{UserId: userA, Type: LogTypeConsume, ModelName: "m", Quota: 10}).Error; err != nil {
				t.Fatalf("写日志失败：%v", err)
			}
			logs, err := GetAllLogs(LogTypeConsume, 0, 0, "", "", "", 0, 100, 0, tc.tenantId)
			if err != nil {
				t.Fatalf("查询日志失败：%v", err)
			}
			found := false
			for _, l := range logs {
				if l.UserId == userA {
					found = true
				}
			}
			if found != tc.wantRows {
				t.Fatalf("期望看到租户 A 的日志=%v，实际=%v", tc.wantRows, found)
			}
		})
	}
}

// TestTenantLogScopeEmptyTenant 空租户必须查不到任何日志（不能退化成"不过滤"）
func TestTenantLogScopeEmptyTenant(t *testing.T) {
	tenantA, _, userA, _, _ := setupTenantTest(t)
	if err := LOG_DB.Create(&Log{UserId: userA, Type: LogTypeConsume, ModelName: "m"}).Error; err != nil {
		t.Fatalf("写日志失败：%v", err)
	}
	empty := Tenant{Name: "empty-" + t.Name()}
	if err := empty.Insert(); err != nil {
		t.Fatalf("建空租户失败：%v", err)
	}

	// 空租户的 tenantId
	logs, err := GetAllLogs(LogTypeConsume, 0, 0, "", "", "", 0, 100, 0, empty.Id)
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("空租户不应查到任何日志，实际 %d 条（退化成不过滤就是跨租户泄漏）", len(logs))
	}
	_ = tenantA
}

// TestTenantDeleteWithUsers 有用户归属时不允许删租户（否则用户会变成孤儿）
func TestTenantDeleteWithUsers(t *testing.T) {
	tenantA, _, _, _, _ := setupTenantTest(t)

	tenant, err := GetTenantById(tenantA)
	if err != nil {
		t.Fatalf("取租户失败：%v", err)
	}
	if err := tenant.Delete(); err == nil {
		t.Fatal("租户下仍有用户时删除应失败")
	}
}

// TestUserTenantIdLookup 鉴权路径用的租户查询
func TestUserTenantIdLookup(t *testing.T) {
	tenantA, _, userA, _, platformUser := setupTenantTest(t)

	got, err := GetUserTenantId(userA)
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if got != tenantA {
		t.Fatalf("租户 A 的用户应返回 %d，实际 %d", tenantA, got)
	}
	got, err = GetUserTenantId(platformUser)
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if got != 0 {
		t.Fatalf("平台用户应返回 0，实际 %d", got)
	}
	if _, err := GetUserTenantId(0); err == nil {
		t.Fatal("id 为 0 应报错")
	}
}

// TestTenantEnabled 租户停用后不可用
func TestTenantEnabled(t *testing.T) {
	tenantA, _, _, _, _ := setupTenantTest(t)
	if !IsTenantEnabled(tenantA) {
		t.Fatal("新建租户应为可用")
	}
	if !IsTenantEnabled(0) {
		t.Fatal("平台（0）应始终可用")
	}
	if IsTenantEnabled(999999) {
		t.Fatal("不存在的租户应视为不可用")
	}
	tenant, _ := GetTenantById(tenantA)
	tenant.Status = TenantStatusDisabled
	if err := tenant.Update(); err != nil {
		t.Fatalf("更新租户失败：%v", err)
	}
	if IsTenantEnabled(tenantA) {
		t.Fatal("停用的租户应不可用")
	}
}

func containsUserId(users []*User, id int) bool {
	for _, u := range users {
		if u.Id == id {
			return true
		}
	}
	return false
}
