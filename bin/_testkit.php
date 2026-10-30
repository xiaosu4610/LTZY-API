<?php
declare(strict_types=1);

/**
 * 测试账号工装。
 *
 * ── 为什么需要这个 ──
 *
 * 以前的测试脚本直接拿库里写死的 dealer_a / dealer_b 登录。那两个账号是开发期
 * 手工插进去的示例经销商，还各自带着 50 元 / 20 元假余额。2026-10-28 按要求把它们
 * 连同 admin（1000 元虚拟额度）一起物理删除了，于是所有测试脚本全线失败。
 *
 * 教训很明确：**测试不能依赖库里的特定真实数据**。真实数据会变、会被删，
 * 而测试必须随时能跑。现在统一改成「跑之前建、跑完就删」，库里永远不留残留。
 *
 * ── 用法 ──
 *
 *   require __DIR__ . '/_testkit.php';
 *
 *   $u = probeCreate('probe', 10);        // 建一个余额 10 元的临时用户
 *   try {
 *       // ... 用 $u['username'] / $u['password'] 登录测试 ...
 *   } finally {
 *       probeDelete($u['id']);            // 无论成败都删掉
 *   }
 */

/**
 * 测试专用数据库连接（独立于服务的连接池）。
 */
function probePdo(): PDO
{
    static $pdo = null;
    if ($pdo instanceof PDO) {
        return $pdo;
    }
    $cfg = require __DIR__ . '/../config/database.php';
    $pdo = new PDO(
        sprintf('pgsql:host=%s;port=%d;dbname=%s', $cfg['host'], $cfg['port'], $cfg['database']),
        $cfg['username'],
        $cfg['password'],
        [
            PDO::ATTR_ERRMODE            => PDO::ERRMODE_EXCEPTION,
            PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC,
        ]
    );
    return $pdo;
}

/**
 * 建一个临时测试用户。
 *
 * @param string $prefix    用户名前缀，便于在日志里认出是谁建的
 * @param int    $quotaYuan 余额（元）
 * @param int    $role      1=普通用户，100=管理员
 *
 * @return array{id:int,username:string,password:string}
 */
function probeCreate(string $prefix = 'probe', int $quotaYuan = 10, int $role = 1): array
{
    $pdo = probePdo();

    // 用户名带随机后缀，避免并发跑测试时撞 primary key
    $username = $prefix . '_' . bin2hex(random_bytes(4));
    $password = 'Probe' . bin2hex(random_bytes(6));

    // cost 必须和线上一致（12），否则登录耗时会和真实用户不同，
    // 测出来的时序数据没有参考价值。
    $hash = password_hash($password, PASSWORD_BCRYPT, ['cost' => 12]);

    $pdo->prepare('
        INSERT INTO users (username, password_hash, display_name, email, role, status, grp, quota, account_type)
        VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?)
    ')->execute([
        $username,
        $hash,
        '临时测试账号',
        $username . '@probe.local',
        $role,
        $role >= 10 ? 'ltzy' : 'default',
        $quotaYuan * 1_000_000,
        'special',   // 普通用户都归 special，经销商靠 account_models 区分
    ]);

    return [
        'id'       => (int) $pdo->lastInsertId(),
        'username' => $username,
        'password' => $password,
    ];
}

/**
 * 给临时用户授权某个模型（模拟经销商的专属价）。
 *
 * @return int 写入条数
 */
function probeGrantModel(int $userId, string $model, int $priceQuota): int
{
    // 先确认模型真实存在，否则 account_models 会挂一条没人能用的记录
    $exists = probePdo()->prepare('SELECT 1 FROM model_prices WHERE model = ?');
    $exists->execute([$model]);
    if ($exists->fetchColumn() === false) {
        return 0;
    }
    probePdo()->prepare('
        INSERT INTO account_models (user_id, model, price) VALUES (?, ?, ?)
        ON CONFLICT (user_id, model) DO UPDATE SET price = EXCLUDED.price
    ')->execute([$userId, $model, $priceQuota]);
    return 1;
}

/**
 * 彻底删除临时用户及其全部关联数据。
 *
 * 顺序和 purge-expired-accounts.php 保持一致：先删子表再删主表。
 */
function probeDelete(int $userId): void
{
    $pdo = probePdo();
    $pdo->beginTransaction();
    try {
        $pdo->prepare('DELETE FROM tokens WHERE user_id = ?')->execute([$userId]);
        $pdo->prepare('DELETE FROM account_models WHERE user_id = ?')->execute([$userId]);
        $pdo->prepare('DELETE FROM logs WHERE user_id = ?')->execute([$userId]);
        $pdo->prepare('DELETE FROM users WHERE id = ?')->execute([$userId]);
        $pdo->commit();
    } catch (Throwable $e) {
        $pdo->rollBack();
        throw $e;
    }
}

/**
 * 清理历史上可能残留的临时账号（正常流程不会留下，防止脚本中途被 kill）。
 */
function probeSweep(): int
{
    $rows = probePdo()->query("
        SELECT id FROM users WHERE username ~ '^(probe|tmp)_[0-9a-f]{8}$'
    ")->fetchAll(PDO::FETCH_COLUMN);

    foreach ($rows as $id) {
        probeDelete((int) $id);
    }
    return count($rows);
}
