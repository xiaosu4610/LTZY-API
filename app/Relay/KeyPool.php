<?php
declare(strict_types=1);

namespace Ltzy\Relay;

/**
 * 密钥池：一个渠道的多把密钥轮询与冷却。
 *
 * 背景：同一个上游可能挂多把密钥，它们模型列表完全一致，只是额度与限流各自独立。
 * 如果按「一把密钥一条渠道」建，要建 500 条渠道、模型列表重复维护 500 遍。
 * 所以让一个渠道的 Key 字段存多行密钥，请求时轮询选一把。
 *
 * 三个设计取舍（沿用现网 Go 版的经验）：
 *   1. **状态只在内存，不落库** —— 密钥失效通常是可自愈的（额度刷新、上游抖动），
 *      落库反而会把一次性失败永久固化下来。
 *   2. **按密钥粒度冷却，不按渠道** —— 一把失效不应影响同渠道另外 499 把。
 *   3. **全部冷却时仍返回一把** —— 直接报错比试一把更糟，可能是冷却状态的误判。
 *
 * 协程安全性说明：本类所有方法内部只有数组遍历与比较，没有 IO 操作，
 * 因此不会被 Swoole 的协程调度打断，**不需要加锁**。
 * 如果将来把状态存到 Redis（有 IO），就必须重新审视这一点。
 */
final class KeyPool
{
    /** @var string[] 去重后的密钥 */
    private array $keys = [];

    /** @var array<string,int> 冷却截止时间戳；key 是密钥的 md5，不存明文 */
    private array $cooldownUntil = [];

    /** @var int 轮询游标 */
    private int $cursor = 0;

    public function __construct(
        private readonly int $channelId,
        string $rawKeys,
        private readonly int $cooldownAuthFailed = 1800,
        private readonly int $cooldownRateLimited = 60,
    ) {
        $this->keys = self::split($rawKeys);
    }

    /**
     * 拆分多密钥字段。
     *
     * 支持换行 / 英文逗号 / 中文逗号 / 分号 —— 现网运营人员从各种地方
     * 复制密钥过来，分隔符不统一，全兼容才不用每次都手工整理。
     */
    public static function split(string $raw): array
    {
        if ($raw === '') {
            return [];
        }
        $normalized = str_replace(
            ["\r\n", "\r", ',', '，', ';'],
            "\n",
            $raw
        );
        $seen = [];
        $out = [];
        foreach (explode("\n", $normalized) as $part) {
            $k = trim($part);
            if ($k === '' || isset($seen[$k])) {
                continue;
            }
            $seen[$k] = true;
            $out[] = $k;
        }
        return $out;
    }

    /**
     * 挑一把可用密钥。
     *
     * @return string|null 池为空时返回 null
     */
    public function pick(): ?string
    {
        $n = count($this->keys);
        if ($n === 0) {
            return null;
        }
        if ($n === 1) {
            return $this->keys[0];
        }

        $now = time();
        $start = $this->cursor % $n;
        $this->cursor = ($this->cursor + 1) % $n;

        // 从游标位置开始找第一把未冷却的
        for ($i = 0; $i < $n; $i++) {
            $idx = ($start + $i) % $n;
            $key = $this->keys[$idx];
            $fp = md5($key);
            if (($this->cooldownUntil[$fp] ?? 0) > $now) {
                continue;
            }
            return $key;
        }

        // 全部在冷却期：兜底返回游标位置那把。
        // 让它试一次比直接失败好 —— 冷却状态可能是误判（比如上游短暂抖动）。
        return $this->keys[$start];
    }

    /**
     * 标记某把密钥进入冷却。
     */
    public function markFailed(string $key, int $statusCode): void
    {
        $ttl = match (true) {
            // 401/403：密钥失效或额度耗尽，短期内不会恢复
            $statusCode === 401, $statusCode === 403 => $this->cooldownAuthFailed,
            // 429：上游限流，冷却太久会白白浪费额度
            $statusCode === 429 => $this->cooldownRateLimited,
            // 其他（5xx、超时）：是上游服务问题，换密钥也没用，不冷却
            default => 0,
        };
        if ($ttl <= 0) {
            return;
        }
        $this->cooldownUntil[md5($key)] = time() + $ttl;
    }

    /**
     * 清理已过期的冷却记录，避免数组无限增长。
     *
     * 由定时任务周期调用。如果不清理，长期运行后这个数组会慢慢变大 ——
     * 虽然每项只有几十字节，但常驻内存的进程不该有单调增长的结构。
     */
    public function gc(): void
    {
        $now = time();
        foreach ($this->cooldownUntil as $fp => $until) {
            if ($until <= $now) {
                unset($this->cooldownUntil[$fp]);
            }
        }
    }

    /**
     * 密钥池统计（供管理后台展示，不含明文密钥）。
     */
    public function stats(): array
    {
        $now = time();
        $cooling = [];
        foreach ($this->cooldownUntil as $fp => $until) {
            if ($until > $now) {
                $cooling[$fp] = $until;
            }
        }

        return [
            'channel_id' => $this->channelId,
            'total'      => count($this->keys),
            'available'  => count($this->keys) - count($cooling),
            'cooling'    => count($cooling),
            // 脱敏后按剩余冷却时间倒序，便于运营先看快过期的
            'cooling_detail' => array_map(
                fn (string $fp, int $until) => [
                    'fingerprint'      => $fp,
                    'remaining_seconds' => $until - $now,
                    'until'            => date('c', $until),
                ],
                array_keys($cooling),
                array_values($cooling)
            ),
        ];
    }

    public function count(): int
    {
        return count($this->keys);
    }
}
