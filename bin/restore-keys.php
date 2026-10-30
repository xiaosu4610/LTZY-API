<?php
declare(strict_types=1);

/**
 * 开发辅助：把渠道密钥从 .secrets/channels-with-keys.json 同步回数据库。
 *
 * 用法：php bin/restore-keys.php [渠道ID]
 * 不传 ID 就恢复全部渠道。
 *
 * 为什么需要这个脚本：调试密钥冷却/重试逻辑时会把密钥改坏，
 * 用 psql 传中文/长串容易碰到编码问题，走 PHP 最稳。
 */

$autoload = __DIR__ . '/../vendor/autoload.php';
if (is_file($autoload)) {
    require $autoload;
}

$dbCfg = require __DIR__ . '/../config/database.php';
$dsn = sprintf('pgsql:host=%s;port=%d;dbname=%s', $dbCfg['host'], $dbCfg['port'], $dbCfg['database']);

$pdo = new PDO($dsn, $dbCfg['username'], $dbCfg['password'], [
    PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
]);

$raw = file_get_contents(__DIR__ . '/../.secrets/channels-with-keys.json');
if ($raw === false) {
    fwrite(STDERR, "找不到 .secrets/channels-with-keys.json\n");
    exit(1);
}
$data = json_decode($raw, true);
if (!is_array($data) || !isset($data['channels'])) {
    fwrite(STDERR, "渠道配置文件格式错误\n");
    exit(1);
}

$onlyId = isset($argv[1]) ? (int) $argv[1] : 0;

$stmt = $pdo->prepare('UPDATE channels SET "key" = ?, updated_at = now() WHERE id = ?');
$count = 0;
foreach ($data['channels'] as $c) {
    $id = (int) $c['id'];
    if ($onlyId > 0 && $id !== $onlyId) {
        continue;
    }
    $keys = $c['keys'] ?? [];
    $stmt->execute([implode("\n", $keys), $id]);
    $count++;
    printf("  渠道 %-3d %-30s %d 把密钥\n", $id, $c['name'], count($keys));
}

printf("\n共恢复 %d 个渠道的密钥。\n", $count);
printf("提示：服务端有 60 秒渠道缓存，改完可以重启进程或等缓存过期。\n");
