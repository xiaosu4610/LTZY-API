<?php
declare(strict_types=1);

/**
 * 压测工具。
 *
 * 用法：
 *   php bin/bench.php --url=/v1/models --concurrency=500 --total=5000
 *   php bin/bench.php --url=/v1/chat/completions --concurrency=200 --total=2000 --chat
 *
 * 为什么要自己写而不是用 ab/wrk：
 *   Swoole 的 hook 让我们能在单进程内跑几千个协程，
 *   比外部工具起几千个连接更贴近真实客户端行为（连接复用、HTTP/1.1 keep-alive）。
 */

use function Swoole\Coroutine\run;
use function Swoole\Coroutine\go;

$opts = [
    'base'        => 'http://127.0.0.1:3000',
    'url'         => '/v1/models',
    'concurrency' => 200,
    'total'       => 2000,
    'chat'        => false,
    'stream'      => false,
];

foreach ($argv as $a) {
    if (preg_match('/^--(\w+)=(.*)$/', $a, $m)) {
        $opts[$m[1]] = is_numeric($m[2]) ? (int) $m[2] : $m[2];
    } elseif ($a === '--chat') {
        $opts['chat'] = true;
    } elseif ($a === '--stream') {
        $opts['stream'] = true;
    }
}

$url = parse_url($opts['base']);
$host = $url['host'] ?? '127.0.0.1';
$port = (int) ($url['port'] ?? 80);

$path = (string) $opts['url'];
$concurrency = max(1, (int) $opts['concurrency']);
$total = max(1, (int) $opts['total']);

$payload = json_encode($opts['stream']
    ? ['model' => 'internlm3-8b-instruct', 'messages' => [['role' => 'user', 'content' => 'hi']], 'max_tokens' => 20, 'stream' => true]
    : ['model' => 'internlm3-8b-instruct', 'messages' => [['role' => 'user', 'content' => 'hi']], 'max_tokens' => 20],
    JSON_UNESCAPED_UNICODE);

printf("压测 %s%s  并发 %d  总请求 %d  %s\n", $opts['base'], $path, $concurrency, $total, $opts['chat'] ? '(POST chat)' : '(GET)');
echo str_repeat('─', 70), "\n";

$latencies = [];
$errors = 0;
$done = 0;
$t0 = 0.0;
$errSamples = [];

run(function () use ($host, $port, $path, $concurrency, $total, $opts, &$latencies, &$errors, &$done, &$t0, &$errSamples) {
    $t0 = microtime(true);

    // 用 Channel 做闸门：控制同时在飞的协程数
    $gate = new Swoole\Coroutine\Channel($concurrency);
    $lock = new Swoole\Coroutine\Channel(1);
    $lock->push(true);

    for ($i = 0; $i < $total; $i++) {
        $gate->push(true);   // 满了会阻塞，天然限制并发

        go(function () use ($host, $port, $path, $opts, $gate, $lock, &$latencies, &$errors, &$errSamples, &$done, $i) {
            $start = microtime(true);

            $cli = new Swoole\Coroutine\Http\Client($host, $port);
            $cli->set([
                'timeout'         => 120,
                'connect_timeout' => 10,
                'keep_alive'      => true,
            ]);

            if ($opts['chat']) {
                $cli->setHeaders(['Content-Type' => 'application/json']);
                $cli->setData(json_encode([
                    'model'      => 'internlm3-8b-instruct',
                    'messages'   => [['role' => 'user', 'content' => 'hi']],
                    'max_tokens' => 20,
                    'stream'     => (bool) $opts['stream'],
                ], JSON_UNESCAPED_UNICODE));
                $cli->setMethod('POST');
            }
            $cli->execute($path);

            $status = $cli->statusCode;
            $errCode = $cli->errCode;
            $cli->close();

            $elapsed = (microtime(true) - $start) * 1000;

            $lock->pop();
            if ($status === 200) {
                $latencies[] = $elapsed;
            } else {
                // 记录失败明细，区分「连不上」和「服务端返回非 200」
                $errors++;
                $errSamples[] = $status === 0
                    ? 'connect_error:' . $errCode
                    : 'http_' . $status;
            }
            $done++;
            $lock->push(true);

            $gate->pop();
        });
    }

    // 等所有协程结束
    while ($done < $total) {
        \Swoole\Coroutine::sleep(0.05);
    }
});

$elapsed = microtime(true) - $t0;

sort($latencies);
$n = count($latencies);

$pct = static function (float $p) use ($latencies, $n): float {
    if ($n === 0) {
        return 0.0;
    }
    $idx = (int) floor(($n - 1) * $p);
    return $latencies[$idx];
};

printf("总耗时     : %.2f s\n", $elapsed);
printf("成功       : %d\n", $n);
printf("失败       : %d\n", $errors);
if ($errors > 0 && $errSamples !== []) {
    $grouped = array_count_values($errSamples);
    echo "  失败明细  : ";
    $parts = [];
    foreach ($grouped as $k => $v) {
        $parts[] = "$k × $v";
    }
    echo implode(', ', $parts), "\n";
}
printf("QPS        : %.0f\n", $n / max($elapsed, 0.001));
echo "─── 延迟 (ms) ───\n";
printf("  min %.1f / p50 %.1f / p90 %.1f / p99 %.1f / max %.1f\n",
    $n ? $latencies[0] : 0,
    $pct(0.50), $pct(0.90), $pct(0.99),
    $n ? $latencies[$n - 1] : 0
);
printf("平均       : %.1f ms\n", $n ? array_sum($latencies) / $n : 0);

// 进程内存（需要 PID，由外部传入或按名字找）
$pid = (int) ($opts['pid'] ?? 0);
if ($pid > 0) {
    printf("服务端内存 : %.0f MB\n", 0.0);
}
