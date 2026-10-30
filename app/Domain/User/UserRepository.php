<?php
declare(strict_types=1);

namespace Ltzy\Domain\User;

use Ltzy\Db\Connection;

/**
 * 账户与令牌的数据访问。
 *
 * 所有方法都返回对象/数组，不返回 PDOStatement —— 因为连接会被归还到池里，
 * 结果集必须在归还前取完（见 Connection::query 的注释）。
 */
final class UserRepository
{
    public function __construct(private readonly Connection $db)
    {
    }

    public function findById(int $id): ?User
    {
        $row = $this->db->first('SELECT * FROM users WHERE id = ?', [$id]);
        return $row === null ? null : $this->hydrate($row);
    }

    /**
     * 按用户名或邮箱查找账户。
     *
     * ── 为什么同时查 email ──
     *
     * 从旧生产系统导入的用户里，有一半的 username 不是邮箱
     * （如 "灰灰"、"zakoxiaolao6"），但他们的 email 是 QQ 邮箱。
     * 如果只按 username 查，这些人记不住自己的用户名就登录不了。
     *
     * 查询优先级：
     *   1. username 精确匹配（最常见，也最快）
     *   2. email 精确匹配（fallback）
     *
     * 为什么不用 OR 一次查完再判断歧义：
     *   如果有个用户的 username 恰好等于另一个用户的 email，
     *   OR 会返回两行，还得额外处理。分开查语义更清楚：
     *   "先当作用户名试，不行再当邮箱试"。
     *
     * @param string $identifier 用户名或邮箱
     */
    public function findByUsername(string $identifier): ?User
    {
        $row = $this->db->first('SELECT * FROM users WHERE username = ?', [$identifier]);
        if ($row !== null) {
            return $this->hydrate($row);
        }

        // 含 @ 才可能是邮箱，省一次无谓查询
        if (!str_contains($identifier, '@')) {
            return null;
        }

        $row = $this->db->first(
            "SELECT * FROM users WHERE email = ? AND email <> '' LIMIT 1",
            [$identifier]
        );
        return $row === null ? null : $this->hydrate($row);
    }

    /**
     * 只按用户名查（管理接口用，避免"搜邮箱搜出一堆"的歧义）。
     */
    public function findByExactUsername(string $username): ?User
    {
        $row = $this->db->first('SELECT * FROM users WHERE username = ?', [$username]);
        return $row === null ? null : $this->hydrate($row);
    }

    /**
     * 按网页会话 token 查账户。
     */
    public function findByAccessToken(string $token): ?User
    {
        if ($token === '') {
            return null;
        }
        $row = $this->db->first('SELECT * FROM users WHERE access_token = ? AND access_token <> \'\'', [$token]);
        return $row === null ? null : $this->hydrate($row);
    }

    /**
     * 按令牌查找账户。
     *
     * 这里 JOIN tokens 而不是先查 token 再查 user —— 少一次数据库往返。
     * 热路径上每个 API 请求都要走这条查询。
     *
     * @return array{user:User,token_id:int,remain_quota:int,models:string}|null
     */
    public function findByTokenKey(string $key): ?array
    {
        $row = $this->db->first('
            SELECT u.*, t.id AS t_id, t.remain_quota AS t_remain, t.models AS t_models,
                   t.status AS t_status, t.expired_at
            FROM tokens t
            JOIN users u ON u.id = t.user_id
            WHERE t."key" = ?
        ', [$key]);

        if ($row === null) {
            return null;
        }

        // 令牌本身的状态也要校验 —— 账户正常不代表某把令牌没被停用
        if ((int) $row['t_status'] !== 1) {
            return null;
        }

        $expired = $row['expired_at'];
        if ($expired !== null && strtotime((string) $expired) < time()) {
            return null;
        }

        return [
            'user'         => $this->hydrate($row),
            'token_id'     => (int) $row['t_id'],
            'remain_quota' => (int) $row['t_remain'],
            'models'       => (string) $row['t_models'],
        ];
    }

    /**
     * 创建新用户。
     *
     * ⚠️ account_type 必须显式传，不能靠数据库默认值 ——
     * 那个默认是 'managed'（经销商），新注册用户用了会被当成经销商。
     *
     * 三类账号（2026-10-29 起仅作来源标记，生命周期完全一致，
     * 都不会被自动删除）：
     *   special —— 旧系统迁移来的历史用户
     *   managed —— 经销商 / 我们自己开的
     *   normal  —— 自助注册的普通用户
     */
    public function create(
        string $username,
        string $passwordHash,
        string $group = 'default',
        int $role = 1,
        string $accountType = 'normal',
    ): int {
        $this->db->execute('
            INSERT INTO users (username, password_hash, display_name, grp, role, status, quota, account_type)
            VALUES (?, ?, ?, ?, ?, 1, 0, ?)
        ', [$username, $passwordHash, $username, $group, $role, $accountType]);

        return (int) $this->db->scalar('SELECT id FROM users WHERE username = ?', [$username]);
    }

    /**
     * 用户名是否已被占用（精确匹配，不做邮箱回退）。
     */
    public function usernameExists(string $username): bool
    {
        return $this->db->first('SELECT 1 FROM users WHERE username = ?', [$username]) !== null;
    }

    /**
     * 记录一次消费（余额、累计用量、请求数）。
     */
    public function consume(int $userId, int $quota): void
    {
        if ($userId <= 0) {
            return;
        }
        $this->db->execute('
            UPDATE users
            SET quota = quota - ?, used_quota = used_quota + ?, request_count = request_count + 1
            WHERE id = ?
        ', [$quota, $quota, $userId]);
    }

    /**
     * 令牌消耗（remain_quota 为 0 表示不限额，不动它）。
     */
    public function consumeToken(int $tokenId, int $quota): void
    {
        if ($tokenId <= 0) {
            return;
        }
        $this->db->execute('
            UPDATE tokens
            SET used_quota = used_quota + ?,
                remain_quota = CASE WHEN remain_quota > 0 THEN GREATEST(remain_quota - ?, 0) ELSE remain_quota END
            WHERE id = ?
        ', [$quota, $quota, $tokenId]);
    }

    public function touchLogin(int $userId): void
    {
        $this->db->execute('UPDATE users SET last_login_at = now() WHERE id = ?', [$userId]);
    }

    /**
     * 更新网页会话 token。
     */
    public function setAccessToken(int $userId, string $token): void
    {
        $this->db->execute('UPDATE users SET access_token = ? WHERE id = ?', [$token, $userId]);
    }

    /**
     * 更新密码哈希（登录时发现旧格式则自动升级）。
     */
    public function updatePasswordHash(int $userId, string $hash): void
    {
        $this->db->execute('UPDATE users SET password_hash = ? WHERE id = ?', [$hash, $userId]);
    }

    /**
     * 创建令牌，返回明文 key。
     */
    public function createToken(int $userId, string $name, int $remainQuota = 0, string $models = '', int $expiredDays = 0): string
    {
        // sk- + 48 位随机。用 random_bytes 而不是 uniqid/rand ——
        // 令牌是凭证，必须密码学安全随机。
        $key = 'sk-' . bin2hex(random_bytes(24));

        $this->db->execute('
            INSERT INTO tokens (user_id, name, "key", remain_quota, models, expired_at)
            VALUES (?, ?, ?, ?, ?, ?)
        ', [
            $userId,
            $name,
            $key,
            $remainQuota,
            $models,
            $expiredDays > 0 ? date('Y-m-d H:i:s', time() + $expiredDays * 86400) : null,
        ]);

        return $key;
    }

    /** @return array<int,array<string,mixed>> */
    public function listTokens(int $userId): array
    {
        return $this->db->query('
            SELECT id, name, "key", status, remain_quota, used_quota, models, created_at, expired_at
            FROM tokens WHERE user_id = ? ORDER BY id DESC
        ', [$userId]);
    }

    public function deleteToken(int $userId, int $tokenId): bool
    {
        return $this->db->execute('DELETE FROM tokens WHERE id = ? AND user_id = ?', [$tokenId, $userId]) > 0;
    }

    /** @param array<string,mixed> $row */
    private function hydrate(array $row): User
    {
        return new User(
            id: (int) $row['id'],
            username: (string) $row['username'],
            passwordHash: (string) $row['password_hash'],
            displayName: (string) $row['display_name'],
            email: (string) $row['email'],
            role: (int) $row['role'],
            status: (int) $row['status'],
            group: (string) $row['grp'],
            quota: (int) $row['quota'],
            usedQuota: (int) $row['used_quota'],
            requestCount: (int) $row['request_count'],
            remark: (string) $row['remark'],
            lastLoginAt: isset($row['last_login_at']) ? (string) $row['last_login_at'] : null,
        );
    }
}
