<?php
declare(strict_types=1);

/**
 * 迁移执行器。
 *
 * 用法：
 *   php bin/migrate.php            执行未应用的迁移
 *   php bin/migrate.php --status   查看状态
 *
 * 为什么不用框架的迁移工具：只有几个 SQL 文件，
 * 一张 schema_migrations 表记录已执行的版本就够了，引入依赖不划算。
 */

$autoload = __DIR__ . '/../vendor/autoload.php';
if (is_file($autoload)) {
    require $autoload;
} else {
    spl_autoload_register(static function (string $c): void {
        if (!str_starts_with($c, 'Ltzy\\')) {
            return;
        }
        $f = __DIR__ . '/../app/' . str_replace('\\', '/', substr($c, 5)) . '.php';
        if (is_file($f)) {
            require $f;
        }
    });
}

$dbCfg = require __DIR__ . '/../config/database.php';

$dsn = sprintf(
    'pgsql:host=%s;port=%d;dbname=%s',
    $dbCfg['host'],
    $dbCfg['port'],
    $dbCfg['database']
);

try {
    $pdo = new PDO($dsn, $dbCfg['username'], $dbCfg['password'], [
        PDO::ATTR_ERRMODE            => PDO::ERRMODE_EXCEPTION,
        PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC,
    ]);
} catch (PDOException $e) {
    fwrite(STDERR, "连接数据库失败: " . $e->getMessage() . "\n");
    fwrite(STDERR, sprintf("  DSN: %s\n  用户: %s\n", $dsn, $dbCfg['username']));
    exit(1);
}

// 确保版本表存在（第一条迁移里也有，但这里要先建才能查）
$pdo->exec('CREATE TABLE IF NOT EXISTS schema_migrations (
    version VARCHAR(64) PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)');

$applied = $pdo->query('SELECT version FROM schema_migrations ORDER BY version')
    ->fetchAll(PDO::FETCH_COLUMN);
$appliedSet = array_flip($applied);

$dir = __DIR__ . '/../migrations';
$files = glob($dir . '/*.sql') ?: [];
sort($files);

$statusOnly = in_array('--status', $argv, true);

echo "=== 迁移文件 ===\n";
$pending = [];
foreach ($files as $f) {
    $ver = basename($f, '.sql');
    $isApplied = isset($appliedSet[$ver]);
    printf("  %-24s %s\n", $ver, $isApplied ? '已应用' : '待应用');
    if (!$isApplied) {
        $pending[] = $f;
    }
}

if ($statusOnly) {
    echo "\n共 " . count($files) . " 个，待应用 " . count($pending) . " 个\n";
    exit(0);
}

if ($pending === []) {
    echo "\n没有待应用的迁移。\n";
    exit(0);
}

echo "\n=== 执行 ===\n";
foreach ($pending as $f) {
    $ver = basename($f, '.sql');
    $sql = file_get_contents($f);
    if ($sql === false || trim($sql) === '') {
        fwrite(STDERR, "  跳过空文件: {$ver}\n");
        continue;
    }

    echo "  → {$ver} ... ";
    $t0 = microtime(true);

    try {
        // 整个文件作为一个事务执行 —— 迁移要么全成功要么全回滚，
        // 不允许出现"应用了一半"的状态（那种半成品最难修）。
        $pdo->beginTransaction();
        $pdo->exec($sql);
        $stmt = $pdo->prepare('INSERT INTO schema_migrations (version) VALUES (?)');
        $stmt->execute([$ver]);
        $pdo->commit();
        printf("OK (%.0f ms)\n", (microtime(true) - $t0) * 1000);
    } catch (PDOException $e) {
        $pdo->rollBack();
        fwrite(STDERR, "失败\n  " . $e->getMessage() . "\n");
        exit(1);
    }
}

echo "\n完成。\n";
