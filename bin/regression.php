<?php
declare(strict_types=1);

require __DIR__ . '/_testkit.php';

/**
 * 全面回归测试。
 *
 * 用法：php bin/regression.php
 *
 * 覆盖：
 *   1. 页面可访问性 + 已删除的敏感路径必须 404
 *   2. 公开接口字段审计（不得泄漏技术栈 / 渠道 / 角色）
 *   3. 五种协议端到端
 *   4. 登录与权限边界
 *   5. 安全（注入 / 伪造 token / 越权）
 */

$base = 'http://127.0.0.1:3000';

$pass = 0;
$fail = 0;
$fails = [];

function http(string $method, string $url, ?array $body = null, ?string $auth = null, int $timeout = 90): array
{
    $ch = curl_init($url);
    $headers = ['Content-Type: application/json'];
    if ($auth !== null) {
        $headers[] = 'Authorization: Bearer ' . $auth;
    }
    curl_setopt_array($ch, [
        CURLOPT_CUSTOMREQUEST  => $method,
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT        => $timeout,
        CURLOPT_HTTPHEADER     => $headers,
        CURLOPT_HEADER         => false,
    ]);
    if ($body !== null) {
        curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($body, JSON_UNESCAPED_UNICODE));
    }
    $raw = curl_exec($ch);
    $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $server = (string) curl_getinfo($ch, CURLINFO_OS_ERRNO); // 占位
    return ['code' => $code, 'body' => (string) $raw, 'json' => json_decode((string) $raw, true)];
}

function check(string $name, bool $ok, string $detail = ''): void
{
    global $pass, $fail, $fails;
    if ($ok) {
        $pass++;
        printf("  ✓ %-46s %s\n", $name, $detail);
    } else {
        $fail++;
        $fails[] = $name . ($detail !== '' ? " ($detail)" : '');
        printf("  ✗ %-46s %s\n", $name, $detail);
    }
}

/* ══════════ 1. 页面 ══════════ */
echo "═══ 1. 页面 ═══\n";

foreach ([
    '/'        => '门户首页',
    '/plaza'   => '模型广场',
    '/login'   => '登录页',
    '/console' => '控制台',
] as $path => $label) {
    $r = http('GET', $base . $path);
    check("GET {$path}（{$label}）", $r['code'] === 200, "HTTP {$r['code']}");
}

echo "\n";

// 这些路径必须不存在
$removed = [
    '/admin', '/admin/users', '/admin/settings',
    '/dealer', '/dealer/dashboard',
    '/manage', '/backend', '/panel',
];
foreach ($removed as $path) {
    $r = http('GET', $base . $path);
    check("已隐藏 {$path}", $r['code'] === 404, "HTTP {$r['code']}");
}

/* ══════════ 2. 公开接口字段审计 ══════════ */
echo "\n═══ 2. 公开接口字段审计 ═══\n";

// 公开接口不该出现的词。
//
// 这里不点名任何具体上游 —— 本项目的公开接口对「所有」渠道名、
// 上游地址、密钥规模都必须屏蔽，而不是只挡某几家。
$leakWords = [
    'php_version', 'swoole_version', 'PostgreSQL', 'channels_total',
    'channels_enabled', '账号开关', 'register_enabled', 'topup_enabled',
    'channel_name', 'keys_total', 'keys_available',
    'base_url', 'upstream', 'merger', 'pusher',
    'redirect', 'is_admin', '"role"', '2.0.0-dev',
];

foreach (['/api/status', '/api/site', '/api/model/plaza', '/v1/models'] as $path) {
    $r = http('GET', $base . $path);
    $leaked = [];
    foreach ($leakWords as $w) {
        if (str_contains($r['body'], $w)) {
            $leaked[] = $w;
        }
    }
    check("{$path} 无泄漏", $leaked === [], $leaked === [] ? '' : implode(', ', $leaked));
}

// Server 头
$ch = curl_init($base . '/');
curl_setopt_array($ch, [CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT => 15, CURLOPT_HEADER => true]);
$raw = (string) curl_exec($ch);
check('响应头隐藏技术栈', !str_contains(strtolower($raw), 'swoole'), 'Server 头应为 nginx');

/* ══════════ 3. 五种协议 ══════════ */
echo "\n═══ 3. 五种协议 ═══\n";

$model = 'internlm3-8b-instruct';

// Chat
$r = http('POST', $base . '/v1/chat/completions', [
    'model' => $model, 'messages' => [['role' => 'user', 'content' => 'hi']], 'max_tokens' => 5,
]);
check('Chat 非流式', $r['code'] === 200 && isset($r['json']['choices']), "HTTP {$r['code']}");

// Responses
$r = http('POST', $base . '/v1/responses', [
    'model' => $model, 'input' => 'hi', 'max_output_tokens' => 5,
]);
check('Responses 非流式', $r['code'] === 200 && ($r['json']['object'] ?? '') === 'response', "HTTP {$r['code']}");

// Claude
$r = http('POST', $base . '/v1/messages', [
    'model' => $model, 'max_tokens' => 5, 'messages' => [['role' => 'user', 'content' => 'hi']],
]);
check('Claude 非流式', $r['code'] === 200 && ($r['json']['type'] ?? '') === 'message', "HTTP {$r['code']}");

// Gemini
$r = http('POST', $base . '/v1beta/models/' . $model . ':generateContent', [
    'contents' => [['role' => 'user', 'parts' => [['text' => 'hi']]]],
    'generationConfig' => ['maxOutputTokens' => 5],
]);
check('Gemini 非流式', $r['code'] === 200 && isset($r['json']['candidates']), "HTTP {$r['code']}");

// Embedding（模型 ID 从环境变量读，各部署环境配置不同）
$embedModel = getenv('E2E_EMBED_MODEL') ?: '';
if ($embedModel !== '') {
    $r = http('POST', $base . '/v1/embeddings', [
        'model' => $embedModel, 'input' => 'hi',
    ]);
    check('Embedding', $r['code'] === 200 && isset($r['json']['data']), "HTTP {$r['code']}");
}

/* ══════════ 4. 登录与权限 ══════════ */
echo "\n═══ 4. 登录与权限 ═══\n";

// 管理员凭据从环境变量读，不要写死在脚本里 ——
// 这个文件会进版本库，明文密码等于把后台权限公开。
$adminUser = getenv('E2E_ADMIN_USER') ?: '';
$adminPass = getenv('E2E_ADMIN_PASS') ?: '';
if ($adminUser === '' || $adminPass === '') {
    echo "  ⚠ 跳过管理员相关用例：未设置 E2E_ADMIN_USER / E2E_ADMIN_PASS\n";
}
$adminTok = '';
if ($adminUser !== '') {
    $admin = http('POST', $base . '/api/login', ['username' => $adminUser, 'password' => $adminPass]);
    $adminTok = $admin['json']['data']['access_token'] ?? '';
    check('管理员登录', $admin['code'] === 200 && $adminTok !== '', "HTTP {$admin['code']}");
}

// 普通用户：临时账号，跑完即删（库里不再保留示例数据）
$probe = probeCreate('probe', 10);
$account = http('POST', $base . '/api/login', ['username' => $probe['username'], 'password' => $probe['password']]);
$accountTok = $account['json']['data']['access_token'] ?? '';
check('普通用户登录', $account['code'] === 200 && $accountTok !== '', "HTTP {$account['code']}");

// 响应结构一致性
$adminFields = array_keys($admin['json']['data'] ?? []);
$accountFields = array_keys($account['json']['data'] ?? []);
sort($adminFields);
sort($accountFields);
check('两种角色响应字段一致', $adminFields === $accountFields, implode(',', $adminFields));
check('响应无 redirect 字段', !in_array('redirect', $adminFields, true));
check('user 对象无 role', !array_key_exists('role', $admin['json']['data']['user'] ?? []));

// 错误密码
$r = http('POST', $base . '/api/login', ['username' => $probe['username'], 'password' => 'wrong-' . bin2hex(random_bytes(4))]);
check('错误密码被拒', $r['code'] === 401);

// 越权
$r = http('GET', $base . '/api/manage/accounts', null, $accountTok);
check('普通用户访问管理接口被拒', $r['code'] === 403, "HTTP {$r['code']}");

$r = http('GET', $base . '/api/manage/accounts', null, $adminTok);
check('管理员访问管理接口', $r['code'] === 200, "HTTP {$r['code']}");

// 匿名
$r = http('GET', $base . '/api/manage/channels');
check('匿名访问管理接口被拒', $r['code'] === 403);

/* ══════════ 5. 安全 ══════════ */
echo "\n═══ 5. 安全 ═══\n";

// SQL 注入
$injections = ["' OR '1'='1", "admin'--", "'; DROP TABLE users;--", "' UNION SELECT 1--"];
$blocked = 0;
foreach ($injections as $inj) {
    $r = http('POST', $base . '/api/login', ['username' => $inj, 'password' => 'x']);
    if (!($r['json']['success'] ?? false)) {
        $blocked++;
    }
}
check('SQL 注入全部拦截', $blocked === count($injections), "{$blocked}/" . count($injections));

// 伪造 token
$fakes = ['fake-token', 'sk-fake', str_repeat('a', 64), "' OR 1=1--", '../../etc/passwd'];
$rejected = 0;
foreach ($fakes as $tk) {
    $r = http('GET', $base . '/api/account/self', null, $tk);
    if ($r['code'] === 401) {
        $rejected++;
    }
}
check('伪造 token 全部拒绝', $rejected === count($fakes), "{$rejected}/" . count($fakes));

// 路径穿越
$r = http('GET', $base . '/../../etc/passwd');
check('路径穿越无效', $r['code'] === 404 && !str_contains($r['body'], 'root:x:'));

// 超长输入
$r = http('POST', $base . '/api/login', ['username' => str_repeat('a', 5000), 'password' => 'x']);
check('超长用户名被拒', $r['code'] === 401 || $r['code'] === 400);

/* ══════════ 结果 ══════════ */
echo "\n" . str_repeat('═', 62) . "\n";
printf("通过 %d 项，失败 %d 项\n", $pass, $fail);
if ($fails !== []) {
    echo "\n失败项：\n";
    foreach ($fails as $f) {
        printf("  - %s\n", $f);
    }
}
// 临时测试账号清理：脚本无论怎么退出（正常/异常/被 kill）都不会在库里留残留
if (isset($probe) && is_array($probe)) {
    probeDelete($probe['id']);
}

exit($fail === 0 ? 0 : 1);
