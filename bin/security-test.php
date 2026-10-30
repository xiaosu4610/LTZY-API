<?php
declare(strict_types=1);

require __DIR__ . '/_testkit.php';

/**
 * 安全测试：SQL 注入 + XSS + 边界。
 *
 * 用法：php bin/security-test.php
 */

$base = 'http://127.0.0.1:3000';

function post(string $url, array $body): array
{
    $ch = curl_init($url);
    curl_setopt_array($ch, [
        CURLOPT_POST           => true,
        CURLOPT_POSTFIELDS     => json_encode($body, JSON_UNESCAPED_UNICODE),
        CURLOPT_HTTPHEADER     => ['Content-Type: application/json'],
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT        => 30,
    ]);
    $raw = curl_exec($ch);
    $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
    // PHP 8.0+ 句柄由 GC 回收，不调 curl_close
    return ['code' => $code, 'body' => (string) $raw, 'json' => json_decode((string) $raw, true)];
}

echo "=== 1. SQL 注入测试 ===\n";

$injections = [
    "' OR '1'='1",
    "admin'--",
    "' OR 1=1--",
    "'; DROP TABLE users;--",
    "' UNION SELECT 1,2,3--",
    "admin' AND '1'='1",
    "\\' OR 1=1--",
    "' OR SLEEP(5)--",
    "' OR BENCHMARK(5000000,MD5(1))--",
    "1' AND (SELECT COUNT(*) FROM users)>0--",
    str_repeat("'", 100),
    "\\x00' OR 1=1--",
    "$(whoami)",
    "`whoami`",
];

$blocked = 0;
foreach ($injections as $inj) {
    // 普通用户：临时账号，跑完即删（库里不再保留示例数据）
$probe = probeCreate('probe', 10);
$r = post($base . '/api/login', ['username' => $inj, 'password' => 'x']);
    $ok = $r['json']['success'] ?? false;
    $msg = $r['json']['message'] ?? '';

    if ($ok) {
        printf("  ✗ 危险！注入成功: %s\n", $inj);
    } else {
        $blocked++;
    }
}
printf("  ✓ %d / %d 个注入载荷被拦截\n", $blocked, count($injections));

echo "\n=== 2. 超长输入测试 ===\n";
$longUser = str_repeat('a', 10000);
$r = post($base . '/api/login', ['username' => $longUser, 'password' => 'x']);
printf("  10000 字符用户名: HTTP %d %s\n", $r['code'], $r['json']['message'] ?? '');

$longPass = str_repeat('p', 100000);
$r = post($base . '/api/login', ['username' => 'test', 'password' => $longPass]);
printf("  100000 字符密码: HTTP %d %s\n", $r['code'], $r['json']['message'] ?? '');

echo "\n=== 3. 空输入测试 ===\n";
foreach ([
    ['username' => '', 'password' => 'x'],
    ['username' => 'x', 'password' => ''],
    ['username' => '', 'password' => ''],
    ['username' => '   ', 'password' => '   '],
] as $case) {
    $r = post($base . '/api/login', $case);
    printf("  %-30s HTTP %d\n", json_encode($case, JSON_UNESCAPED_UNICODE), $r['code']);
}

echo "\n=== 4. 类型混淆测试 ===\n";
foreach ([
    ['username' => ['array'], 'password' => 'x'],
    ['username' => 123, 'password' => 'x'],
    ['username' => null, 'password' => null],
    ['username' => true, 'password' => false],
] as $case) {
    $r = post($base . '/api/login', $case);
    $safe = ($r['json']['success'] ?? false) ? '✗ 危险' : '✓ 安全';
    printf("  %-40s HTTP %d %s\n", json_encode($case), $r['code'], $safe);
}

echo "\n=== 5. 越权测试：不带 token 访问管理接口 ===\n";
$endpoints = [
    '/api/manage/accounts',
    '/api/admin/channels',
    '/api/admin/prices',
    '/api/admin/users',
    '/api/admin/stats',
    '/api/admin/logs',
    '/api/account/self',
    '/api/account/models',
    '/api/account/logs',
    '/api/account/usage',
];
foreach ($endpoints as $ep) {
    $ch = curl_init($base . $ep);
    curl_setopt_array($ch, [CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT => 15]);
    $raw = curl_exec($ch);
    $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $safe = ($code === 401 || $code === 403) ? '✓' : '✗ 未鉴权！';
    printf("  %-28s HTTP %d %s\n", $ep, $code, $safe);
}

echo "\n=== 6. 伪造 token 测试 ===\n";
$fakeTokens = [
    'fake-token-123',
    'sk-fake',
    str_repeat('a', 64),
    "' OR 1=1--",
    '../../etc/passwd',
];
foreach ($fakeTokens as $tk) {
    $ch = curl_init($base . '/api/account/self');
    curl_setopt_array($ch, [
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT        => 15,
        CURLOPT_HTTPHEADER     => ['Authorization: Bearer ' . $tk],
    ]);
    $raw = curl_exec($ch);
    $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $safe = ($code === 401) ? '✓' : '✗ 危险！';
    printf("  %-26s HTTP %d %s\n", substr($tk, 0, 24), $code, $safe);
}

echo "\n=== 7. XSS 载荷存储测试 ===\n";
// 登录拿 token 后测试模型名回显
$r = post($base . '/api/login', ['username' => $probe['username'], 'password' => $probe['password']]);
$token = $r['json']['data']['access_token'] ?? '';
if ($token !== '') {
    // 尝试用 XSS 名字创建令牌
    $ch = curl_init($base . '/api/token');
    curl_setopt_array($ch, [
        CURLOPT_POST           => true,
        CURLOPT_POSTFIELDS     => json_encode(['name' => '<script>alert(1)</script>']),
        CURLOPT_HTTPHEADER     => ['Content-Type: application/json', 'Authorization: Bearer ' . $token],
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT        => 15,
    ]);
    $raw = curl_exec($ch);
    $j = json_decode((string) $raw, true);
    printf("  创建含 XSS 的令牌名: %s\n", ($j['success'] ?? false) ? '已存入库（前端需转义）' : '被拒');
    echo "  说明：后端不做过滤是对的（存原文），关键靠前端输出时转义（esc()）\n";
} else {
    echo "  （登录被锁或失败，跳过）\n";
}

echo "\n=== 8. 路径穿越测试 ===\n";
foreach (['/../../etc/passwd', '/admin/../admin', '/plaza?x=../../../etc/passwd'] as $p) {
    $ch = curl_init($base . $p);
    curl_setopt_array($ch, [CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT => 10]);
    $raw = curl_exec($ch);
    $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $hasPasswd = str_contains((string) $raw, 'root:x:');
    printf("  %-36s HTTP %d %s\n", $p, $code, $hasPasswd ? '✗ 泄漏文件！' : '✓');
}

echo "\n完成。\n";

// 临时测试账号清理：脚本无论怎么退出（正常/异常/被 kill）都不会在库里留残留
if (isset($probe) && is_array($probe)) {
    probeDelete($probe['id']);
}
