<?php
declare(strict_types=1);

namespace Ltzy\Install;

use PDO;
use Throwable;

/**
 * 安装器。
 *
 * 一次性的初始化流程：检测环境 → 连数据库 → 建表 → 写配置 → 建管理员 → 上锁。
 *
 * ── 为什么用「锁文件」而不是查数据库判断 ──
 *
 * 锁文件在**连数据库之前**就能判断。如果靠查库，数据库没配好时连不上，
 * 就没法区分「还没装」和「装好了但库挂了」—— 前者应该进安装页，
 * 后者应该报错，混在一起会把用户带到错误的页面。
 *
 * 锁文件放在 `var/` 下，`var/` 已经在 .gitignore 里，不会被提交。
 *
 * ── 装完之后 ──
 *
 * 写锁文件之后，Router 会在入口处把 `/install` 一律 404 掉。
 * 同时所有需要数据库的页面才能正常访问 —— 在此之前它们都在重定向。
 *
 * ── 关于数据库类型 ──
 *
 * **只支持 PostgreSQL。** 代码里用了大量 PG 专有特性
 * （`DISTINCT ON`、`ON CONFLICT ... DO UPDATE`、`::text` 类型转换、
 * `FILTER (WHERE ...)` 聚合、部分索引），换 SQLite 要重写整个数据层。
 * 与其给一个「能装上但跑不起来」的选项，不如直接说清楚。
 */
final class Installer
{
    private string $baseDir;

    public function __construct(?string $baseDir = null)
    {
        $this->baseDir = rtrim($baseDir ?? dirname(__DIR__, 2), '/\\');
    }

    public function lockFile(): string
    {
        return $this->baseDir . '/var/install.lock';
    }

    public function envFile(): string
    {
        return $this->baseDir . '/.env';
    }

    /** 是否已完成安装 */
    public function installed(): bool
    {
        // ⚠️ 必须 clearstatcache。
        //
        // 这是 Swoole 常驻进程的一个坑：PHP 的 stat cache 在长驻进程里
        // **会跨请求保留**，不像 FPM/CGI 每个请求都重置。
        // 表现是：把 var/install.lock 删掉之后，同一个 worker 里的
        // is_file() 仍然返回 true —— 安装向导怎么都打不开，
        // 而且重启前完全看不出问题（CLI 下是正常的，因为那是新进程）。
        //
        // 加 true 表示连 realpath cache 一起清，只清这一个路径，
        // 开销是一次系统调用（微秒级），比排查这个 bug 便宜太多。
        $file = $this->lockFile();
        clearstatcache(true, $file);

        return is_file($file);
    }

    /**
     * 环境自检。
     *
     * @return array<int,array{name:string,need:string,now:string,ok:bool,hint:string}>
     */
    public function checkEnv(): array
    {
        $rows = [];

        $phpOk = version_compare(PHP_VERSION, '8.2.0', '>=');
        $rows[] = [
            'name' => 'PHP 版本',
            'need' => '>= 8.2',
            'now'  => PHP_VERSION,
            'ok'   => $phpOk,
            'hint' => $phpOk ? '' : '请升级 PHP，本项目用到了只读属性、枚举、match 等 8.x 语法',
        ];

        $exts = [
            'pdo_pgsql' => 'PostgreSQL 数据库驱动（必需）',
            'pdo'       => 'PDO 扩展（必需）',
            'curl'      => '中继请求上游用（必需）',
            'mbstring'  => '多字节字符串处理（必需）',
            'json'      => 'JSON 编解码（内置，通常都有）',
            'swoole'    => '常驻内存 HTTP 服务（可选，没有也能用 PHP-FPM 跑）',
            'gd'        => '图片处理（可选，仅生成验证码时用）',
            'openssl'   => '签名验签（可选，仅支付回调时用）',
        ];
        foreach ($exts as $ext => $desc) {
            $ok = extension_loaded($ext);
            $optional = str_contains($desc, '可选');
            $rows[] = [
                'name' => '扩展 ' . $ext,
                'need' => $optional ? '可选' : '必需',
                'now'  => $ok ? '已安装' : '未安装',
                'ok'   => $ok || $optional,
                'hint' => ($ok || !$optional) ? '' : $desc,
            ];
        }

        foreach (['config' => '配置目录（写 .env 与配置缓存）', 'var' => '运行目录（写日志与安装锁）', 'web' => '前端目录（如需上传 logo）'] as $dir => $desc) {
            $full = $this->baseDir . '/' . $dir;
            $exists = is_dir($full);
            $writable = $exists && is_writable($full);
            $rows[] = [
                'name' => '目录 ' . $dir . '/',
                'need' => '可写',
                'now'  => !$exists ? '不存在' : ($writable ? '可写' : '只读'),
                'ok'   => $writable,
                'hint' => $writable ? '' : $desc,
            ];
        }

        return $rows;
    }

    /**
     * 测试数据库连接（不建表，只连）。
     *
     * @param array{host:string,port:int,database:string,username:string,password:string} $cfg
     * @return array{ok:bool,message:string,version?:string}
     */
    public function testDb(array $cfg): array
    {
        try {
            $pdo = new PDO(
                sprintf('pgsql:host=%s;port=%d;dbname=%s', $cfg['host'], $cfg['port'], $cfg['database']),
                $cfg['username'],
                $cfg['password'],
                [
                    PDO::ATTR_ERRMODE            => PDO::ERRMODE_EXCEPTION,
                    PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC,
                    PDO::ATTR_TIMEOUT            => 5,
                ]
            );
            $ver = (string) $pdo->query('SHOW server_version')->fetchColumn();

            // 顺带检查 pgcrypto / 扩展要不要 —— 本项目的迁移只用到内置功能，
            // 但提前告诉用户版本，遇到奇怪语法问题时好排查
            return ['ok' => true, 'message' => '连接成功', 'version' => $ver];
        } catch (Throwable $e) {
            $msg = $e->getMessage();
            // 把常见的几种错误翻译成人话，否则用户看到的是
            // "SQLSTATE[08006] [7] could not connect to server"
            $friendly = match (true) {
                str_contains($msg, 'could not connect')   => '连不上数据库，检查 host / port 是否正确、PostgreSQL 是否已启动',
                str_contains($msg, 'password')            => '用户名或密码错误',
                str_contains($msg, 'does not exist')      => '数据库不存在，请先手动创建（例如 createdb ltzy）',
                str_contains($msg, 'authentication')      => '认证失败，检查 pg_hba.conf 是否允许该用户从本机连接',
                default                                   => $msg,
            };
            return ['ok' => false, 'message' => $friendly];
        }
    }

    /**
     * 执行安装。
     *
     * @param array{
     *   db: array{host:string,port:int,database:string,username:string,password:string},
     *   site: array{name:string,url:string,logo?:string},
     *   admin: array{username:string,password:string}
     * } $in
     *
     * @return array{ok:bool,message:string,steps:array<int,string>}
     */
    public function run(array $in): array
    {
        $steps = [];

        if ($this->installed()) {
            return ['ok' => false, 'message' => '已经安装过了。如需重装，请先删除 var/install.lock', 'steps' => $steps];
        }

        // ── 1. 校验输入 ──
        $db = $in['db'] ?? [];
        foreach (['host', 'database', 'username'] as $k) {
            if (trim((string) ($db[$k] ?? '')) === '') {
                return ['ok' => false, 'message' => "数据库参数 {$k} 不能为空", 'steps' => $steps];
            }
        }
        $adminUser = trim((string) ($in['admin']['username'] ?? ''));
        $adminPass = (string) ($in['admin']['password'] ?? '');
        if ($adminUser === '' || !preg_match('/^[A-Za-z0-9_.\-]{3,64}$/', $adminUser)) {
            return ['ok' => false, 'message' => '管理员用户名需 3~64 位，只允许字母、数字、下划线、点、横线', 'steps' => $steps];
        }
        if (strlen($adminPass) < 6) {
            return ['ok' => false, 'message' => '管理员密码至少 6 位', 'steps' => $steps];
        }

        $db = [
            'host'     => trim((string) $db['host']),
            'port'     => (int) ($db['port'] ?? 5432),
            'database' => trim((string) $db['database']),
            'username' => trim((string) $db['username']),
            'password' => (string) ($db['password'] ?? ''),
        ];

        // ── 2. 连库 ──
        $test = $this->testDb($db);
        if (!$test['ok']) {
            return ['ok' => false, 'message' => '数据库连接失败：' . $test['message'], 'steps' => $steps];
        }
        $steps[] = '数据库连接成功（PostgreSQL ' . ($test['version'] ?? '') . '）';

        $pdo = new PDO(
            sprintf('pgsql:host=%s;port=%d;dbname=%s', $db['host'], $db['port'], $db['database']),
            $db['username'],
            $db['password'],
            [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION, PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC]
        );

        // ── 3. 执行迁移 ──
        try {
            $applied = $this->migrate($pdo);
            $steps[] = "建表完成（应用了 {$applied} 个迁移）";
        } catch (Throwable $e) {
            return ['ok' => false, 'message' => '建表失败：' . $e->getMessage(), 'steps' => $steps];
        }

        // ── 4. 创建/更新管理员 ──
        try {
            // cost 固定 12：必须与注册接口一致。
            // 不指定时用的是 php.ini 默认值，不同环境可能不同 ——
            // 而登录端的时序防护（dummy 哈希）按 cost=12 设计，
            // cost 不一致会让「用户是否存在」能从响应耗时上看出来。
            $hash = password_hash($adminPass, PASSWORD_BCRYPT, ['cost' => 12]);
            // 用户名大小写不敏感查重，与登录 / 注册保持一致
            $exists = $pdo->prepare('SELECT id FROM users WHERE lower(username) = lower(?)');
            $exists->execute([$adminUser]);
            if ($exists->fetchColumn() !== false) {
                $pdo->prepare("
                    UPDATE users SET password_hash = ?, role = 100, account_type = 'managed', status = 1
                     WHERE lower(username) = lower(?)
                ")->execute([$hash, $adminUser]);
                $steps[] = "管理员 {$adminUser} 已存在，密码已重置";
            } else {
                $pdo->prepare("
                    INSERT INTO users (username, password_hash, display_name, role, status, grp, account_type)
                    VALUES (?, ?, '管理员', 100, 1, 'default', 'managed')
                ")->execute([$adminUser, $hash]);
                $steps[] = "管理员 {$adminUser} 已创建";
            }
        } catch (Throwable $e) {
            return ['ok' => false, 'message' => '创建管理员失败：' . $e->getMessage(), 'steps' => $steps];
        }

        // ── 5. 站点信息写进 options ──
        try {
            $site = $in['site'] ?? [];
            $pairs = [
                'site_name'        => trim((string) ($site['name'] ?? 'LLM Gateway')) ?: 'LLM Gateway',
                'site_url'         => rtrim(trim((string) ($site['url'] ?? '')), '/'),
                'site_logo'        => trim((string) ($site['logo'] ?? '')),
                'site_installed_at' => date('Y-m-d H:i:s'),
            ];
            $up = $pdo->prepare("
                INSERT INTO options (key, value) VALUES (?, ?)
                ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
            ");
            foreach ($pairs as $k => $v) {
                if ($v !== '') {
                    $up->execute([$k, $v]);
                }
            }
            $steps[] = '站点信息已保存';
        } catch (Throwable $e) {
            // options 表可能没有 ON CONFLICT 需要的唯一索引，降级处理
            try {
                $site = $in['site'] ?? [];
                $del = $pdo->prepare('DELETE FROM options WHERE key = ?');
                $ins = $pdo->prepare('INSERT INTO options (key, value) VALUES (?, ?)');
                foreach ([
                    'site_name' => trim((string) ($site['name'] ?? 'LLM Gateway')) ?: 'LLM Gateway',
                    'site_url'  => rtrim(trim((string) ($site['url'] ?? '')), '/'),
                    'site_logo' => trim((string) ($site['logo'] ?? '')),
                ] as $k => $v) {
                    if ($v === '') {
                        continue;
                    }
                    $del->execute([$k]);
                    $ins->execute([$k, $v]);
                }
                $steps[] = '站点信息已保存';
            } catch (Throwable $e2) {
                return ['ok' => false, 'message' => '保存站点信息失败：' . $e2->getMessage(), 'steps' => $steps];
            }
        }

        // ── 6. 写 .env ──
        try {
            $this->writeEnv($db, $in['site'] ?? []);
            $steps[] = '.env 已生成';
        } catch (Throwable $e) {
            return ['ok' => false, 'message' => '写配置文件失败：' . $e->getMessage(), 'steps' => $steps];
        }

        // ── 7. 上锁（最后一步）──
        try {
            $this->writeLock($adminUser);
            $steps[] = '安装锁已写入';
        } catch (Throwable $e) {
            return ['ok' => false, 'message' => '写安装锁失败：' . $e->getMessage(), 'steps' => $steps];
        }

        return ['ok' => true, 'message' => '安装完成', 'steps' => $steps];
    }

    /**
     * 按文件名顺序执行 migrations/ 下未应用的 SQL。
     *
     * @return int 本次应用了几个
     */
    private function migrate(PDO $pdo): int
    {
        $pdo->exec('CREATE TABLE IF NOT EXISTS schema_migrations (
            version VARCHAR(64) PRIMARY KEY,
            applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
        )');

        $applied = $pdo->query('SELECT version FROM schema_migrations')
            ->fetchAll(PDO::FETCH_COLUMN);
        $done = array_flip($applied);

        $dir = $this->baseDir . '/migrations';
        $files = glob($dir . '/*.sql') ?: [];
        sort($files);

        $count = 0;
        foreach ($files as $file) {
            $version = basename($file, '.sql');
            if (isset($done[$version])) {
                continue;
            }
            $sql = file_get_contents($file);
            if ($sql === false || trim($sql) === '') {
                continue;
            }

            // 整个文件放一个事务里：一条 SQL 失败就整份回滚，
            // 避免留下「建了一半」的库
            $pdo->beginTransaction();
            try {
                $pdo->exec($sql);
                $pdo->prepare('INSERT INTO schema_migrations (version) VALUES (?)')
                    ->execute([$version]);
                $pdo->commit();
                $count++;
            } catch (Throwable $e) {
                $pdo->rollBack();
                throw new \RuntimeException($version . ' → ' . $e->getMessage(), 0, $e);
            }
        }

        return $count;
    }

    /**
     * 生成 .env。
     */
    private function writeEnv(array $db, array $site): void
    {
        $vars = [
            'APP_HOST'              => '127.0.0.1',
            'APP_PORT'              => 3000,
            'SITE_URL'              => rtrim((string) ($site['url'] ?? ''), '/'),
            'DB_HOST'               => $db['host'],
            'DB_PORT'               => $db['port'],
            'DB_DATABASE'           => $db['database'],
            'DB_USERNAME'           => $db['username'],
            'DB_PASSWORD'           => $db['password'],
            'DB_POOL_SIZE'          => 16,
            'LOG_LEVEL'             => 'info',
            'LOG_STDOUT'            => 'true',
            'FREE_ANONYMOUS_ENABLED' => 'false',
            'REGISTER_ENABLED'      => 'true',
            'RELAY_TIMEOUT'         => 300,
            'RETRY_TIMES'           => 3,
        ];

        $header = <<<TXT
        LLM Gateway 配置文件
        由安装向导生成于 %s

        注意：
        1. 这个文件含数据库密码，权限必须是 600，且不要提交到版本库
        2. 直接改这里的内容后需要重启服务才生效
        3. 同名环境变量（systemd / docker）优先级高于本文件
        TXT;
        $header = sprintf($header, date('Y-m-d H:i:s'));

        $content = \Ltzy\Support\Env::render($vars, $header);

        $tmp = $this->envFile() . '.tmp';
        if (file_put_contents($tmp, $content) === false) {
            throw new \RuntimeException('无法写入 ' . $tmp);
        }
        @chmod($tmp, 0600);
        if (!rename($tmp, $this->envFile())) {
            throw new \RuntimeException('无法替换 .env');
        }
        @chmod($this->envFile(), 0600);
    }

    /**
     * 写安装锁。内容是安装元信息，方便将来排查「什么时候装的、谁装的」。
     */
    private function writeLock(string $adminUser): void
    {
        $dir = dirname($this->lockFile());
        if (!is_dir($dir)) {
            @mkdir($dir, 0755, true);
        }
        $body = json_encode([
            'installed_at' => date('c'),
            'php'          => PHP_VERSION,
            'admin'        => $adminUser,
        ], JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE);
        if (file_put_contents($this->lockFile(), $body) === false) {
            throw new \RuntimeException('无法写入 ' . $this->lockFile());
        }
        @chmod($this->lockFile(), 0600);
    }
}
