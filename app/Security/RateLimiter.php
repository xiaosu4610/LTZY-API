<?php
declare(strict_types=1);

namespace Ltzy\Security;

/**
 * 通用固定窗口限流器。
 *
 * 为什么不用滑安窗口（存每个时间戳）：
 *   验证码、下单这类场景只要「大概限住」就够了，固定窗口用 O(1) 内存
 *   就能达到目的。滑动窗口要存一串时间戳，被扫 IP 时数组会膨胀，
 *   反而成为新的内存攻击面。
 *
 * 为什么不用 Redis：
 *   单进程 + 协程模型下，进程内数组就是最快的共享状态。
 *   多实例部署时各实例独立计数（实际限额 = 单实例限额 × 实例数），
 *   这个误差对防刷完全可接受，换来的是零外部依赖。
 *
 * 边界行为：窗口从第一次请求开始计算，到点清零。
 *   最坏情况下（窗口边界）允许 2×limit 次请求，这是固定窗口的固有特性。
 */
final class RateLimiter
{
    /** @var array<string,array{count:int,reset:float}> */
    private array $buckets = [];

    /**
     * @param int $maxKeys 键数量上限。超出后强制 GC，防止被扫 IP/UA 撑爆内存
     */
    public function __construct(private readonly int $maxKeys = 50000)
    {
    }

    /**
     * 尝试消耗一次配额。
     *
     * @return bool true = 放行且已计数；false = 超限（未计数）
     */
    public function allow(string $key, int $limit, int $windowSec): bool
    {
        if ($limit <= 0 || $windowSec <= 0) {
            return true;
        }

        $now = microtime(true);

        // 键太多时先回收一轮，避免内存被无限制的 key 撑大
        if (count($this->buckets) > $this->maxKeys) {
            $this->gc();
            if (count($this->buckets) > $this->maxKeys) {
                $this->buckets = [];
            }
        }

        $b = $this->buckets[$key] ?? null;

        // 新窗口
        if ($b === null || $b['reset'] <= $now) {
            $this->buckets[$key] = ['count' => 1, 'reset' => $now + $windowSec];

            return true;
        }

        if ($b['count'] >= $limit) {
            return false;
        }

        $this->buckets[$key]['count']++;

        return true;
    }

    /** 当前窗口内已用次数（诊断用） */
    public function used(string $key): int
    {
        $b = $this->buckets[$key] ?? null;
        if ($b === null || $b['reset'] <= microtime(true)) {
            return 0;
        }

        return $b['count'];
    }

    /** 距离窗口重置还有几秒（用于 Retry-After） */
    public function retryAfter(string $key): int
    {
        $b = $this->buckets[$key] ?? null;
        if ($b === null) {
            return 0;
        }

        return max(0, (int) ceil($b['reset'] - microtime(true)));
    }

    /** 清理所有过期窗口 */
    public function gc(): int
    {
        $now = microtime(true);
        $n = 0;
        foreach ($this->buckets as $k => $b) {
            if ($b['reset'] <= $now) {
                unset($this->buckets[$k]);
                $n++;
            }
        }

        return $n;
    }

    public function stats(): array
    {
        return ['keys' => count($this->buckets)];
    }
}
