<?php
declare(strict_types=1);

/**
 * 导出所有有余额账号的清单。
 *
 * 用法：
 *   php bin/list-balances.php             打印到终端
 *   php bin/list-balances.php --csv       同时写 docs/余额清单.csv
 *
 * 为什么单独一个脚本：余额是要定期对账的东西，
 * 「谁还有钱、还剩多少」这个问题每次都要回答，不该每次手搓 SQL。
 *
 * 单位：库里 quota 是「分」，1 元 = 1,000,000（不是 100）。
 * 这是沿用旧系统的约定，前端 quota_per_unit 也是 1e6。
 */

$dbCfg = require __DIR__ . '/../config/database.php';
$dsn = sprintf('pgsql:host=%s;port=%d;dbname=%s', $dbCfg['host'], $dbCfg['port'], $dbCfg['database']);
$pdo = new PDO($dsn, $dbCfg['username'], $dbCfg['password'], [
    PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
]);

$rows = $pdo->query("
    SELECT id, username, display_name, account_type, quota, used_quota,
           request_count, last_login_at
    FROM users
    WHERE quota > 0
    ORDER BY quota DESC, id ASC
")->fetchAll(PDO::FETCH_ASSOC);

$total = 0;
foreach ($rows as $r) {
    $total += (int) $r['quota'];
}

printf("有余额账号：%d 个，余额合计 %.2f 元\n", count($rows), $total / 1_000_000);
printf("（另有 %d 个账号余额为 0，不在清单内）\n\n",
    (int) $pdo->query("SELECT count(*) FROM users WHERE quota <= 0")->fetchColumn());

// ── 区间分布 ──
$bands = [
    ['≥100 元',   100_000_000, PHP_INT_MAX],
    ['50–100 元',  50_000_000, 100_000_000],
    ['20–50 元',   20_000_000,  50_000_000],
    ['10–20 元',   10_000_000,  20_000_000],
    ['5–10 元',     5_000_000,  10_000_000],
    ['1–5 元',      1_000_000,   5_000_000],
    ['0.1–1 元',      100_000,   1_000_000],
    ['＜0.1 元',            0,     100_000],
];

echo "═══ 区间分布 ═══\n";
foreach ($bands as [$label, $lo, $hi]) {
    $n = 0;
    $sum = 0;
    foreach ($rows as $r) {
        $q = (int) $r['quota'];
        if ($q >= $lo && $q < $hi) {
            $n++;
            $sum += $q;
        }
    }
    if ($n > 0) {
        printf("  %-11s %3d 个   %9.2f 元\n", $label, $n, $sum / 1_000_000);
    }
}

// ── 明细 ──
echo "\n═══ 明细（按余额降序）═══\n";
printf("%4s  %-28s %-14s %10s %10s %8s  %s\n",
    'id', '账号', '显示名', '余额(元)', '已用(元)', '请求数', '末次登录');

foreach ($rows as $i => $r) {
    $name = (string) $r['username'];
    $disp = (string) $r['display_name'];
    // 显示名和账号一样就不重复占位，让表格窄一点好读
    $showDisp = ($disp !== '' && $disp !== $name) ? $disp : '—';
    $last = $r['last_login_at'] !== null
        ? substr((string) $r['last_login_at'], 0, 10)
        : '—';

    printf("%4d  %-28s %-14s %10.2f %10.2f %8d  %s\n",
        (int) $r['id'],
        mb_strimwidth($name, 0, 28, '..'),
        mb_strimwidth($showDisp, 0, 14, '..'),
        (int) $r['quota'] / 1_000_000,
        (int) $r['used_quota'] / 1_000_000,
        (int) $r['request_count'],
        $last
    );
}

// ── 可选导出 CSV ──
if (in_array('--csv', $argv, true)) {
    $path = __DIR__ . '/../docs/余额清单.csv';
    $fp = fopen($path, 'wb');
    // UTF-8 BOM：不加的话 Excel 会把中文显示成乱码
    fwrite($fp, "\xEF\xBB\xBF");
    // fputcsv 的后三个参数（分隔符/引号/转义）在 PHP 8.4 起必须显式传，
    // 否则每写一行就吐一条 Deprecated，268 行直接把输出刷满。
    fputcsv($fp, ['排名', 'id', '账号', '显示名', '类型', '余额(元)', '已用(元)', '请求数', '末次登录'], ',', '"', '');
    foreach ($rows as $i => $r) {
        fputcsv($fp, [
            $i + 1,
            $r['id'],
            $r['username'],
            $r['display_name'],
            $r['account_type'] === 'managed' ? '经销商' : '普通',
            number_format((int) $r['quota'] / 1_000_000, 2, '.', ''),
            number_format((int) $r['used_quota'] / 1_000_000, 2, '.', ''),
            $r['request_count'],
            $r['last_login_at'] ?? '',
        ], ',', '"', '');
    }
    fclose($fp);
    printf("\n已导出：%s\n", realpath($path));
}
