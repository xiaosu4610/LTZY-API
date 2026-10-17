package model

import (
	"errors"

	"gorm.io/gorm"

	"github.com/xiaosu4610/LTZY-API/common/helper"
)

// 多租户（LTZY-API 新增，C5）。
//
// 目标：一套部署服务多个互相隔离的"租户"（团队/客户/业务线）。
//
// ⚠️ 隔离范围（有意划定，不是遗漏）：
//   - **租户隔离**：用户、令牌、日志 —— 这三类承载租户的业务数据；
//   - **平台共享**：渠道、系统设置、兑换码 —— 一次部署通常只有一套上游渠道，
//     各租户共用；兑换码由平台统一发放。把它们也隔离会让运维变成噩梦
//     （每个租户都要配一遍渠道），且与"多租户共用网关"的实际业务不符。
//
// 实现方式：**只在 users 表上加 tenant_id**，令牌与日志通过 user_id 归属到租户。
// 这样做的好处是不需要给每张表都加列，也就不存在"新增代码忘了写 tenant_id
// 导致跨租户泄漏"的风险 —— 归属关系只有一处真相。
//
// 约定：TenantId == 0 表示**平台（不隔离）**，可以看到全部租户的数据。
// 只有 root 能创建平台用户；普通租户用户必然是 TenantId > 0。

const (
	// TenantStatusEnabled / Disabled 租户状态
	TenantStatusEnabled  = 1
	TenantStatusDisabled = 2
)

// Tenant 租户。
type Tenant struct {
	Id        int    `json:"id" gorm:"primaryKey"`
	Name      string `json:"name" gorm:"unique;index" validate:"max=32"`
	Status    int    `json:"status" gorm:"default:1"`
	Remark    string `json:"remark" gorm:"index" validate:"max=200"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint"`
}

func (tenant *Tenant) Insert() error {
	if tenant.Name == "" {
		return errors.New("租户名不能为空")
	}
	now := helper.GetTimestamp()
	tenant.CreatedAt = now
	tenant.UpdatedAt = now
	if tenant.Status == 0 {
		tenant.Status = TenantStatusEnabled
	}
	return DB.Create(tenant).Error
}

func (tenant *Tenant) Update() error {
	if tenant.Id == 0 {
		return errors.New("租户 id 为空")
	}
	tenant.UpdatedAt = helper.GetTimestamp()
	return DB.Model(tenant).Select("name", "status", "remark", "updated_at").Updates(tenant).Error
}

func (tenant *Tenant) Delete() error {
	if tenant.Id == 0 {
		return errors.New("租户 id 为空")
	}
	// 还有用户归属该租户时不允许删除：否则这些用户会变成"孤儿"，
	// 平台视角下还能看到，租户视角下却谁都不认，属于最难排查的一类数据问题。
	var count int64
	if err := DB.Model(&User{}).Where("tenant_id = ?", tenant.Id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("该租户下仍有用户，请先迁移或删除这些用户")
	}
	return DB.Delete(tenant).Error
}

func GetTenantById(id int) (*Tenant, error) {
	if id == 0 {
		return nil, errors.New("租户 id 为空")
	}
	tenant := Tenant{Id: id}
	err := DB.First(&tenant, "id = ?", id).Error
	return &tenant, err
}

func GetTenantByName(name string) (*Tenant, error) {
	tenant := Tenant{}
	err := DB.First(&tenant, "name = ?", name).Error
	return &tenant, err
}

// GetAllTenants 分页查询租户（仅平台视角可调用）。
func GetAllTenants(startIdx int, num int) ([]*Tenant, error) {
	var tenants []*Tenant
	err := DB.Order("id desc").Limit(num).Offset(startIdx).Find(&tenants).Error
	return tenants, err
}

func GetTenantCount() (int64, error) {
	var count int64
	err := DB.Model(&Tenant{}).Count(&count).Error
	return count, err
}

// IsTenantEnabled 租户是否可用（用户登录/鉴权时的检查点）。
func IsTenantEnabled(id int) bool {
	if id == 0 {
		return true // 平台用户不属于任何租户
	}
	tenant, err := GetTenantById(id)
	if err != nil {
		return false
	}
	return tenant.Status == TenantStatusEnabled
}

// tenantScope 把「租户过滤」应用到一个查询上。
//
// 这是本功能唯一的隔离收口点：所有需要按租户隔离的查询都必须经过它，
// 而不是各处手写 Where("tenant_id = ?") —— 后者只要漏一处就是跨租户泄漏。
//
// tenantId == 0 表示平台视角，不加任何限制。
func tenantScope(db *gorm.DB, column string, tenantId int) *gorm.DB {
	if tenantId == 0 {
		return db
	}
	return db.Where(column+" = ?", tenantId)
}

// maxTenantUsersForLogScope 解析租户用户 ID 时的上限。
// 超过上限直接报错而不是静默截断 —— 截断会让统计结果悄悄偏小，
// 这比明确失败更难发现。
const maxTenantUsersForLogScope = 20000

// TenantUserIds 返回某租户下的全部用户 ID。
//
// 为什么用「先取 ID 再 IN」而不是子查询：日志表可能与主库不是同一个库
// （配置了独立的日志 DSN 时），跨库子查询会直接报错。
func TenantUserIds(tenantId int) ([]int, error) {
	var ids []int
	err := DB.Model(&User{}).Where("tenant_id = ?", tenantId).Limit(maxTenantUsersForLogScope + 1).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, err
	}
	if len(ids) > maxTenantUsersForLogScope {
		return nil, errors.New("该租户用户数过多，无法进行日志范围过滤")
	}
	return ids, nil
}

// scopeLogsByTenant 给日志查询加上「只属于该租户用户」的限制。
// tenantId == 0 时原样返回（平台视角）。
func scopeLogsByTenant(db *gorm.DB, tenantId int) (*gorm.DB, error) {
	if tenantId == 0 {
		return db, nil
	}
	ids, err := TenantUserIds(tenantId)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		// 该租户还没有任何用户：用一个恒假条件保证「查不到任何日志」，
		// 而不是退化成不过滤（那会把别的租户日志全暴露出来）。
		return db.Where("1 = 0"), nil
	}
	return db.Where("user_id IN ?", ids), nil
}
