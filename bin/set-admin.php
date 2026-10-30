<?php
declare(strict_types=1);

/**
 * 设置管理员账号。
 *
 * 用法：php bin/set-admin.php [用户名] [密码]
 *
 * 为什么单独一个脚本：管理员密码不该出现在代码里，
 * 也不该通过 SQL 手工 UPDATE（容易把哈希写错，而且 psql 传中文/特殊字符有坑）。
 */

$autoload = __DIR__ . '/../vendor/autoload.php';
if (is_file($autoload)) {
    require $autoload;
}

$dbCfg = require __DIR__ . '/../config/database.php';
$dsn = sprintf('pgsql:host=%s;port=%d;dbname=%s', $dbCfg['host'], $dbCfg['port'], $dbCfg['database']);

$pdo = new PDO($dsn, $dbCfg['username'], $dbCfg['password'], [
    PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
]);

$username = $argv[1] ?? '17764753865';
$password = $argv[2] ?? '471142Qw..';

if (strlen($password) < 8) {
    fwrite(STDERR, "密码至少 8 位\n");
    exit(1);
}

// bcrypt cost=12：比默认 10 慢 4 倍，但对登录接口来说完全可以接受
// （登录是低频操作），换来的是离线爆破难度提升 4 倍。
//
// ⚠️ 这个 cost 必须和 AuthController 里的 dummyHash 保持一致，
// 否则"用户不存在"和"密码错误"的响应时间会有明显差异（时序侧信道）。
$hash = password_hash($password, PASSWORD_BCRYPT, ['cost' => 12]);

$pdo->beginTransaction();
try {
    // 先看这个用户名是否已被占用
    $exists = $pdo->prepare('SELECT id, role FROM users WHERE username = ?');
    $exists->execute([$username]);
    $row = $exists->fetch(PDO::FETCH_ASSOC);

    if ($row !== false) {
        // 存在就升级为超管并重置密码
        $pdo->prepare('UPDATE users SET password_hash = ?, role = 100, status = 1 WHERE id = ?')
            ->execute([$hash, $row['id']]);
        $id = (int) $row['id'];
        printf("已更新现有账号 id=%d 为超管\n", $id);
    } else {
        // 不存在则新建
        $pdo->prepare('
            INSERT INTO users (username, password_hash, display_name, role, status, grp, quota)
            VALUES (?, ?, ?, 100, 1, ?, 0)
        ')->execute([$username, $hash, '管理员', 'ltzy']);
        $id = (int) $pdo->lastInsertId();
        printf("已创建管理员 id=%d\n", $id);
    }

    // 顺手把旧的 admin 账号降级（避免留着默认口令的入口）
    $demote = $pdo->prepare("UPDATE users SET role = 1 WHERE username = 'admin' AND username <> ?");
    $demote->execute([$username]);
    if ($demote->rowCount() > 0) {
        echo "已把旧 admin 账号降级为普通账户（默认口令不再有管理权限）\n";
    }

    $pdo->commit();
} catch (Throwable $e) {
    $pdo->rollBack();
    fwrite(STDERR, "失败: " . $e->getMessage() . "\n");
    exit(1);
}

// 验证
$verify = $pdo->prepare('SELECT username, role, status, password_hash FROM users WHERE username = ?');
$verify->execute([$username]);
$u = $verify->fetch(PDO::FETCH_ASSOC);

printf("\n账号: %s\n角色: %d (超管)\n状态: %d\n", $u['username'], $u['role'], $u['status']);
printf("密码验证: %s\n", password_verify($password, $u['password_hash']) ? '通过' : '失败');
printf("哈希算法: %s (cost 12)\n", password_get_info($u['password_hash'])['algoName']);
