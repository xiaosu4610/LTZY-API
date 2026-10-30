<?php
declare(strict_types=1);

/**
 * 服务入口。
 *
 * 用法：
 *   php bin/server.php
 *   php bin/server.php --port=3001
 */

// PSR-4 自动加载（由 composer dump-autoload 生成；若还没有 vendor 则退回手写加载）
$autoload = __DIR__ . '/../vendor/autoload.php';
if (is_file($autoload)) {
    require $autoload;
} else {
    spl_autoload_register(static function (string $class): void {
        $prefix = 'Ltzy\\';
        if (!str_starts_with($class, $prefix)) {
            return;
        }
        $rel = str_replace('\\', '/', substr($class, strlen($prefix)));
        $file = __DIR__ . '/../app/' . $rel . '.php';
        if (is_file($file)) {
            require $file;
        }
    });
}

// 常量类 Env 不在 autoload 覆盖范围内也能用，这里显式 require 一次更保险
if (!class_exists(\Ltzy\Support\Env::class)) {
    require __DIR__ . '/../app/Support/Env.php';
}

use Ltzy\Server;
use Ltzy\Support\Env;

// 先加载 .env，再读 config —— 顺序不能反。
// 安装向导把数据库连接写进 .env，config/*.php 全是 getenv() 读的，
// 如果先 require config 再 load .env，读到的永远是文件里的默认值。
// 注意 Env 内部「已有环境变量优先」，所以 systemd / docker 注入的值不会被覆盖。
Env::load(__DIR__ . '/../.env');

$cfg = require __DIR__ . '/../config/app.php';
$dbCfg = require __DIR__ . '/../config/database.php';

// 命令行覆盖
foreach ($argv as $arg) {
    if (preg_match('/^--port=(\d+)$/', $arg, $m)) {
        $cfg['port'] = (int) $m[1];
    } elseif (preg_match('/^--host=(.+)$/', $arg, $m)) {
        $cfg['host'] = $m[1];
    } elseif ($arg === '--debug') {
        $cfg['log_level'] = 'debug';
    }
}

$server = new Server($cfg, $dbCfg);
$server->run();
