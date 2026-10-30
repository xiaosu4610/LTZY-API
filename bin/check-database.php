<?php
declare(strict_types=1);

/**
 * 数据库健康检查。
 *
 * 用法：php bin/check-database.php
 *
 * 检查维度：
 *   1. 表结构完整性
 *   2. 数据一致性（孤儿数据、悬空引用）
 *   3. 账号数据质量（余额、邮箱、密码哈希）
 *   4. 索引与分区
 *   5. 序列状态
 */

$dbCfg = require __DIR__ . '/../config/database.php';
$dsn = sprintf('pgsql:host=%s;port=%d;dbname=%s', $dbCfg['host'], $dbCfg['port'], $dbCfg['database']);
$pdo = new PDO($dsn, $dbCfg['username'], $dbCfg['password'], [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);

$pass = 0;
$fail = 0;
$warn = 0;

function check(string $name, bool $ok, string $detail = '', bool $isWarn = false): void
{
    global $pass, $fail, $warn;
    if ($ok) {
        $pass++;
        printf("  ✓ %-44s %s\n", $name, $detail);
    } elseif ($isWarn) {
        $warn++;
        printf("  ~ %-44s %s\n", $name, $detail);
    } else {
        $fail++;
        printf("  ✗ %-44s %s\n", $name, $detail);
    }
}

$q = static fn(string $sql) => $pdo->query($sql)->fetchAll(PDO::FETCH_ASSOC);
$one = static fn(string $sql) => $pdo->query($sql)->fetchColumn();

/* ══════════ 1. 表结构 ══════════ */
echo "═══ 1. 表结构 ═══\n";

$expected = ['channels', 'abilities', 'model_prices', 'group_ratios', 'users',
             'tokens', 'logs', 'options', 'schema_migrations',
             'account_models', 'recharge_orders'];

// 分区子表（logs_2026_10 之类）在 pg_class 里也是独立行，但它们不是
// 「多余的表」—— 是 logs 按时间自动挂上去的分区。
//
// relkind 要同时收 'r'（普通表）和 'p'（分区父表）：
//   logs 建的是 PARTITION BY RANGE，它的父表 relkind 是 'p' 而不是 'r'，
//   只查 'r' 会把 logs 本尊判成「缺失」。
// 排除条件只看 inhrelid（谁是谁的子表），父表 logs 是 inhparent，不会被误排。
$actual = array_column($q("
    SELECT c.relname AS tablename
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
      AND NOT EXISTS (
          SELECT 1 FROM pg_inherits i WHERE i.inhrelid = c.oid
      )
    ORDER BY c.relname
"), 'tablename');

foreach ($expected as $t) {
    check("表 {$t}", in_array($t, $actual, true));
}

$extra = array_diff($actual, $expected);
if ($extra !== []) {
    check('无多余表', false, implode(', ', $extra), true);
}

// 关键字段
$cols = array_column($q("
    SELECT column_name FROM information_schema.columns
    WHERE table_name = 'users' AND table_schema = 'public'
"), 'column_name');

foreach (['account_type', 'quota', 'used_quota', 'email', 'password_hash'] as $c) {
    check("users.{$c}", in_array($c, $cols, true));
}

/* ══════════ 2. 数据一致性 ══════════ */
echo "\n═══ 2. 数据一致性 ═══\n";

// 孤儿令牌
$orphanTokens = (int) $one("
    SELECT COUNT(*) FROM tokens t
    LEFT JOIN users u ON u.id = t.user_id
    WHERE u.id IS NULL
");
check('无孤儿令牌', $orphanTokens === 0, "{$orphanTokens} 条");

// 孤儿模型授权
$orphanModels = (int) $one("
    SELECT COUNT(*) FROM account_models am
    LEFT JOIN users u ON u.id = am.user_id
    WHERE u.id IS NULL
");
check('无孤儿模型授权', $orphanModels === 0, "{$orphanModels} 条");

// 孤儿日志（user_id 必须存在，除非是 0 表示匿名）
$orphanLogs = (int) $one("
    SELECT COUNT(*) FROM logs l
    LEFT JOIN users u ON u.id = l.user_id
    WHERE l.user_id > 0 AND u.id IS NULL
");
check('无孤儿日志', $orphanLogs === 0, "{$orphanLogs} 条");

// abilities 指向不存在的渠道
$orphanAbilities = (int) $one("
    SELECT COUNT(*) FROM abilities a
    LEFT JOIN channels c ON c.id = a.channel_id
    WHERE c.id IS NULL
");
check('abilities 无悬空渠道', $orphanAbilities === 0, "{$orphanAbilities} 条");

// 渠道引用了不存在的模型价格（不致命，记警告）
$noPrice = (int) $one("
    SELECT COUNT(DISTINCT a.model) FROM abilities a
    WHERE a.enabled = 1
      AND NOT EXISTS (SELECT 1 FROM model_prices p WHERE p.model = a.model)
");
check('所有模型都有价格记录', $noPrice === 0, "{$noPrice} 个模型缺价格", true);

/* ══════════ 3. 账号数据质量 ══════════ */
echo "\n═══ 3. 账号数据质量 ═══\n";

$total = (int) $one('SELECT COUNT(*) FROM users');
printf("  总数 %d\n", $total);

// 账号类型分布
foreach ($q("SELECT account_type, COUNT(*) AS n FROM users GROUP BY account_type ORDER BY account_type") as $r) {
    printf("    %-10s %d\n", $r['account_type'], (int) $r['n']);
}

// 密码哈希格式
$badHash = (int) $one("
    SELECT COUNT(*) FROM users
    WHERE password_hash !~ '^\\\$2[aby]\\\$'
");
check('密码哈希格式正确（bcrypt）', $badHash === 0, "{$badHash} 个异常");

// 余额为负
$negQuota = (int) $one('SELECT COUNT(*) FROM users WHERE quota < 0');
check('无负余额账号', $negQuota === 0, "{$negQuota} 个", true);

// 余额为 0 的账号：这是正常状态，不再是「待清理」
// （2026-10-29 起废弃「余额耗尽自动删除」，见 migrations/012_drop_purge.sql）
$zeroQuota = (int) $one("SELECT COUNT(*) FROM users WHERE role < 10 AND quota <= 0");
printf("    余额为 0 的普通账号: %d 个（正常，不会自动删）\n", $zeroQuota);

// 邮箱格式
$validEmail = (int) $one("
    SELECT COUNT(*) FROM users
    WHERE email ~ '^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$'
");
printf("    邮箱格式有效: %d 个\n", $validEmail);

// 用户名或邮箱可登录的 special 账号
$loginable = (int) $one("
    SELECT COUNT(*) FROM users
    WHERE account_type = 'special' AND quota > 0
      AND (username ~ '@' OR email ~ '@')
");
check('special 账号可邮箱登录', $loginable > 0, "{$loginable} 个");

// 重复邮箱
$dupEmail = $q("
    SELECT email, COUNT(*) AS n FROM users
    WHERE email <> '' GROUP BY email HAVING COUNT(*) > 1
");
check('邮箱唯一', $dupEmail === [], count($dupEmail) . ' 组重复');

// 重复用户名
$dupUser = $q("SELECT username, COUNT(*) AS n FROM users GROUP BY username HAVING COUNT(*) > 1");
check('用户名唯一', $dupUser === [], count($dupUser) . ' 组重复');

/* ══════════ 4. 分区与索引 ══════════ */
echo "\n═══ 4. 分区与索引 ═══\n";

$partitions = array_column($q("
    SELECT c.relname FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = 'logs'
    ORDER BY c.relname
"), 'relname');

printf("  logs 分区: %d 个\n", count($partitions));
foreach ($partitions as $p) {
    printf("    %s\n", $p);
}

check('未来有可用分区', count($partitions) >= 2, count($partitions) . ' 个');

// 关键索引：按「表 + 首列」查，而不是硬编码索引名。
// 索引名是建表时的产物（tokens_key_key 这种 PG 自动命名），写死名字会让
// 检查在库结构没变时误报失败，掩盖真正的回归。
$idxRows = $q("
    SELECT t.relname  AS tbl,
           a.attname  AS col,
           i.indisunique AS uniq,
           ic.relname AS idxname
    FROM pg_index i
    JOIN pg_class ic ON ic.oid = i.indexrelid
    JOIN pg_class t  ON t.oid  = i.indrelid
    JOIN pg_namespace n ON n.oid = t.relnamespace
    CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
    JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
    WHERE n.nspname = 'public' AND k.ord = 1
");

$hasIndex = static function (string $tbl, string $col, bool $needUnique = false) use ($idxRows): ?string {
    foreach ($idxRows as $r) {
        if ($r['tbl'] !== $tbl || $r['col'] !== $col) {
            continue;
        }
        if ($needUnique && $r['uniq'] !== true && $r['uniq'] !== 't') {
            continue;
        }
        return $r['idxname'];
    }
    return null;
};

foreach ([
    ['tokens',  'key',       true,  'tokens.key 唯一索引（令牌校验热路径）'],
    ['users',   'username',  true,  'users.username 唯一索引（登录用）'],
    ['users',   'email',     false, 'users.email 索引（邮箱登录用）'],
    ['users',   'account_type', false, 'users.account_type 索引（清理扫描用）'],
    ['abilities', 'model',   false, 'abilities.model 索引（模型路由用）'],
    ['account_models', 'user_id', false, 'account_models.user_id 索引'],
] as [$tbl, $col, $uniqOnly, $desc]) {
    $name = $hasIndex($tbl, $col, $uniqOnly);
    check($desc, $name !== null, $name ?? "{$tbl}.{$col} 缺失");
}

/* ══════════ 5. 序列 ══════════ */
echo "\n═══ 5. 序列状态 ══════════\n";

foreach ([
    'users_id_seq'    => 'users',
    'tokens_id_seq'   => 'tokens',
    'channels_id_seq' => 'channels',
    'abilities_id_seq' => 'abilities',
] as $seq => $table) {
    try {
        $last = (int) $one("SELECT last_value FROM {$seq}");
        $max = (int) $one("SELECT COALESCE(MAX(id), 0) FROM {$table}");
        check("{$seq}", $last >= $max, "last={$last} max={$max}");
    } catch (Throwable $e) {
        check("{$seq}", false, '序列不存在', true);
    }
}

/* ══════════ 6. 数据量总览 ══════════ */
echo "\n═══ 6. 数据量 ═══\n";

foreach ([
    'channels'      => '渠道',
    'abilities'     => '模型映射',
    'model_prices'  => '价格',
    'users'         => '账号',
    'tokens'        => '令牌',
    'account_models' => '模型授权',
    'logs'          => '调用日志',
    'recharge_orders' => '充值订单',
    'options'       => '配置项',
] as $t => $label) {
    printf("  %-14s %6d\n", $label, (int) $one("SELECT COUNT(*) FROM {$t}"));
}

/* ══════════ 结果 ══════════ */
echo "\n" . str_repeat('═', 62) . "\n";
printf("通过 %d 项，警告 %d 项，失败 %d 项\n", $pass, $warn, $fail);
exit($fail === 0 ? 0 : 1);
