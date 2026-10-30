<?php
declare(strict_types=1);

namespace Ltzy\Security;

/**
 * 注册频率限制（按 IP）。
 *
 * 规则：单 IP 30 分钟内最多 10 次注册尝试。
 *
 * ── 为什么是「尝试」而不是「成功」──
 *
 * 只在成功时计数的话，攻击者可以用必然失败的用户名（已存在的、非法字符的）
 * 空转刷接口。这里在「通过全部前置校验、准备落库之前」就记一次，
 * 让无效请求也消耗配额。
 *
 * ── 为什么不用滑动窗口的精确实现 ──
 *
 * 滑动窗口要存每个时间戳。IP 数量一多，数组会膨胀。这里每次 check 时
 * 顺手把窗口外的过滤掉，天然保持数组简短，代价只是每次多一次 O(n) 遍历
 * （n 远小于上限 10）。
 */
final class RegisterGuard
{
    /** @var array<string,int[]> */
    private array $hits = [];

    public function __construct(
        private readonly int $max = 10,
        private readonly int $window = 1800,  // 30 分钟
        private readonly int $maxIps = 20000, // 防止 IP 扫描把内存撑爆
    ) {
    }

    /**
     * 检查是否已超限。返回 null 表示放行，否则返回提示文案。
     */
    public function check(string $ip): ?string
    {
        $now = time();
        $list = $this->prune($this->hits[$ip] ?? [], $now);

        if ($list !== []) {
            $this->hits[$ip] = $list;
        } else {
            unset($this->hits[$ip]);
        }

        if (count($list) >= $this->max) {
            $wait = $list[0] + $this->window - $now;
            $min = max(1, (int) ceil($wait / 60));
            return "注册过于频繁，请 {$min} 分钟后再试";
        }

        return null;
    }

    /**
     * 记录一次尝试。
     */
    public function record(string $ip): void
    {
        $now = time();
        $this->hits[$ip] = $this->prune($this->hits[$ip] ?? [], $now);
        $this->hits[$ip][] = $now;

        if (count($this->hits) > $this->maxIps) {
            $this->gc();
        }
    }

    /**
     * 清理所有过期记录（由定时器每分钟调用）。
     */
    public function gc(): void
    {
        $now = time();
        foreach ($this->hits as $ip => $list) {
            $kept = $this->prune($list, $now);
            if ($kept === []) {
                unset($this->hits[$ip]);
            } else {
                $this->hits[$ip] = $kept;
            }
        }
    }

    /**
     * 只保留窗口内的记录。
     *
     * @param int[] $list
     * @return int[]
     */
    private function prune(array $list, int $now): array
    {
        $from = $now - $this->window;
        $out = [];
        foreach ($list as $t) {
            if ($t > $from) {
                $out[] = $t;
            }
        }
        return $out;
    }

    public function stats(): array
    {
        return ['tracked_ips' => count($this->hits)];
    }
}
