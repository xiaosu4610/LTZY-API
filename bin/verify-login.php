<?php
declare(strict_types=1);

/**
 * 真实数据登录链路验证。
 *
 * 用法：php bin/verify-login.php
 *
 * 为什么不能只跑 bin/test-email-login.php：
 *   那个脚本自己造一个临时账号再删掉，只证明「代码路径通」，
 *   不证明「库里这 269 个真实账号能用」。导入的账号密码哈希是旧系统
 *   原样搬过来的（见 import-legacy-users.php），需要拿真实行验证。
 *
 * 导入账号的明文密码我们不知道，所以这里用「备份 → 临时哈希 → 测试 → 还原」：
 *   1. 读出原 password_hash / access_token 存起来
 *   2. 写入本次测试专用的临时哈希
 *   3. 走完整登录链路
 *   4. finally 里无条件还原，中途抛异常也不留后患
 * 这样既不破坏用户密码，又能验证真实行的 bcrypt 校验、状态判定、token 发放。
 *
 * 检查项分三组：
 *   A. 管理员 / 普通用户 / special 三类账号的真实登录
 *   B. 失败路径（错误密码、不存在用户、空输入、超长输入）
 *   C. 会话与余额（token 轮换、登出失效、余额与库一致、无越权字段泄露）
 *
 * ⚠️ 普通用户用临时账号（probe），不再依赖库里的 dealer_a / dealer_b。
 *    那两个是开发期手插的示例数据，已于 2026-10-28 连同假余额一起物理删除。
 */

require __DIR__ . '/_testkit.php';

$dbCfg = require __DIR__ . '/../config/database.php';
$dsn = sprintf('pgsql:host=%s;port=%d;dbname=%s', $dbCfg['host'], $dbCfg['port'], $dbCfg['database']);
$pdo = new PDO($dsn, $dbCfg['username'], $dbCfg['password'], [
    PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
    PDO::ATTR_EMULATE_PREPARES => false,
]);

$base = getenv('APP_BASE') ?: 'http://127.0.0.1:3000';

$pass = 0;
$fail = 0;

function check(string $name, bool $ok, string $detail = ''): void
{
    global $pass, $fail;
    if ($ok) {
        $pass++;
        printf("  ✓ %-44s %s\n", $name, $detail);
    } else {
        $fail++;
        printf("  ✗ %-44s %s\n", $name, $detail);
    }
}

/**
 * @return array{code:int,json:?array,raw:string,ms:int}
 */
function req(string $method, string $url, ?array $body = null, string $token = ''): array
{
    $ch = curl_init($url);
    $h = ['Content-Type: application/json'];
    if ($token !== '') {
        $h[] = 'Authorization: Bearer ' . $token;
    }
    curl_setopt_array($ch, [
        CURLOPT_CUSTOMREQUEST  => $method,
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT        => 30,
        CURLOPT_HTTPHEADER     => $h,
    ]);
    if ($body !== null) {
        curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($body, JSON_UNESCAPED_UNICODE));
    }
    $t0 = microtime(true);
    $raw = (string) curl_exec($ch);
    $ms = (int) round((microtime(true) - $t0) * 1000);
    $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
    return ['code' => $code, 'json' => json_decode($raw, true), 'raw' => $raw, 'ms' => $ms];
}

function login(string $user, string $pass): array
{
    global $base;
    return req('POST', $base . '/api/login', ['username' => $user, 'password' => $pass]);
}

/** 临时把某个账号的哈希改成已知密码，返回还原用的闭包 */
function withTempPassword(PDO $pdo, int $userId, string $plain): array
{
    $row = $pdo->prepare('SELECT password_hash, access_token FROM users WHERE id = ?');
    $row->execute([$userId]);
    $orig = $row->fetch(PDO::FETCH_ASSOC);
    if ($orig === false) {
        throw new RuntimeException("用户 {$userId} 不存在");
    }
    $pdo->prepare('UPDATE users SET password_hash = ? WHERE id = ?')
        ->execute([password_hash($plain, PASSWORD_BCRYPT, ['cost' => 12]), $userId]);
    return ['orig' => $orig, 'userId' => $userId];
}

function restorePassword(PDO $pdo, array $snap): void
{
    $pdo->prepare('UPDATE users SET password_hash = ?, access_token = ? WHERE id = ?')
        ->execute([$snap['orig']['password_hash'], $snap['orig']['access_token'], $snap['userId']]);
}

/** 直接读库里的余额（分） */
function dbQuota(PDO $pdo, int $userId): int
{
    $s = $pdo->prepare('SELECT quota FROM users WHERE id = ?');
    $s->execute([$userId]);
    return (int) $s->fetchColumn();
}

$snaps = [];

try {
    echo "═══ A. 真实账号登录 ═══\n";

    // ── A1. 管理员（密码从环境变量读，脚本里不存明文）──
    $admin = $pdo->query("SELECT id, username FROM users WHERE role >= 10 LIMIT 1")->fetch(PDO::FETCH_ASSOC);
    if ($admin === false) {
        check('存在管理员账号', false, 'users 表里没有 role>=10 的行');
    } else {
        $adminId = (int) $admin['id'];
        $adminPwd = getenv('E2E_ADMIN_PASS') ?: '';
        if ($adminPwd === '') {
            echo "  ⚠ 未设置 E2E_ADMIN_PASS，跳过管理员登录用例\n";
            $r = ['code' => 0, 'ms' => 0, 'json' => []];
        } else {
            $r = login((string) $admin['username'], $adminPwd);
        }
        check('管理员登录', $r['code'] === 200 && ($r['json']['success'] ?? false),
            "HTTP {$r['code']} {$r['ms']}ms");

        $d = $r['json']['data'] ?? [];
        check('管理员 is_manager=true', ($d['is_manager'] ?? null) === true,
            var_export($d['is_manager'] ?? null, true));

        // 响应体只该有这三个字段，多一个都算信息泄露
        $keys = array_keys($d);
        sort($keys);
        check('登录响应字段纯净', $keys === ['access_token', 'is_manager', 'user'], implode(',', $keys));

        // 逐步确认没有把内部概念漏给前端
        foreach (['role', 'redirect', 'status', 'account_type', 'group', 'grp'] as $leak) {
            check("登录响应不含 {$leak}", !array_key_exists($leak, $d) && !array_key_exists($leak, $d['user'] ?? []));
        }

        $adminTok = (string) ($d['access_token'] ?? '');
        check('管理员 token 长度 64', strlen($adminTok) === 64, strlen($adminTok) . ' 字符');

        // 管理员能进管理接口
        $m = req('GET', $base . '/api/manage/channels', null, $adminTok);
        check('管理员可访问管理接口', $m['code'] === 200, "HTTP {$m['code']}");
    }

    // ── A2. 普通用户：临时账号（跑完即删）──
    //
    // 以前这里登录的是库里写死的 dealer_a / dealer_b。那两个是开发期手插的
    // 示例经销商，2026-10-28 已连同假余额物理删除。测试不该依赖真实数据，
    // 所以改成运行时建、结束时删的临时账号 —— 测的是同一套「普通用户」判定：
    // 能登录、余额对得上、进不了管理接口、/api/account/self 不泄露内部字段。
    $probe = probeCreate('probe', 10);
    $probeTok = '';

    $ra = login($probe['username'], $probe['password']);
    check('普通用户登录', $ra['code'] === 200 && ($ra['json']['success'] ?? false),
        "HTTP {$ra['code']} {$ra['ms']}ms");
    check('普通用户 is_manager=false', ($ra['json']['data']['is_manager'] ?? null) === false);

    $probeTok = (string) ($ra['json']['data']['access_token'] ?? '');
    if ($probeTok !== '') {
        // 普通用户不能进管理接口 —— 越权检查
        $m = req('GET', $base . '/api/manage/channels', null, $probeTok);
        check('普通用户被拒于管理接口', $m['code'] === 403, "HTTP {$m['code']}");

        $me = req('GET', $base . '/api/me', null, $probeTok);
        $qy = (float) ($me['json']['data']['user']['quota_yuan'] ?? -1);
        $expect = dbQuota($pdo, $probe['id']) / 1_000_000;
        check('普通用户余额与库一致', abs($qy - $expect) < 0.005, "{$qy} 元 / 库 {$expect} 元");

        // /api/account/self 是用户自见接口，同样不能带内部字段
        $self = req('GET', $base . '/api/account/self', null, $probeTok);
        $su = $self['json']['data']['user'] ?? [];
        check('/api/account/self 可访问', $self['code'] === 200, "HTTP {$self['code']}");
        foreach (['role', 'status', 'group', 'grp', 'account_type'] as $leak) {
            check("/api/account/self 不含 {$leak}", !array_key_exists($leak, $su));
        }
    }

    // ── A4. 真实 special 账号：用户名登录 + 邮箱登录 ──
    // 挑一个有真实邮箱、username 不等于邮箱的账号，这样两条路径都能测到
    $sp = $pdo->query("
        SELECT id, username, email, quota FROM users
        WHERE account_type = 'special' AND role < 10
          AND email <> '' AND email NOT LIKE '%imported.local'
          AND username <> email AND username !~ '@'
          AND quota > 0
        ORDER BY quota DESC LIMIT 1
    ")->fetch(PDO::FETCH_ASSOC);

    if ($sp === false) {
        check('存在可测的 special 账号', false);
    } else {
        $sid = (int) $sp['id'];
        $temp = 'TempVerify_' . bin2hex(random_bytes(6));
        $snaps[$sid] = withTempPassword($pdo, $sid, $temp);
        try {
            printf("  测试账号：id=%d username=%s email=%s\n", $sid, $sp['username'], $sp['email']);

            $r1 = login((string) $sp['username'], $temp);
            check('special 用用户名登录', $r1['code'] === 200 && ($r1['json']['success'] ?? false),
                "HTTP {$r1['code']} {$r1['ms']}ms");
            check('special is_manager=false', ($r1['json']['data']['is_manager'] ?? null) === false);

            // 用邮箱登录 —— findByUsername 里的 @ 回退分支
            $r2 = login((string) $sp['email'], $temp);
            check('special 用邮箱登录', $r2['code'] === 200 && ($r2['json']['success'] ?? false),
                "HTTP {$r2['code']} {$r2['ms']}ms");

            // ⚠️ 每次登录都会刷新 access_token，$r1 的 token 此刻已经失效。
            //    所以下面必须用 $r2 的 token 去查身份，否则只会拿到 401。
            $t2 = (string) ($r2['json']['data']['access_token'] ?? '');
            $me2 = req('GET', $base . '/api/me', null, $t2);
            check('邮箱登录身份一致',
                ($me2['json']['data']['user']['username'] ?? '') === (string) $sp['username'],
                ($me2['json']['data']['user']['username'] ?? '?') . ' ← 期望 ' . $sp['username']);

            $qy = (float) ($me2['json']['data']['user']['quota_yuan'] ?? -1);
            $exp = dbQuota($pdo, $sid) / 1_000_000;
            check('special 余额与库一致', abs($qy - $exp) < 0.005, "{$qy} 元 / 库 {$exp} 元");
        } finally {
            restorePassword($pdo, $snaps[$sid]);
            unset($snaps[$sid]);
        }
    }

    // ── A5. username 本身就是邮箱的账号（旧系统常见）──
    $mailUser = $pdo->query("
        SELECT id, username FROM users
        WHERE account_type = 'special' AND role < 10 AND username ~ '@' AND quota > 0
        ORDER BY quota DESC LIMIT 1
    ")->fetch(PDO::FETCH_ASSOC);
    if ($mailUser !== false) {
        $mid = (int) $mailUser['id'];
        $temp = 'TempVerify_' . bin2hex(random_bytes(6));
        $snaps[$mid] = withTempPassword($pdo, $mid, $temp);
        try {
            $r = login((string) $mailUser['username'], $temp);
            check('邮箱形态用户名登录', $r['code'] === 200 && ($r['json']['success'] ?? false),
                "HTTP {$r['code']} " . $mailUser['username']);
        } finally {
            restorePassword($pdo, $snaps[$mid]);
            unset($snaps[$mid]);
        }
    }

    echo "\n═══ B. 失败路径 ═══\n";

    $r = login($probe['username'], 'definitely-wrong-' . bin2hex(random_bytes(4)));
    check('错误密码被拒', $r['code'] === 401, "HTTP {$r['code']} {$r['ms']}ms");

    $r = login('no_such_user_' . bin2hex(random_bytes(4)), 'whatever');
    check('不存在的用户被拒', $r['code'] === 401, "HTTP {$r['code']} {$r['ms']}ms");

    // 时序侧信道：不存在用户与错误密码耗时差不能太大
    $r1 = login($probe['username'], 'wrong-pass-x');
    $r2 = login('ghost_' . bin2hex(random_bytes(4)), 'wrong-pass-x');
    $diff = abs($r1['ms'] - $r2['ms']);
    check('不存在用户与错误密码耗时相近', $diff < 120, "{$r1['ms']}ms vs {$r2['ms']}ms，差 {$diff}ms");

    $r = login('', '');
    check('空输入被拒', $r['code'] === 400, "HTTP {$r['code']}");

    $r = login(str_repeat('a', 100), 'x');
    check('超长用户名被拒', $r['code'] === 401, "HTTP {$r['code']}");

    $r = req('GET', $base . '/api/me');
    check('未带 token 访问 /api/me', $r['code'] === 401, "HTTP {$r['code']}");

    $r = req('GET', $base . '/api/me', null, str_repeat('f', 64));
    check('伪造 token 被拒', $r['code'] === 401, "HTTP {$r['code']}");

    echo "\n═══ C. 会话与余额 ═══\n";

    // token 轮换：再登录一次，上一个 token 必须失效
    $x1 = login($probe['username'], $probe['password']);
    $tk1 = (string) ($x1['json']['data']['access_token'] ?? '');
    $x2 = login($probe['username'], $probe['password']);
    $tk2 = (string) ($x2['json']['data']['access_token'] ?? '');
    check('第二次登录换发新 token', $tk1 !== '' && $tk2 !== '' && $tk1 !== $tk2);

    // 缓存 5 秒 TTL，等一下让旧 token 的缓存自然过期
    usleep(5_200_000);
    $old = req('GET', $base . '/api/me', null, $tk1);
    check('旧 token 已失效', $old['code'] === 401, "HTTP {$old['code']}");

    // 登出后当前 token 应立即失效（不等缓存 TTL）
    $o = req('POST', $base . '/api/logout', [], $tk2);
    check('登出返回成功', $o['code'] === 200, "HTTP {$o['code']}");
    $after = req('GET', $base . '/api/me', null, $tk2);
    check('登出后 token 立即失效', $after['code'] === 401, "HTTP {$after['code']}");

    // ── 全体账号登录可用性（静态判定，不打 269 次请求）──
    $stat = $pdo->query("
        SELECT
          count(*) AS total,
          count(*) FILTER (WHERE status <> 1) AS disabled,
          count(*) FILTER (WHERE password_hash !~ '^\\\$2[aby]\\\$') AS bad_hash,
          count(*) FILTER (WHERE quota < 0) AS neg
        FROM users
    ")->fetch(PDO::FETCH_ASSOC);
    check('全部账号状态正常', (int) $stat['disabled'] === 0, "停用 {$stat['disabled']} 个");
    check('全部账号哈希格式正确', (int) $stat['bad_hash'] === 0, "异常 {$stat['bad_hash']} 个");
    check('无负余额', (int) $stat['neg'] === 0, "负数 {$stat['neg']} 个");

    // 余额汇总
    // ⚠️ 必须排除临时测试账号，否则它会把自己那 10 元算进真实余额里，
    //    输出看起来就像线上余额多了一笔，误导性很强。
    $sum = $pdo->query("
        SELECT account_type, count(*) AS n, sum(quota) AS q
        FROM users
        WHERE role < 10 AND username !~ '^(probe|tmp)_[0-9a-f]{8}$'
        GROUP BY account_type ORDER BY account_type
    ")->fetchAll(PDO::FETCH_ASSOC);
    echo "\n  余额汇总：\n";
    foreach ($sum as $s) {
        printf("    %-8s %3d 个   %10.2f 元\n", $s['account_type'], $s['n'], $s['q'] / 1_000_000);
    }
} finally {
    // 无条件还原所有被改过的账号
    foreach ($snaps as $snap) {
        restorePassword($pdo, $snap);
    }
    echo "\n  已还原被改动的密码哈希与 access_token\n";

    // 临时账号必须删干净 —— 中途抛异常也要删，否则库里会留测试残留
    if (isset($probe)) {
        probeDelete($probe['id']);
        echo "  已删除临时测试账号：{$probe['username']}\n";
    }
}

echo "\n" . str_repeat('─', 62) . "\n";
printf("通过 %d 项，失败 %d 项\n", $pass, $fail);
exit($fail === 0 ? 0 : 1);
