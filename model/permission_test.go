package model

import (
	"testing"

	"github.com/xiaosu4610/LTZY-API/common/config"
)

func setRolePermissionsOption(t *testing.T, value string) {
	t.Helper()
	config.OptionMapRWMutex.Lock()
	if config.OptionMap == nil {
		config.OptionMap = make(map[string]string)
	}
	config.OptionMap["RolePermissions"] = value
	config.OptionMapRWMutex.Unlock()
}

// TestDefaultRolePermissionsMatchLegacy 默认配置必须与改造前的硬编码行为一致（C4 的核心安全网）
func TestDefaultRolePermissionsMatchLegacy(t *testing.T) {
	setRolePermissionsOption(t, "")
	defer setRolePermissionsOption(t, "")

	// admin 能做的事 = 改造前所有 AdminAuth 网住的接口
	for _, perm := range []string{
		PermChannelRead, PermChannelWrite, PermChannelTest,
		PermUserRead, PermUserWrite,
		PermRedemptionRead, PermRedemptionWrite,
		PermLogRead, PermAuditRead, PermGroupRead,
	} {
		if !HasPermission(RoleAdminUser, perm) {
			t.Fatalf("默认配置下 admin 应拥有 %s（改造前 AdminAuth 允许）", perm)
		}
	}
	// 改造前 option 相关是 RootAuth，admin 进不去 —— 默认值必须保持
	if HasPermission(RoleAdminUser, PermOptionWrite) {
		t.Fatal("默认配置下 admin 不应拥有 option.write（改造前那是 RootAuth）")
	}
	// 普通用户与游客没有任何管理权限
	for _, perm := range AllPermissions() {
		if HasPermission(RoleCommonUser, perm) {
			t.Fatalf("普通用户不应拥有 %s", perm)
		}
		if HasPermission(RoleGuestUser, perm) {
			t.Fatalf("游客不应拥有 %s", perm)
		}
	}
}

// TestRootAlwaysHasAllPermissions root 必须恒有全部权限（防止配置失误把系统锁死）
func TestRootAlwaysHasAllPermissions(t *testing.T) {
	// 故意把 root 配成空权限
	setRolePermissionsOption(t, `{"root":[]}`)
	defer setRolePermissionsOption(t, "")

	for _, perm := range AllPermissions() {
		if !HasPermission(RoleRootUser, perm) {
			t.Fatalf("即使配置把 root 清空，root 也应拥有 %s", perm)
		}
	}
	// 比 root 更高的角色同样恒真
	if !HasPermission(RoleRootUser+1, PermOptionWrite) {
		t.Fatal("高于 root 的角色也应恒有全部权限")
	}
}

// TestCustomRole 自定义角色：给一个未占用的数值配置权限即可生效
func TestCustomRole(t *testing.T) {
	setRolePermissionsOption(t, `{"20":["channel.read","log.read"]}`)
	defer setRolePermissionsOption(t, "")

	if !HasPermission(20, PermChannelRead) {
		t.Fatal("自定义角色应拥有已配置的 channel.read")
	}
	if !HasPermission(20, PermLogRead) {
		t.Fatal("自定义角色应拥有已配置的 log.read")
	}
	if HasPermission(20, PermChannelWrite) {
		t.Fatal("未配置的权限点不应被授予")
	}
	// 未配置的自定义角色 = 无权限
	if HasPermission(21, PermChannelRead) {
		t.Fatal("未配置的自定义角色不应拥有任何权限")
	}
	// 自定义角色不影响内置角色的默认值
	if !HasPermission(RoleAdminUser, PermChannelWrite) {
		t.Fatal("自定义角色不应影响 admin 的默认权限")
	}
}

// TestRolePermissionsByName 支持用角色名覆盖内置角色
func TestRolePermissionsByName(t *testing.T) {
	setRolePermissionsOption(t, `{"admin":["channel.read"]}`)
	defer setRolePermissionsOption(t, "")

	if !HasPermission(RoleAdminUser, PermChannelRead) {
		t.Fatal("按角色名配置应生效")
	}
	if HasPermission(RoleAdminUser, PermChannelWrite) {
		t.Fatal("被显式移除的权限不应再生效（这正是 RBAC 的用途）")
	}
}

// TestInvalidOptionFallsBackToDefault 配置写坏时回退默认值，而不是把全员权限清空
func TestInvalidOptionFallsBackToDefault(t *testing.T) {
	setRolePermissionsOption(t, "not-a-json")
	defer setRolePermissionsOption(t, "")

	if !HasPermission(RoleAdminUser, PermChannelRead) {
		t.Fatal("配置损坏时应回退内置默认值，而不是清空权限")
	}
}

// TestValidateRolePermissionsOption 校验：未知权限点必须被拦下
func TestValidateRolePermissionsOption(t *testing.T) {
	if err := ValidateRolePermissionsOption(""); err != nil {
		t.Fatalf("空配置应合法：%v", err)
	}
	if err := ValidateRolePermissionsOption(`{"20":["channel.read","*"]}`); err != nil {
		t.Fatalf("合法配置应通过：%v", err)
	}
	if err := ValidateRolePermissionsOption("not-a-json"); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	if err := ValidateRolePermissionsOption(`{"20":["channel.red"]}`); err == nil {
		t.Fatal("拼错的权限点应被拦下（否则会表现为「配了却不生效」）")
	}
	if err := ValidateRolePermissionsOption(`{"":["channel.read"]}`); err == nil {
		t.Fatal("空角色名应报错")
	}
}
