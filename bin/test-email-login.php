<?php
declare(strict_types=1);

/**
 * 测试邮箱登录。
 *
 * 用法：php bin/test-email-login.php
 *
 * 造一个 special 账号，用 email 登录，验证：
 *   1. 用 email 能登录成功
 *   2. 用 username 也能登录成功（如果 username 不是邮箱）
 *   3. 登录后的身份正确
 */

$dbCfg = require __DIR__ . '/../config/database.php';
$dsn = sprintf('pgsql:host=%s;port=%d;dbname=%s', $dbCfg['host'], $dbCfg['port'], $dbCfg['database']);
$pdo = new PDO($dsn, $dbCfg['username'], $dbCfg['password'], [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);

$base = 'http://127.0.0.1:3000';

function post(string $url, array $body): array
{
    $ch = curl_init($url);
    curl_setopt_array($ch, [
        CURLOPT_POST => true,
        CURLOPT_POSTFIELDS => json_encode($body, JSON_UNESCAPED_UNICODE),
        CURLOPT_HTTPHEADER => ['Content-Type: application/json'],
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT => 30,
    ]);
    $raw = (string) curl_exec($ch);
    $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
    return ['code' => $code, 'json' => json_decode($raw, true)];
}

// ── 造测试账号 ──
$username = 'emailtest_user';
$email = 'emailtest@qq.com';
$password = 'TestMail12345';
$hash = password_hash($password, PASSWORD_BCRYPT, ['cost' => 12]);

$pdo->prepare("DELETE FROM users WHERE username = ? OR email = ?")->execute([$username, $email]);
$pdo->prepare("
    INSERT INTO users (username, password_hash, display_name, email, role, status, grp, quota, account_type)
    VALUES (?, ?, ?, ?, 1, 1, 'default', 10000000, 'special')
")->execute([$username, $hash, '测试邮箱账号', $email]);

$id = (int) $pdo->lastInsertId();
printf("已创建测试账号：id=%d, username=%s, email=%s, 余额 10 元\n\n", $id, $username, $email);

$pass = 0;
$fail = 0;

function check(string $name, bool $ok, string $detail = ''): void
{
    global $pass, $fail;
    if ($ok) {
        $pass++;
        printf("  ✓ %-40s %s\n", $name, $detail);
    } else {
        $fail++;
        printf("  ✗ %-40s %s\n", $name, $detail);
    }
}

echo "═══ 登录测试 ═══\n";

// 1. 用 email 登录
$r = post($base . '/api/login', ['username' => $email, 'password' => $password]);
check('用 email 登录', $r['code'] === 200 && ($r['json']['success'] ?? false), "HTTP {$r['code']}");

// ⚠️ 注意：每次登录都会刷新 access_token，旧 token 立即失效。
//    所以后续要用「最后一次登录」拿到的 token，不能混用。
$tok = $r['json']['data']['access_token'] ?? '';

// 2. 用 username 登录（这一步会让上面的 token 失效）
$r2 = post($base . '/api/login', ['username' => $username, 'password' => $password]);
check('用 username 登录', $r2['code'] === 200 && ($r2['json']['success'] ?? false), "HTTP {$r2['code']}");

// 3. 错误密码
$r3 = post($base . '/api/login', ['username' => $email, 'password' => 'wrong-' . bin2hex(random_bytes(4))]);
check('错误密码被拒', $r3['code'] === 401, "HTTP {$r3['code']}");

// 4. 重新登录拿有效 token（前面的都被刷掉了）
$r4 = post($base . '/api/login', ['username' => $email, 'password' => $password]);
$tok = $r4['json']['data']['access_token'] ?? '';

// 5. 登录后身份正确
if ($tok !== '') {
    $ch = curl_init($base . '/api/me');
    curl_setopt_array($ch, [
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT => 20,
        CURLOPT_HTTPHEADER => ['Authorization: Bearer ' . $tok],
    ]);
    $me = json_decode((string) curl_exec($ch), true);
    check('登录后身份正确', ($me['data']['user']['username'] ?? '') === $username,
        $me['data']['user']['username'] ?? '?');
    check('余额正确', (float) ($me['data']['user']['quota_yuan'] ?? 0) === 10.0,
        ($me['data']['user']['quota_yuan'] ?? '?') . ' 元');
    check('不是管理员', ($me['data']['is_manager'] ?? true) === false);

    // 6. 能用 API 调用
    $ch = curl_init($base . '/v1/chat/completions');
    curl_setopt_array($ch, [
        CURLOPT_POST => true,
        CURLOPT_POSTFIELDS => json_encode([
            'model' => 'internlm3-8b-instruct',
            'messages' => [['role' => 'user', 'content' => 'hi']],
            'max_tokens' => 3,
        ]),
        CURLOPT_HTTPHEADER => ['Content-Type: application/json', 'Authorization: Bearer ' . $tok],
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT => 90,
    ]);
    $raw = (string) curl_exec($ch);
    $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
    check('登录后可调用模型', $code === 200, "HTTP {$code}");
}

// ── 清理 ──
$pdo->prepare('DELETE FROM tokens WHERE user_id = ?')->execute([$id]);
$pdo->prepare('DELETE FROM account_models WHERE user_id = ?')->execute([$id]);
$pdo->prepare('DELETE FROM users WHERE id = ?')->execute([$id]);
printf("\n已清理测试账号\n");

echo "\n" . str_repeat('─', 60) . "\n";
printf("通过 %d 项，失败 %d 项\n", $pass, $fail);
exit($fail === 0 ? 0 : 1);
