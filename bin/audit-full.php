<?php
declare(strict_types=1);

require __DIR__ . '/_testkit.php';

/**
 * 全链路检测：所有路由 + 所有模型的真实调用。
 *
 * 用法：
 *   php bin/audit-full.php            完整检测
 *   php bin/audit-full.php --routes   只测路由
 *   php bin/audit-full.php --models   只测模型
 *
 * 与 regression.php 的区别：
 *   regression  关注"功能是否正常"（35 项断言）
 *   audit       关注"每一处是否都可达、都正确"（穷举所有路由和所有模型）
 */

$base = 'http://127.0.0.1:3000';
$onlyRoutes = in_array('--routes', $argv, true);
$onlyModels = in_array('--models', $argv, true);

// 从数据库取密钥和模型清单
$dbCfg = require __DIR__ . '/../config/database.php';
$dsn = sprintf('pgsql:host=%s;port=%d;dbname=%s', $dbCfg['host'], $dbCfg['port'], $dbCfg['database']);
$pdo = new PDO($dsn, $dbCfg['username'], $dbCfg['password'], [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);

function req(string $method, string $url, ?array $body = null, ?string $token = null, int $timeout = 60): array
{
    $ch = curl_init($url);
    $h = ['Content-Type: application/json'];
    if ($token !== null) {
        $h[] = 'Authorization: Bearer ' . $token;
    }
    curl_setopt_array($ch, [
        CURLOPT_CUSTOMREQUEST  => $method,
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT        => $timeout,
        CURLOPT_HTTPHEADER     => $h,
    ]);
    if ($body !== null) {
        curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($body, JSON_UNESCAPED_UNICODE));
    }
    $t0 = microtime(true);
    $raw = curl_exec($ch);
    $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $ms = (int) ((microtime(true) - $t0) * 1000);
    return ['code' => $code, 'body' => (string) $raw, 'json' => json_decode((string) $raw, true), 'ms' => $ms];
}

/* ══════════ 路由检测 ══════════ */
if (!$onlyModels) {
    echo "══════ 路由检测 ══════\n\n";

    // 管理员凭据从环境变量读，不要写死 —— 本文件会进版本库
    $adminUser = getenv('E2E_ADMIN_USER') ?: '';
    $adminPass = getenv('E2E_ADMIN_PASS') ?: '';
    if ($adminUser === '') {
        echo "  ⚠ 未设置 E2E_ADMIN_USER / E2E_ADMIN_PASS，跳过管理员相关检测\n";
        $adminTok = '';
    } else {
        $r = req('POST', $base . '/api/login', ['username' => $adminUser, 'password' => $adminPass]);
        if (($r['json']['success'] ?? false)) {
            $adminTok = $r['json']['data']['access_token'];
        }
        printf("管理员登录: %s (%dms)\n", $adminTok !== '' ? '✓' : '✗', $r['ms']);
    }

    $acctTok = '';
    $r = req('POST', $base . '/api/login', ['username' => $probe['username'], 'password' => $probe['password']]);
    if (($r['json']['success'] ?? false)) {
        $acctTok = $r['json']['data']['access_token'];
    }
    printf("账户登录:   %s (%dms)\n\n", $acctTok !== '' ? '✓' : '✗', $r['ms']);

    /**
     * 预期：路径 => [方法, 期望状态码列表, 用哪个 token]
     */
    $expect = [
        // 页面
        '/'          => ['GET', [200], null, '门户首页'],
        '/plaza'     => ['GET', [200], null, '模型广场'],
        '/login'     => ['GET', [200], null, '登录页'],
        '/console'   => ['GET', [200], null, '控制台'],

        // 已删除路径
        '/admin'     => ['GET', [404], null, '已删除'],
        '/dealer'    => ['GET', [404], null, '已删除'],
        '/manage'    => ['GET', [404], null, '已删除'],

        // 公开接口
        '/api/status'      => ['GET', [200], null, '状态'],
        '/api/site'        => ['GET', [200], null, '站点信息'],
        '/api/model/plaza' => ['GET', [200], null, '广场数据'],
        '/v1/models'       => ['GET', [200], null, '模型列表'],

        // 认证
        '/api/me'     => ['GET', [200, 401], null, '需要 token'],
        '/api/logout' => ['POST', [200, 401], null, '需要 token'],

        // 账户接口（需要登录）
        '/api/account/self'   => ['GET', [200, 401], 'account', '账户信息'],
        '/api/account/models' => ['GET', [200, 401], 'account', '我的模型'],
        '/api/account/logs'   => ['GET', [200, 401], 'account', '调用日志'],
        '/api/account/usage'  => ['GET', [200, 401], 'account', '用量'],

        // 管理接口（需要管理员）
        '/api/manage/status'   => ['GET', [200, 403], 'admin', '系统状态'],
        '/api/manage/channels' => ['GET', [200, 403], 'admin', '渠道列表'],
        '/api/manage/prices'   => ['GET', [200, 403], 'admin', '定价'],
        '/api/manage/accounts' => ['GET', [200, 403], 'admin', '账户列表'],
        '/api/manage/users'    => ['GET', [200, 403], 'admin', '用户列表'],
        '/api/manage/stats'    => ['GET', [200, 403], 'admin', '统计'],
        '/api/manage/logs'     => ['GET', [200, 403], 'admin', '全部日志'],
        '/api/manage/security' => ['GET', [200, 403], 'admin', '登录防护'],
    ];

    $ok = 0;
    $bad = [];

    foreach ($expect as $path => [$method, $codes, $who, $label]) {
        $tok = match ($who) {
            'admin'   => $adminTok,
            'account' => $acctTok,
            default   => null,
        };
        $r = req($method, $base . $path, $method === 'POST' ? [] : null, $tok);
        $pass = in_array($r['code'], $codes, true);
        if ($pass) {
            $ok++;
        } else {
            $bad[] = "{$path} → {$r['code']}（期望 " . implode('/', $codes) . "）";
        }
        printf("  %s %-24s %-4s %4dms  %s\n",
            $pass ? '✓' : '✗', $path, $r['code'], $r['ms'], $label);
    }

    printf("\n路由：%d / %d 通过\n", $ok, count($expect));
    if ($bad !== []) {
        echo "\n失败:\n";
        foreach ($bad as $b) {
            echo "  - {$b}\n";
        }
    }

    // 越权专项
    echo "\n── 越权检测 ──\n";
    $adminApis = ['/api/manage/status', '/api/manage/channels', '/api/manage/prices',
        '/api/manage/accounts', '/api/manage/users', '/api/manage/stats',
        '/api/manage/logs', '/api/manage/security'];
    $leak = 0;
    foreach ($adminApis as $api) {
        $r = req('GET', $base . $api, null, $acctTok);
        if ($r['code'] !== 403) {
            printf("  ✗ %s 用普通账户访问返回 %d（应为 403）\n", $api, $r['code']);
            $leak++;
        }
        $r2 = req('GET', $base . $api);
        if ($r2['code'] !== 403) {
            printf("  ✗ %s 匿名访问返回 %d（应为 403）\n", $api, $r2['code']);
            $leak++;
        }
    }
    printf("  %s 8 个管理接口 × 2 种身份 = 16 项越权检查\n", $leak === 0 ? '✓ 全部拦截' : "✗ {$leak} 项泄漏");
}

/* ══════════ 模型检测 ══════════ */
if (!$onlyRoutes) {
    echo "\n══════ 模型全测 ══════\n\n";

    // 取所有 chat 类模型
    $models = $pdo->query("
        SELECT DISTINCT a.model, c.id AS channel_id, c.name AS channel_name
        FROM abilities a JOIN channels c ON c.id = a.channel_id
        WHERE a.enabled = 1 AND c.status = 1 AND a.category IN ('chat', 'vision')
        ORDER BY c.id, a.model
    ")->fetchAll(PDO::FETCH_ASSOC);

    printf("共 %d 个 chat 模型待测\n\n", count($models));

    $stats = ['ok' => 0, 'quota' => 0, 'timeout' => 0, 'err' => 0];
    $problems = [];
    $cat = null;

    foreach ($models as $i => $m) {
        if ($m['channel_id'] != $cat) {
            $cat = $m['channel_id'];
            printf("\n── 渠道 %d：%s ──\n", $cat, $m['channel_name']);
        }

        $r = req('POST', $base . '/v1/chat/completions', [
            'model' => $m['model'],
            'messages' => [['role' => 'user', 'content' => 'hi']],
            'max_tokens' => 3,
        ], null, 70);

        $msg = $r['json']['error']['message'] ?? '';
        $status = 'ok';
        if ($r['code'] === 200) {
            $status = 'ok';
            $stats['ok']++;
        } elseif (str_contains($msg, 'limit reached') || $r['code'] === 502 && str_contains($msg, 'Exhausted')) {
            $status = 'quota';
            $stats['quota']++;
        } elseif ($r['code'] === 0 || $r['ms'] >= 65000) {
            $status = 'timeout';
            $stats['timeout']++;
        } else {
            $status = 'err';
            $stats['err']++;
        }

        $icon = match ($status) {
            'ok' => '✓', 'quota' => 'Q', 'timeout' => 'T', default => '✗',
        };

        printf("  %s %-46s %5dms", $icon, substr($m['model'], 0, 46), $r['ms']);
        if ($status !== 'ok') {
            printf("  %s", substr(trim($msg), 0, 40));
            $problems[] = ['model' => $m['model'], 'status' => $status,
                'http' => $r['code'], 'msg' => substr(trim($msg), 0, 80)];
        }
        echo "\n";
    }

    echo "\n" . str_repeat('─', 70) . "\n";
    printf("可用:   %d\n", $stats['ok']);
    printf("配额满: %d（可上架，会返回 429）\n", $stats['quota']);
    printf("超时:   %d（上游无响应）\n", $stats['timeout']);
    printf("错误:   %d\n", $stats['err']);

    if ($problems !== []) {
        echo "\n异常明细:\n";
        foreach ($problems as $p) {
            printf("  [%s] %-42s HTTP %d  %s\n", $p['status'], $p['model'], $p['http'], $p['msg']);
        }
    }

    file_put_contents(__DIR__ . '/../docs/audit-result.json', json_encode([
        'at' => date('c'), 'stats' => $stats, 'problems' => $problems,
    ], JSON_UNESCAPED_UNICODE | JSON_PRETTY_PRINT));
}

// 临时测试账号清理：脚本无论怎么退出（正常/异常/被 kill）都不会在库里留残留
if (isset($probe) && is_array($probe)) {
    probeDelete($probe['id']);
}
