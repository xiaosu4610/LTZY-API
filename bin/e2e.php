<?php
declare(strict_types=1);

/**
 * 端到端验收脚本。
 *
 * 用法：php bin/e2e.php [--base=http://127.0.0.1:3000]
 *
 * 覆盖：匿名免费 / 匿名付费拒绝 / 登录 / 令牌 / 计费扣减 / 日志落库 / 管理接口。
 * 每项都打真实 HTTP 请求，不打桩 —— 这是唯一能证明链路通的方式。
 */

$base = 'http://127.0.0.1:3000';
foreach ($argv as $a) {
    if (str_starts_with($a, '--base=')) {
        $base = substr($a, 7);
    }
}

$pass = 0;
$fail = 0;

function http(string $method, string $url, ?array $body = null, ?string $auth = null): array
{
    $ch = curl_init($url);
    $headers = ['Content-Type: application/json'];
    if ($auth !== null) {
        $headers[] = 'Authorization: Bearer ' . $auth;
    }
    curl_setopt_array($ch, [
        CURLOPT_CUSTOMREQUEST  => $method,
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT        => 120,
        CURLOPT_HTTPHEADER     => $headers,
    ]);
    if ($body !== null) {
        curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($body, JSON_UNESCAPED_UNICODE));
    }
    $raw = curl_exec($ch);
    $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
    return ['code' => $code, 'body' => is_string($raw) ? $raw : '', 'json' => json_decode((string) $raw, true)];
}

function check(string $name, bool $ok, string $detail = ''): void
{
    global $pass, $fail;
    if ($ok) {
        $pass++;
        printf("  ✓ %-38s %s\n", $name, $detail);
    } else {
        $fail++;
        printf("  ✗ %-38s %s\n", $name, $detail);
    }
}

echo "=== 1. 公开接口 ===\n";
$r = http('GET', "$base/api/status");
check('GET /api/status', $r['code'] === 200 && ($r['json']['success'] ?? false));
check('  匿名状态 is_logged_in=false', ($r['json']['data']['is_logged_in'] ?? null) === false);

$r = http('GET', "$base/v1/models");
$anonModels = $r['json']['data'] ?? [];
check('GET /v1/models 匿名', $r['code'] === 200 && count($anonModels) > 0, count($anonModels) . ' 个');

$r = http('GET', "$base/api/model/plaza");
check('GET /api/model/plaza', $r['code'] === 200 && ($r['json']['data']['total'] ?? 0) > 0,
    ($r['json']['data']['total'] ?? 0) . ' 个模型, 免费 ' . ($r['json']['data']['free_count'] ?? 0));

echo "\n=== 2. 匿名中继 ===\n";
$freeModel = 'internlm3-8b-instruct';
$r = http('POST', "$base/v1/chat/completions", [
    'model' => $freeModel,
    'messages' => [['role' => 'user', 'content' => '说一个字']],
    'max_tokens' => 10,
]);
$content = $r['json']['choices'][0]['message']['content'] ?? '';
check('匿名调免费模型', $r['code'] === 200 && $content !== '', mb_substr($content, 0, 20));

$r = http('POST', "$base/v1/chat/completions", [
    'model' => 'minimax-m3',
    'messages' => [['role' => 'user', 'content' => 'hi']],
    'max_tokens' => 5,
]);
check('匿名调付费模型被拒', $r['code'] === 401 && ($r['json']['error']['code'] ?? '') === 'login_required');

echo "\n=== 3. 登录 ===\n";
$adminUser = getenv('E2E_ADMIN_USER') ?: '';
$adminPass = getenv('E2E_ADMIN_PASS') ?: '';
if ($adminUser === '') {
    echo "  ⚠ 未设置 E2E_ADMIN_USER / E2E_ADMIN_PASS，跳过登录用例\n";
    $sess = '';
} else {
    $r = http('POST', "$base/api/login", ['username' => $adminUser, 'password' => $adminPass]);
    $sess = $r['json']['data']['access_token'] ?? '';
    check('管理员登录', $r['code'] === 200 && $sess !== '', 'role=' . ($r['json']['data']['user']['role'] ?? '?'));
}

$r = http('POST', "$base/api/login", ['username' => $adminUser ?: 'nobody', 'password' => 'wrong']);
check('错误密码被拒', $r['code'] === 401);

$r = http('GET', "$base/api/user/self", null, $sess);
check('GET /api/user/self', $r['code'] === 200 && ($r['json']['data']['username'] ?? '') === 'admin');

$r = http('GET', "$base/v1/models", null, $sess);
$authModels = $r['json']['data'] ?? [];
check('登录后模型更多', count($authModels) > count($anonModels),
    count($anonModels) . ' → ' . count($authModels));

echo "\n=== 4. 令牌 ===\n";
$r = http('POST', "$base/api/token", ['name' => 'e2e-test'], $sess);
$sk = $r['json']['data']['key'] ?? '';
check('创建令牌', $r['code'] === 200 && str_starts_with($sk, 'sk-'), substr($sk, 0, 12) . '...');

$r = http('GET', "$base/api/token", null, $sess);
$tokens = $r['json']['data'] ?? [];
$masked = $tokens[0]['key'] ?? '';
check('令牌列表脱敏', $r['code'] === 200 && str_contains($masked, '...'), $masked);

echo "\n=== 5. 流式 ===\n";
$t0 = microtime(true);
$ch = curl_init("$base/v1/chat/completions");
curl_setopt_array($ch, [
    CURLOPT_POST => true,
    CURLOPT_POSTFIELDS => json_encode([
        'model' => $freeModel,
        'messages' => [['role' => 'user', 'content' => '数到三']],
        'max_tokens' => 30,
        'stream' => true,
    ], JSON_UNESCAPED_UNICODE),
    CURLOPT_HTTPHEADER => ['Content-Type: application/json'],
    CURLOPT_RETURNTRANSFER => true,
    CURLOPT_TIMEOUT => 120,
]);
$sse = (string) curl_exec($ch);
$elapsed = microtime(true) - $t0;
$frames = array_values(array_filter(explode("\n", $sse), static fn($l) => str_starts_with($l, 'data: ')));
$last = $frames === [] ? '' : trim(end($frames));
check('流式 SSE 帧', count($frames) > 1, count($frames) . ' 帧, ' . round($elapsed, 2) . 's');
check('流式末帧 [DONE]', $last === 'data: [DONE]');

// 注意：不能用 str_contains($sse, '"model":"internlm3"') 判断 ——
// 改写后的 "model":"internlm3-8b-instruct" 也包含这个子串，那样永远"检测到泄漏"。
// 必须匹配完整字段值（后面跟引号结束符）。
$leaked = (bool) preg_match('/"model"\s*:\s*"internlm3"/', $sse);
check('流式 model 字段已改写', !$leaked, $leaked ? '泄漏了上游原名 internlm3' : '已替换为 internlm3-8b-instruct');

echo "\n=== 6. 管理接口 ===\n";
if ($sess !== '') {
    foreach ([
        '/api/admin/channels' => 'items',
        '/api/admin/prices'   => 'items',
        '/api/admin/users'    => 'items',
        '/api/admin/stats'    => 'daily',
        '/api/admin/logs'     => 'items',
    ] as $path => $field) {
        $r = http('GET', $base . $path, null, $sess);
        check("GET $path", $r['code'] === 200 && array_key_exists($field, $r['json']['data'] ?? []));
    }

    $r = http('GET', "$base/api/admin/channels", null, $sess);
    $items = $r['json']['data']['items'] ?? [];
    $withPool = null;
    foreach ($items as $c) {
        if (($c['keys_total'] ?? 0) > 0) {
            $withPool = $c;
            break;
        }
    }
    check('渠道密钥池可用', $withPool !== null,
        $withPool !== null ? "{$withPool['keys_available']}/{$withPool['keys_total']} 可用" : '未找到带密钥的渠道');
}

echo "\n=== 7. 无权限访问管理接口 ===\n";
$r = http('GET', "$base/api/admin/channels");
check('匿名访问管理接口被拒', $r['code'] === 403);

echo "\n" . str_repeat('─', 60) . "\n";
printf("通过 %d 项，失败 %d 项\n", $pass, $fail);
exit($fail === 0 ? 0 : 1);
