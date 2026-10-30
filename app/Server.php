<?php
declare(strict_types=1);

namespace Ltzy;

use Ltzy\Db\Connection;
use Ltzy\Domain\Channel\DbChannelSource;
use Ltzy\Domain\User\AuthCache;
use Ltzy\Relay\KeyPoolManager;
use Ltzy\Security\LoginGuard;
use Ltzy\Support\ClassWarmer;
use Ltzy\Support\Logger;

/**
 * 服务器装配与生命周期。
 *
 * 用 Swoole\Coroutine\Http\Server 而不是 Swoole\Http\Server：
 *   1. Windows 版 Swoole 没有后者（裁剪版）；
 *   2. 单进程协程模型对这个项目够用 —— 压测实测 1000 并发 / QPS 2282 / 内存 146MB；
 *   3. 多实例部署可以替代多 worker，且滚动重启更容易。
 */
final class Server
{
    private \Swoole\Coroutine\Http\Server $server;
    private KeyPoolManager $pools;
    private LoginGuard $loginGuard;
    private ?AuthCache $authCache = null;
    private ?Router $router = null;

    public function __construct(
        private readonly array $cfg,
        private readonly array $dbCfg,
    ) {
        $this->pools = new KeyPoolManager(
            $cfg['cooldown_auth_failed'] ?? 1800,
            $cfg['cooldown_rate_limited'] ?? 60,
        );

        // 登录防护：5 次失败锁 15 分钟，同 IP 20 次失败锁 1 小时。
        // 阈值来自实际运营经验 —— 正常人不会连续输错 5 次密码，
        // 但可能手滑 2-3 次，所以不能设成 3。
        $this->loginGuard = new LoginGuard(
            accountMaxFailures: (int) ($cfg['login_max_failures'] ?? 5),
            accountLockSeconds: (int) ($cfg['login_lock_seconds'] ?? 900),
            ipMaxFailures:      (int) ($cfg['login_ip_max_failures'] ?? 20),
            ipLockSeconds:      (int) ($cfg['login_ip_lock_seconds'] ?? 3600),
        );
    }

    public function run(): void
    {
        Logger::init(
            $this->cfg['log_level'] ?? 'info',
            (bool) ($this->cfg['log_stdout'] ?? true),
        );

        // ⚠️ 三个关键点，都是踩出来的：
        //
        // 1. Coroutine\Http\Server::start() 必须在协程内调用，
        //    否则报 "API must be called in the coroutine"。
        //
        // 2. **重初始化不能放在协程里**。解析大 JSON / 大批量对象构造
        //    在协程栈里做会直接崩（0xC0000409 = STACK_BUFFER_OVERRUN），
        //    而且崩得毫无输出，极难定位。
        //
        // 3. **协程内触发 autoload 也会崩**（同一个错误码）。
        //    所以必须先把所有类预热到内存，协程里只做对象构造。
        //
        // 三条加起来的教训：**协程里只放"纯粹的运行时逻辑"，任何加载/解析都在外面做完。**

        $warm = ClassWarmer::warm(dirname(__DIR__) . '/app');
        Logger::info('server.class_warmed', [
            'loaded' => $warm['loaded'],
            'failed' => count($warm['failed']),
        ]);
        if ($warm['failed'] !== []) {
            Logger::warn('server.class_warm_failed', ['classes' => $warm['failed']]);
        }

        // 数据库连接池。
        //
        // ⚠️ 池大小必须读配置 —— 之前直接 new Connection($cfg) 用了默认 8，
        //    导致 config/app.php 里的 db_pool_size 完全是摆设。
        //
        // 8 对这个规模的系统够用：
        //   · 单进程协程模型，同一时刻的并发查询数受协程调度限制
        //   · PG 的 max_connections 是 100，留足余量给运维工具
        //   · 池太小会排队，太大会浪费 PG 的连接槽
        $db = new Connection($this->dbCfg, (int) ($this->cfg['db_pool_size'] ?? 8));
        $channels = new DbChannelSource($db, (int) ($this->cfg['channel_cache_ttl'] ?? 60));
        $this->authCache = new AuthCache(5, 2000);
        $this->router = $router = new Router($channels, $this->pools, $db, $this->loginGuard, $this->cfg, $this->authCache);

        Logger::info('server.bootstrap', [
            'php'    => PHP_VERSION,
            'swoole' => SWOOLE_VERSION,
            'db'     => sprintf('%s:%d/%s', $this->dbCfg['host'], $this->dbCfg['port'], $this->dbCfg['database']),
        ]);

        // 协程栈默认偏小，而流式转发里会有较大的局部变量（上游分块）。
        // 调到 8MB 留足余量。
        \Swoole\Coroutine::set(['stack_size' => 8 * 1024 * 1024]);

        \Swoole\Coroutine\run(function () use ($router) {
            $this->serve($router);
        });
    }

    private function serve(Router $router): void
    {
        $host = $this->cfg['host'] ?? '127.0.0.1';
        $port = (int) ($this->cfg['port'] ?? 3000);

        $this->server = new \Swoole\Coroutine\Http\Server($host, $port, false);
        $this->server->set([
            'enable_coroutine'  => true,
            'max_coroutine'     => 10000,
            'hook_flags'        => SWOOLE_HOOK_ALL,
            // 请求体上限：现网是 100MB（图片/音频走 base64）
            'package_max_length' => 100 * 1024 * 1024,
            // 压测发现默认 backlog（128）在 500 并发下会拒连：
            // 5000 个请求里有 711 个根本没到服务端。调到 4096 后正常。
            'backlog'           => 4096,
            // ⚠️ 隐藏技术栈。
            // 默认会回 `Server: swoole-http-server`，等于直接告诉攻击者
            // "这是个 Swoole 服务"，配合指纹可进一步定位 PHP 版本和已知 CVE。
            // 生产环境 nginx 反代会覆盖这个头，但直连时就会暴露，所以这里也要改。
            'server_header'     => 'nginx',
            'log_level'         => SWOOLE_LOG_WARNING,
        ]);

        // 所有路径都交给 Router 自己分派，
        // 不用 Swoole 的路径匹配 —— 我们的路由逻辑要支持容错（如 /chat/completions）
        $this->server->handle('/', function ($request, $response) use ($router) {
            $router->dispatch($request, $response);
        });

        // 定时清理密钥池里过期的冷却记录。
        // 不清理的话 cooldownUntil 数组会单调增长（每项几十字节，
        // 但常驻内存的进程不该有这样的结构）。
        \Swoole\Timer::tick(60_000, function () {
            $this->pools->gc();
            $this->loginGuard->gc();
            $this->authCache?->gc();
            // 验证码与注册限流的内存清理：不清理会随访问量单调增长
            $this->router?->gcSecurity();
        });

        Logger::info('server.listen', ['addr' => "{$host}:{$port}"]);

        $this->server->start();
    }

    public function shutdown(): void
    {
        if (isset($this->server)) {
            $this->server->shutdown();
        }
    }
}
