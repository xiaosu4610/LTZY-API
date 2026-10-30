<?php
declare(strict_types=1);

namespace Ltzy\Domain\User;

use Ltzy\Support\Logger;

/**
 * 鉴权结果缓存。
 *
 * ── 为什么需要 ──
 *
 * 压测数据（并发 50）：
 *   /api/status       1214 QPS   ← 无鉴权
 *   /api/me            456 QPS   ← 带鉴权，掉了一半
 *
 * 每个带鉴权的请求都要执行一次 tokens JOIN users 查询。
 * 而 PG 对分区表/多表 JOIN 的**规划时间**高达 4.6ms
 * （执行只要 0.2ms）—— 也就是说瓶颈不在查数据，
 * 在每次 prepare 都要重新做一次查询规划。
 *
 * PDO 不会缓存 prepared statement（每次调用都重新 prepare），
 * 所以唯一的解法是**减少查询次数**。
 *
 * ── 缓存策略 ──
 *
 * TTL 5 秒。权衡：
 *   · 太短：高并发下还是会频繁穿透
 *   · 太长：用户被停用/令牌被删后，最多要等 N 秒才生效
 *
 * 5 秒是这两者的平衡点 —— 对内部系统来说，"停用后 5 秒生效"完全可以接受。
 *
 * ── 为什么缓存整个身份而不是只缓存"token 是否有效" ──
 *
 * 因为下游还要用到 user 对象（余额、分组、角色）。
 * 如果只缓存有效性，还得再查一次用户 —— 那等于没省。
 *
 * ── 为什么不清除缓存 ──
 *
 * 用户改密码/被停用时，5 秒内旧缓存仍然有效。
 * 这是**已知且接受**的代价：单进程模型下，就在同一个内存里，
 * 加失效通知反而要引入跨请求状态管理，复杂度不划算。
 * 真要立即生效，重启进程即可。
 */
final class AuthCache
{
    /**
     * @var array<string,array{hit:array,until:float}>
     */
    private array $cache = [];

    public function __construct(
        private readonly int $ttlSeconds = 5,
        private readonly int $maxEntries = 2000,
    ) {
    }

    /**
     * 取缓存的鉴权结果。
     *
     * @return array{user:?User,token_id:int,remain_quota:int,models:string}|null
     */
    public function get(string $tokenKey): ?array
    {
        $e = $this->cache[$tokenKey] ?? null;
        if ($e === null) {
            return null;
        }
        if ($e['until'] < microtime(true)) {
            unset($this->cache[$tokenKey]);
            return null;
        }
        return $e['hit'];
    }

    /**
     * 写入缓存。
     *
     * 超过上限时清掉最早的一批 —— 防止被大量伪造 token 撑爆内存。
     */
    public function put(string $tokenKey, array $hit): void
    {
        if (count($this->cache) >= $this->maxEntries) {
            // 简单策略：删掉前 25%
            $keys = array_keys($this->cache);
            $drop = (int) (count($keys) * 0.25);
            for ($i = 0; $i < $drop; $i++) {
                unset($this->cache[$keys[$i]]);
            }
            Logger::debug('auth.cache_evicted', ['dropped' => $drop]);
        }

        $this->cache[$tokenKey] = [
            'hit'   => $hit,
            'until' => microtime(true) + $this->ttlSeconds,
        ];
    }

    /**
     * 清掉某个 token 的缓存（登出时用）。
     */
    public function forget(string $tokenKey): void
    {
        unset($this->cache[$tokenKey]);
    }

    /**
     * 清掉某个用户的所有缓存（改密码/停用时用）。
     *
     * 由于缓存的 key 是 token 而非 user_id，这里需要遍历。
     * 条目数有上限（2000），遍历成本可接受。
     */
    public function forgetUser(int $userId): int
    {
        $n = 0;
        foreach ($this->cache as $k => $e) {
            if (($e['hit']['user']->id ?? 0) === $userId) {
                unset($this->cache[$k]);
                $n++;
            }
        }
        return $n;
    }

    /**
     * 清理过期条目。由定时器周期调用。
     */
    public function gc(): void
    {
        $now = microtime(true);
        $n = 0;
        foreach ($this->cache as $k => $e) {
            if ($e['until'] < $now) {
                unset($this->cache[$k]);
                $n++;
            }
        }
        if ($n > 0) {
            Logger::debug('auth.cache_gc', ['removed' => $n]);
        }
    }

    /**
     * @return array{entries:int,max:int}
     */
    public function stats(): array
    {
        return ['entries' => count($this->cache), 'max' => $this->maxEntries];
    }
}
