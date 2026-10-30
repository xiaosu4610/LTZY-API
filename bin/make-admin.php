<?php
declare(strict_types=1);

/**
 * 开发辅助：创建管理员账户。
 *
 * 用法：php bin/make-admin.php [用户名] [密码]
 * 默认：admin / admin12345
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
$dsn = sprintf('pgsql:host=%s;port=%d;dbname=%s', $dbCfg['host'], $dbCfg['port'], $dbCfg['database']);

$pdo = new PDO($dsn, $dbCfg['username'], $dbCfg['password'], [
    PDO::ATTR_ERRMODE            => PDO::ERRMODE_EXCEPTION,
    PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC,
]);

$username = $argv[1] ?? 'admin';
$password = $argv[2] ?? 'admin12345';
$hash = password_hash($password, PASSWORD_DEFAULT);

$pdo->prepare('DELETE FROM users WHERE username = ?')->execute([$username]);

$stmt = $pdo->prepare('
    INSERT INTO users (username, password_hash, display_name, role, grp, quota)
    VALUES (?, ?, ?, 100, ?, 999999999)
');
$stmt->execute([$username, $hash, '管理员', 'default']);

printf("已创建管理员：%s / %s (role=100, group=default)\n", $username, $password);
