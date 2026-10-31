<?php
declare(strict_types=1);

namespace Ltzy\Domain\User;

// ══════════════════════════════════════════════════════════════
// 账号类型（account_type）
//
//   special —— 从旧系统导入的历史用户（有余额、可能用邮箱登录）
//   managed —— 管理员手工创建
//   normal  —— 自助注册
//   agent   —— 分销账号（拿货价 / 禁自助充值）
//
// ⚠️ 所有类型的账号都不再自动删除。
//
//   原规则是「special 账号余额耗尽后物理删除」，配套脚本
//   bin/purge-expired-accounts.php。已整体废弃并删除，原因：
//
//     1. 开始正常运营后用户会充值。余额为 0 是正常的中间状态
//        （刚用完准备再充），删号等于把人赶走；
//     2. 账号背后挂着调用日志、令牌、充值订单，物理删除会让
//        对账数据断链；
//     3. 那套规则原本是为了清走不活跃的迁移用户省资源，
//        而免费模型不扣费，留一个账号的成本几乎为零。
//
//   账号只会在管理员显式操作时删除。account_type 现在只是来源标记，
//   不再影响生命周期。
// ══════════════════════════════════════════════════════════════

/**
 * 账户实体。
 *
 * quota / used_quota 的单位是「内部额度」（1 元 = 1,000,000），
 * 与计费体系保持一致 —— 这样导入历史数据时不用做单位换算。
 */
final class User
{
    public function __construct(
        public readonly int $id,
        public readonly string $username,
        public readonly string $passwordHash,
        public readonly string $displayName,
        public readonly string $email,
        public readonly int $role,
        public readonly int $status,
        public readonly string $group,
        public readonly int $quota,
        public readonly int $usedQuota,
        public readonly int $requestCount,
        public readonly string $remark,
        public readonly ?string $lastLoginAt = null,
        /**
         * 账号来源/类型标记：normal（自助注册）/ special（旧系统迁移）/
         * managed（管理员）/ agent（分销）。
         *
         * ⚠️ 除 agent 外，其余都不影响业务逻辑，只是来源记录。
         * agent 会影响界面与充值权限：拿货价展示、禁止自助充值。
         */
        public readonly string $accountType = 'normal',
    ) {
    }

    public function isEnabled(): bool
    {
        return $this->status === 1;
    }

    public function isAdmin(): bool
    {
        return $this->role >= 10;
    }

    public function isSuperAdmin(): bool
    {
        return $this->role >= 100;
    }

    /**
     * 是否为分销（agent）账号。
     *
     * 与普通用户的三点差异：
     *   1. 看到的是拿货价，并显示与零售价的差价（利润空间）
     *   2. 不能自助充值，余额由平台方手工添加
     *   3. 界面不出现充值入口
     */
    public function isAgent(): bool
    {
        return $this->accountType === 'agent';
    }

    /**
     * 余额是否足够。
     *
     * ⚠️ 管理员豁免：role >= 10 视为不限额度。
     * 否则运营自己测模型时会被自己的余额卡住，很难用。
     */
    public function hasQuota(int $needed = 1): bool
    {
        if ($this->isAdmin()) {
            return true;
        }
        return $this->quota >= $needed || $this->quota < 0;
    }
}
