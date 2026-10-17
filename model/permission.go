package model

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/xiaosu4610/LTZY-API/common/config"
)

// 基于权限点的 RBAC（LTZY-API 新增，C4）。
//
// 背景：One API 的权限是「4 个硬编码角色 + 数值比较」（0 游客 / 1 普通 / 10 管理 / 100 根），
// 想让「某个管理员只能看渠道不能改渠道」或者「新建一个只读角色」都做不到。
//
// 设计：
//   - 引入**权限点**（如 channel.read / channel.write），角色 → 权限点集合可配置；
//   - 角色仍用 `User.Role` 这个 int 表达（不改表结构），因此**可以自定义新角色**：
//     给用户设一个未占用的数值（如 20），再在 RolePermissions 里配置这个角色的权限即可；
//   - 未配置时使用**与改造前完全一致**的内置默认值，保证既有部署行为不变；
//   - root 角色恒定拥有全部权限（硬编码在 HasPermission 里）——
//     否则管理员一个配置失误就会把自己彻底锁在系统外面，这是不可接受的。

// 权限点常量。命名遵循 resource.action，便于前端做分组展示。
const (
	PermAll = "*"

	PermChannelRead   = "channel.read"
	PermChannelWrite  = "channel.write"
	PermChannelTest   = "channel.test"
	PermUserRead      = "user.read"
	PermUserWrite     = "user.write"
	PermRedemptionRead  = "redemption.read"
	PermRedemptionWrite = "redemption.write"
	PermLogRead       = "log.read"
	PermAuditRead     = "audit.read"
	PermGroupRead     = "group.read"
	PermOptionRead    = "option.read"
	PermOptionWrite   = "option.write"
)

// AllPermissions 全部权限点（供后台展示与配置校验使用）。
func AllPermissions() []string {
	perms := []string{
		PermChannelRead, PermChannelWrite, PermChannelTest,
		PermUserRead, PermUserWrite,
		PermRedemptionRead, PermRedemptionWrite,
		PermLogRead, PermAuditRead, PermGroupRead,
		PermOptionRead, PermOptionWrite,
	}
	sort.Strings(perms)
	return perms
}

// builtinRoleNames 内置角色数值 → 名称（配置里两种键都支持，便于可读性）。
var builtinRoleNames = map[int]string{
	RoleGuestUser:  "guest",
	RoleCommonUser: "common",
	RoleAdminUser:  "admin",
	RoleRootUser:   "root",
}

// defaultRolePermissions 内置默认值，**必须与改造前的硬编码行为一致**：
//   - admin 能做的事 = 改造前所有 AdminAuth 网关住的接口；
//   - option.read/option.write 不授予 admin（改造前那是 RootAuth，管理员本来就进不去）；
//   - common/guest 不授予任何管理权限（改造前它们访问管理接口一律 403）。
func defaultRolePermissions() map[string][]string {
	return map[string][]string{
		"guest":  {},
		"common": {},
		"admin": {
			PermChannelRead, PermChannelWrite, PermChannelTest,
			PermUserRead, PermUserWrite,
			PermRedemptionRead, PermRedemptionWrite,
			PermLogRead, PermAuditRead, PermGroupRead,
		},
		"root": {PermAll},
	}
}

// RolePermissionsOption 读取管理员配置的角色权限表；未配置时返回内置默认值。
func RolePermissionsOption() map[string][]string {
	config.OptionMapRWMutex.RLock()
	raw := config.OptionMap["RolePermissions"]
	config.OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return defaultRolePermissions()
	}
	parsed := map[string][]string{}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		// 配置写坏时回退默认值，而不是把所有权限清空（那会让全员瞬间失去管理能力）
		return defaultRolePermissions()
	}
	return parsed
}

// GetRolePermissions 取某角色的权限点集合。
// 查找顺序：配置里的角色名 → 配置里的角色数值字符串 → 内置默认值 → 空。
func GetRolePermissions(role int) []string {
	table := RolePermissionsOption()
	if name, ok := builtinRoleNames[role]; ok {
		if perms, hit := table[name]; hit {
			return perms
		}
	}
	if perms, hit := table[fmt.Sprintf("%d", role)]; hit {
		return perms
	}
	// 自定义角色未配置 → 无权限（宁可少给，也不要凭空给权限）
	if _, isBuiltin := builtinRoleNames[role]; !isBuiltin {
		return nil
	}
	return defaultRolePermissions()[builtinRoleNames[role]]
}

// HasPermission 判断角色是否拥有某权限点。
// root 恒为 true：这是一条硬性安全底线，不受配置影响。
func HasPermission(role int, permission string) bool {
	if role >= RoleRootUser {
		return true
	}
	for _, p := range GetRolePermissions(role) {
		if p == PermAll || p == permission {
			return true
		}
	}
	return false
}

// ValidateRolePermissionsOption 校验管理员填写的角色权限配置。
// 会拒绝未知权限点（拼错一个词就会被拦下，否则会表现为"配了却不生效"的诡异现象）。
func ValidateRolePermissionsOption(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed := map[string][]string{}
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return err
	}
	known := map[string]bool{PermAll: true}
	for _, p := range AllPermissions() {
		known[p] = true
	}
	for roleKey, perms := range parsed {
		if strings.TrimSpace(roleKey) == "" {
			return fmt.Errorf("角色名不能为空")
		}
		for _, p := range perms {
			if !known[p] {
				return fmt.Errorf("角色 %s 配置了未知权限点：%s（可选值：%s）",
					roleKey, p, strings.Join(append(AllPermissions(), PermAll), ","))
			}
		}
	}
	// root 恒有全部权限，若配置里显式把它收紧，提示但不阻断（由 HasPermission 保证兜底）
	return nil
}
