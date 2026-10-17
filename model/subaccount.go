package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/random"
)

// 子账号（LTZY-API 新增，C6）。
//
// 场景：一个团队/个人有多个使用者（家人、同事、多个项目），主账号希望
// 「一个钱包、多把钥匙、各自限额」，而不是每人一个独立账号各自充值。
//
// 设计取舍（与既有机制的关系）：
//   - **不新建账号体系**，而是复用 users 表的 `inviter_id` 字段建立
//     「子账号 → 主账号」的从属关系。好处是：令牌、日志、预算、限流等
//     既有机制全部原样复用，子账号就是一个普通用户，只是额度由主账号拨付。
//   - 额度隔离依赖现有的「用户级」计数：子账号自己消费自己的额度；
//     主账号的额度通过 `TransferQuotaToChild` 拨付，拨付是**事务性转账**
//     （主减子增在同一次提交里），不会凭空产生额度。
//   - 不使用 `InviteQuota` 那套邀请返利逻辑：那是拉新奖励，与"家庭共享"语义不同，
//     混用会让两套规则互相干扰。
//
// 安全约束：
//   - 只能创建/管理**自己名下**的子账号（由 inviter_id 判定），
//   - 子账号不能再创建子账号（避免无限层级与额度套娃），
//   - 拨付额度不得超过主账号可用额度。

// MaxChildAccounts 单个主账号可拥有的子账号上限。
// 不设上限会让一个账号被用来批量注册，进而绕过注册限制。
const MaxChildAccounts = 20

// ErrNotChildAccount 表示目标用户不是调用者的子账号。
var ErrNotChildAccount = errors.New("该用户不是你的子账号")

// GetChildAccounts 返回某主账号名下的全部子账号。
func GetChildAccounts(parentId int) ([]*User, error) {
	var children []*User
	err := DB.Omit("password", "access_token").
		Where("inviter_id = ? AND status != ?", parentId, UserStatusDeleted).
		Order("id asc").Find(&children).Error
	return children, err
}

// CountChildAccounts 统计子账号数量（用于上限校验）。
func CountChildAccounts(parentId int) (int64, error) {
	var count int64
	err := DB.Model(&User{}).
		Where("inviter_id = ? AND status != ?", parentId, UserStatusDeleted).
		Count(&count).Error
	return count, err
}

// IsChildOf 判断 childId 是否是 parentId 的子账号。
// 所有子账号操作都必须先过这一关 —— 否则任何一个用户都能改别人的额度。
func IsChildOf(childId int, parentId int) (bool, error) {
	if childId == 0 || parentId == 0 {
		return false, errors.New("用户 id 为空")
	}
	var count int64
	err := DB.Model(&User{}).
		Where("id = ? AND inviter_id = ?", childId, parentId).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// AddChildAccount 由主账号创建一个子账号，并把它绑定到主账号名下。
// 返回新建子账号的 id。
func AddChildAccount(parentId int, username string, password string, displayName string, initQuota int64) (int, error) {
	if username == "" || password == "" {
		return 0, errors.New("用户名与密码不能为空")
	}
	count, err := CountChildAccounts(parentId)
	if err != nil {
		return 0, err
	}
	if count >= MaxChildAccounts {
		return 0, fmt.Errorf("子账号数量已达上限（%d 个）", MaxChildAccounts)
	}
	if initQuota < 0 {
		return 0, errors.New("初始额度不能为负数")
	}

	child := &User{
		Username:    username,
		Password:    password,
		DisplayName: displayName,
		// 子账号一律是普通用户：允许子账号是管理员等于绕过了角色管控
		Role: RoleCommonUser,
		// 子账号跟随主账号的租户归属（C5），否则会变成跨租户的孤儿数据
		TenantId: 0,
	}
	parent, err := GetUserById(parentId, false)
	if err != nil {
		return 0, err
	}
	child.TenantId = parent.TenantId
	if child.DisplayName == "" {
		child.DisplayName = username
	}

	// 新用户注册赠送额度对子账号不适用（它不是"新用户注册"），
	// 因此走自己的插入逻辑而不是 User.Insert。
	child.Password, err = common.Password2Hash(password)
	if err != nil {
		return 0, err
	}
	child.AccessToken = random.GetUUID()
	child.AffCode = random.GetRandomString(4)
	child.Status = UserStatusEnabled
	child.Group = parent.Group
	child.InviterId = parentId
	child.Quota = 0

	// 建号与首次拨付放在同一事务：否则可能建出账号却没额度，
	// 用户看到"创建成功但余额为 0"，还得再来一次。
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(child).Error; err != nil {
			return err
		}
		if initQuota > 0 {
			return transferQuotaTx(tx, parentId, child.Id, initQuota)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return child.Id, nil
}

// TransferQuotaToChild 主账号向子账号拨付额度（事务性转账）。
func TransferQuotaToChild(parentId int, childId int, quota int64) error {
	if quota <= 0 {
		return errors.New("拨付额度必须大于 0")
	}
	ok, err := IsChildOf(childId, parentId)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotChildAccount
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		return transferQuotaTx(tx, parentId, childId, quota)
	})
	if err != nil {
		return err
	}
	// 额度是直接读库的（没有用户级额度缓存），因此无需额外失效；
	// 这里多写一行注释是为了避免后来者误以为有缓存、又在别处补错逻辑。
	return nil
}

// ReclaimQuotaFromChild 从子账号收回额度（同样是事务性转账）。
func ReclaimQuotaFromChild(parentId int, childId int, quota int64) error {
	if quota <= 0 {
		return errors.New("收回额度必须大于 0")
	}
	ok, err := IsChildOf(childId, parentId)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotChildAccount
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		return transferQuotaTx(tx, childId, parentId, quota)
	})
	if err != nil {
		return err
	}
	return nil
}

// UpdateChildPassword 设置子账号的新密码（调用方负责传入哈希后的密码）。
func UpdateChildPassword(childId int, hashedPassword string) error {
	if childId == 0 {
		return errors.New("用户 id 为空")
	}
	if hashedPassword == "" {
		return errors.New("密码不能为空")
	}
	return DB.Model(&User{}).Where("id = ?", childId).Update("password", hashedPassword).Error
}

// DisableChildAccount 解绑子账号：标记为已删除并解除从属关系。
//
// 不物理删除：历史消费日志仍引用该 user_id，硬删会让报表出现空洞；
// 同时把用户名改名，让原用户名可以被重新使用（否则解绑后会永久占用）。
func DisableChildAccount(childId int) error {
	if childId == 0 {
		return errors.New("用户 id 为空")
	}
	return DB.Model(&User{}).Where("id = ?", childId).Updates(map[string]any{
		"status":     UserStatusDeleted,
		"username":   fmt.Sprintf("deleted_child_%s", random.GetUUID()[:8]),
		"inviter_id": 0,
	}).Error
}

// transferQuotaTx 在给定事务内把额度从 from 转到 to。
//
// 用「带条件的 UPDATE」而不是「先查再改」：后者在并发拨付时会丢更新，
// 而且无法在 SQL 层保证「转出方余额足够」。这里把余额校验直接写进
// WHERE 条件，影响行数为 0 就说明余额不足。
func transferQuotaTx(tx *gorm.DB, from int, to int, quota int64) error {
	if quota <= 0 {
		return errors.New("转账额度必须大于 0")
	}
	result := tx.Model(&User{}).
		Where("id = ? AND quota >= ?", from, quota).
		Update("quota", gorm.Expr("quota - ?", quota))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("主账号可用额度不足")
	}
	if err := tx.Model(&User{}).Where("id = ?", to).
		Update("quota", gorm.Expr("quota + ?", quota)).Error; err != nil {
		return err
	}
	return nil
}
