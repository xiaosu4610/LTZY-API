<?php
declare(strict_types=1);

/**
 * 从本地 CSV 导入旧生产系统的有余额用户，标记为 special。
 *
 * 用法：
 *   php bin/import-legacy-users.php --dry-run
 *   php bin/import-legacy-users.php
 *
 * ══════════════════════════════════════════════════════════════
 * ⚠️ 导入的账号 account_type = 'special'
 *
 *    special 的生命周期规则（见 migrations/008_account_lifecycle.sql）：
 *      · 余额 > 0   → 正常使用
 *      · 余额 <= 0  → **物理删除**（user / tokens / 模型授权 / 日志全部清空）
 *
 *    这是产品决策，不是 bug。不要为了"保留数据"而改成软删除或打标记。
 *    执行删除的代码在 bin/purge-expired-accounts.php。
 *
 *    手工创建的经销商是 managed 类型，**永远不参与自动删除**。
 *    由管理员在后台显式操作才能删除。
 * ══════════════════════════════════════════════════════════════
 *
 * ── 数据来源 ──
 *
 * .secrets/legacy/users.csv
 *   从旧版系统只读导出的用户数据。
 *   列：id, username, display_name, password(bcrypt), email, quota, used_quota, request_count
 *
 * ── 登录方式 ──
 *
 * 导入的用户可以：
 *   · 用原 username 登录（可能是邮箱，也可能是普通名字）
 *   · 用原 email 登录（若 email 是邮箱格式）
 * 密码沿用旧系统的 bcrypt 哈希，用户用原密码即可，不需要重置。
 */

$dryRun = in_array('--dry-run', $argv, true);
$csvFile = __DIR__ . '/../.secrets/legacy/users.csv';

if (!is_file($csvFile)) {
    fwrite(STDERR, "找不到 {$csvFile}\n");
    fwrite(STDERR, "先用 plink 从生产导出：\n");
    fwrite(STDERR, "  sqlite3 -readonly -csv DB \"SELECT ...\" > users.csv\n");
    exit(1);
}

$dbCfg = require __DIR__ . '/../config/database.php';
$dsn = sprintf('pgsql:host=%s;port=%d;dbname=%s', $dbCfg['host'], $dbCfg['port'], $dbCfg['database']);
$pdo = new PDO($dsn, $dbCfg['username'], $dbCfg['password'], [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);

// ── 解析 CSV ──
$fh = fopen($csvFile, 'r');
if ($fh === false) {
    fwrite(STDERR, "无法打开 CSV\n");
    exit(1);
}

$rows = [];
while (($r = fgetcsv($fh, 0, ',', '"', '')) !== false) {
    if (count($r) < 8) {
        continue;
    }
    $rows[] = [
        'id'            => (int) $r[0],
        'username'      => (string) $r[1],
        'display_name'  => (string) $r[2],
        'password'      => (string) $r[3],
        'email'         => (string) $r[4],
        'quota'         => (int) $r[5],
        'used_quota'    => (int) $r[6],
        'request_count' => (int) $r[7],
    ];
}
fclose($fh);

printf("CSV 解析出 %d 个用户\n\n", count($rows));

// ── 写库 ──
$inserted = 0;
$updated = 0;
$skipped = 0;
$skipReasons = [];

if (!$dryRun) {
    $pdo->beginTransaction();
}

$findByUser = $pdo->prepare('SELECT id, account_type FROM users WHERE username = ?');
$findByEmail = $pdo->prepare('SELECT id FROM users WHERE email = ? AND email <> \'\' LIMIT 1');
$upd = $pdo->prepare('
    UPDATE users
    SET password_hash = ?, display_name = ?, email = ?,
        quota = ?, used_quota = ?, request_count = ?,
        account_type = \'special\', status = 1
    WHERE id = ?
');
$ins = $pdo->prepare('
    INSERT INTO users (username, password_hash, display_name, email, role, status,
                       grp, quota, used_quota, request_count, account_type)
    VALUES (?, ?, ?, ?, 1, 1, \'default\', ?, ?, ?, \'special\')
');

try {
    foreach ($rows as $r) {
        $findByUser->execute([$r['username']]);
        $existing = $findByUser->fetch(PDO::FETCH_ASSOC);

        if ($existing !== false) {
            // 已经是手工账号 → 绝不覆盖
            if ($existing['account_type'] === 'managed') {
                $skipped++;
                $skipReasons[] = "{$r['username']}（已是 managed，不覆盖）";
                continue;
            }
            if (!$dryRun) {
                $upd->execute([
                    $r['password'], $r['display_name'], $r['email'],
                    $r['quota'], $r['used_quota'], $r['request_count'],
                    $existing['id'],
                ]);
            }
            $updated++;
            continue;
        }

        // email 被占用 → 跳过（避免登录歧义）
        if ($r['email'] !== '') {
            $findByEmail->execute([$r['email']]);
            if ($findByEmail->fetchColumn() !== false) {
                $skipped++;
                $skipReasons[] = "{$r['username']}（email 已被占用）";
                continue;
            }
        }

        if (!$dryRun) {
            $ins->execute([
                $r['username'], $r['password'], $r['display_name'], $r['email'],
                $r['quota'], $r['used_quota'], $r['request_count'],
            ]);
        }
        $inserted++;
    }

    if (!$dryRun) {
        $pdo->commit();
    }
} catch (Throwable $e) {
    if (!$dryRun) {
        $pdo->rollBack();
    }
    fwrite(STDERR, "写库失败: " . $e->getMessage() . "\n");
    exit(1);
}

printf("%s新增 %d，更新 %d，跳过 %d\n", $dryRun ? '（dry-run）' : '', $inserted, $updated, $skipped);

if ($skipReasons !== []) {
    echo "\n跳过的原因（前 10 条）：\n";
    foreach (array_slice($skipReasons, 0, 10) as $r) {
        echo "  - {$r}\n";
    }
}

// ── 统计 ──
echo "\n=== 账号类型分布 ===\n";
$stats = $pdo->query("
    SELECT account_type, COUNT(*) AS n, ROUND(SUM(quota)/1000000.0, 2) AS yuan
    FROM users WHERE role < 10 GROUP BY account_type ORDER BY account_type
")->fetchAll(PDO::FETCH_ASSOC);

foreach ($stats as $s) {
    printf("  %-10s %4d 个，余额合计 %.2f 元\n", $s['account_type'], (int) $s['n'], (float) $s['yuan']);
}

$emailLogin = (int) $pdo->query("
    SELECT COUNT(*) FROM users
    WHERE account_type = 'special' AND (username LIKE '%@%' OR (email LIKE '%@%' AND email <> ''))
")->fetchColumn();
printf("\n%d 个 special 账号可用邮箱登录\n", $emailLogin);
