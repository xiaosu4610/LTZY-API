<?php
declare(strict_types=1);

require __DIR__ . '/_testkit.php';

/**
 * 分段压测：找出真实瓶颈在哪。
 *
 * 用法：php bin/bench-layers.php
 *
 * 思路：从"最轻的接口"到"最重的接口"逐层加压，看 QPS 在哪一层掉下来。
 *
 *   /api/status      无鉴权、无查库、命中缓存  → 框架基线
 *   /v1/models       无鉴权、构造模型列表      → 遍历开销
 *   /api/model/plaza 无鉴权、查渠道池状态      → 池调用开销
 *   /api/me          带鉴权、查用户表          → 鉴权 + DB 开销
 *   /api/account/logs 带鉴权、查日志表         → 复杂查询开销
 */

use function Swoole\Coroutine\run;
use function Swoole\Coroutine\go;

$base = 'http://127.0.0.1:3000';

// 拿两个 token
function login(string $u, string $p): string
{
    $ch = curl_init('http://127.0.0.1:3000/api/login');
    curl_setopt_array($ch, [
        CURLOPT_POST => true,
        CURLOPT_POSTFIELDS => json_encode(['username' => $u, 'password' => $p]),
        CURLOPT_HTTPHEADER => ['Content-Type: application/json'],
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT => 30,
    ]);
    $j = json_decode((string) curl_exec($ch), true);
    return $j['data']['access_token'] ?? '';
}

$benchAdmin = getenv('E2E_ADMIN_USER') ?: '';
$benchPass  = getenv('E2E_ADMIN_PASS') ?: '';
$adminTok = $benchAdmin !== '' ? login($benchAdmin, $benchPass) : '';
// 普通用户：临时账号，跑完即删（库里不再保留示例数据）
$probe = probeCreate('probe', 10);
$acctTok = login($probe['username'], $probe['password']);

$cases = [
    ['/api/status',       null,      '框架基线（无鉴权/无查库）'],
    ['/v1/models',        null,      '模型列表（构造 39 个对象）'],
    ['/api/model/plaza',  null,      '广场（遍历渠道 + 池状态）'],
    ['/api/me',           $acctTok,  '鉴权 + 查用户表'],
    ['/api/account/logs', $acctTok,  '鉴权 + 查日志表'],
    ['/api/manage/channels', $adminTok, '管理接口 + 池状态'],
];

$concurrency = 50;
$total = 1000;

printf("并发 %d，每个接口 %d 请求\n\n", $concurrency, $total);
printf("%-24s %-30s %8s %8s %8s\n", '接口', '说明', 'QPS', 'p50', 'p99');
echo str_repeat('─', 90), "\n";

foreach ($cases as [$path, $tok, $desc]) {
    $lat = [];
    $err = 0;
    $t0 = 0.0;

    run(function () use ($base, $path, $tok, $concurrency, $total, &$lat, &$err, &$t0) {
        $t0 = microtime(true);
        $gate = new Swoole\Coroutine\Channel($concurrency);
        $lock = new Swoole\Coroutine\Channel(1);
        $lock->push(true);
        $done = 0;

        for ($i = 0; $i < $total; $i++) {
            $gate->push(true);
            go(function () use ($base, $path, $tok, $gate, $lock, &$lat, &$err, &$done) {
                $start = microtime(true);
                $cli = new Swoole\Coroutine\Http\Client('127.0.0.1', 3000);
                $cli->set(['timeout' => 30, 'connect_timeout' => 5, 'keep_alive' => true]);
                if ($tok !== null) {
                    $cli->setHeaders(['Authorization' => 'Bearer ' . $tok]);
                }
                $cli->execute($path);
                $code = $cli->statusCode;
                $cli->close();
                $ms = (microtime(true) - $start) * 1000;

                $lock->pop();
                if ($code === 200) {
                    $lat[] = $ms;
                } else {
                    $err++;
                }
                $done++;
                $lock->push(true);
                $gate->pop();
            });
        }

        while ($done < $total) {
            \Swoole\Coroutine::sleep(0.02);
        }
    });

    $elapsed = microtime(true) - $t0;
    sort($lat);
    $n = count($lat);
    $p = static fn(float $q) => $n ? $lat[(int) floor(($n - 1) * $q)] : 0;

    printf("%-24s %-30s %8.0f %7.1f %7.1f%s\n",
        str_pad(substr($path, 0, 24), 24),
        str_pad(substr($desc, 0, 30), 30),
        $n / max($elapsed, 0.001),
        $p(0.5), $p(0.99),
        $err > 0 ? "  失败{$err}" : ''
    );

    // 间隔，让端口释放
    \Swoole\Coroutine\run(static function () {
        \Swoole\Coroutine::sleep(2);
    });
}

echo "\n";
echo "说明：\n";
echo "  · QPS 掉得最多的地方就是瓶颈\n";
echo "  · p99 远高于 p50 说明有排队或偶发慢查询\n";

// 临时测试账号清理：脚本无论怎么退出（正常/异常/被 kill）都不会在库里留残留
if (isset($probe) && is_array($probe)) {
    probeDelete($probe['id']);
}
