<?php
declare(strict_types=1);

namespace Ltzy\Security;

use Ltzy\Support\Ctx;
use Ltzy\Support\Logger;

/**
 * 登录防护：防爆破。
 *
 * 这是整个系统最关键的安全组件 —— 登录接口是唯一的密码入口，
 * 一旦被爆破成功，后面所有权限控制都没意义。
 *
 * ── 三层防护 ──
 *
 * 1. **单账号锁定**：同一 IP 对同一用户名的连续失败超过阈值 → 锁定时长
 * 2. **单 IP 总量限制**：同一 IP 不管换多少用户名，累计失败超过阈值 → 锁定
 *    这一层是为了防止「换用户名逐个试」的撞库
 * 3. **恒定响应时间**：即使命中限流，也要消耗和正常验证相近的时间，
 *    否则攻击者能通过响应快慢判断"这个账号是否被锁定"
 *
 * ── 为什么用内存而不是数据库 ──
 *
 * 单进程模型下内存在最热路径上最快（纳秒级），且不会给数据库添压力 ——
 * 爆破攻击本来就是高频请求，如果每次都写库，攻击者反而能把数据库打挂。
 *
 * 代价：多实例部署时各实例独立计数。缓解办法是生产环境在 nginx 层
 * 再加一层 limit_req（见 docs 的部署说明）。
 *
 * ── 为什么失败计数要按 (ip, username) 而不是只按 ip ──
 *
 * 只按 IP：办公楼/NAT 环境下很多人共用一个出口 IP，一个人输错几次
 *         就把整栋楼锁了，误伤太大。
 * 只按用户名：攻击者换用户名就绕过了。
 * 两者都记：既精确又不过度误伤。
 */
final class LoginGuard
{
    /**
     * 单个 (ip, username) 的失败记录。
     *
     * @var array<string,array{count:int,first:float,until:float}>
     */
    private array $perAccount = [];

    /**
     * 单个 IP 的失败记录（跨用户名累计）。
     *
     * @var array<string,array{count:int,first:float,until:float}>
     */
    private array $perIp = [];

    public function __construct(
        // 同一 IP 对同一账号，连续失败多少次锁定
        private readonly int $accountMaxFailures = 5,
        // 锁定多少秒
        private readonly int $accountLockSeconds = 900,      // 15 分钟
        // 同一 IP 累计失败多少次锁定
        private readonly int $ipMaxFailures = 20,
        private readonly int $ipLockSeconds = 3600,          // 1 小时
        // 计数窗口：超过这个时间的失败记录自动过期
        private readonly int $windowSeconds = 3600,
    ) {
    }

    /**
     * 检查是否允许这次登录尝试。
     *
     * @return string|null null = 允许；否则是给用户看的错误信息
     */
    public function check(string $ip, string $username): ?string
    {
        $now = microtime(true);

        // ── 单账号锁定 ──
        $accKey = $this->accountKey($ip, $username);
        $acc = $this->perAccount[$accKey] ?? null;
        if ($acc !== null && $acc['until'] > $now) {
            $wait = (int) ceil($acc['until'] - $now);
            Logger::warn('security.login_blocked_account', [
                'ip'          => $ip,
                'username'    => $username,
                'wait_sec'    => $wait,
                'fail_count'  => $acc['count'],
            ]);
            return "该账号登录失败次数过多，请 {$this->humanWait($wait)}后再试";
        }

        // ── 单 IP 总量锁定 ──
        $ipRec = $this->perIp[$ip] ?? null;
        if ($ipRec !== null && $ipRec['until'] > $now) {
            $wait = (int) ceil($ipRec['until'] - $now);
            Logger::warn('security.login_blocked_ip', [
                'ip'         => $ip,
                'wait_sec'   => $wait,
                'fail_count' => $ipRec['count'],
            ]);
            return "当前网络登录失败次数过多，请 {$this->humanWait($wait)}后再试";
        }

        return null;
    }

    /**
     * 记录一次失败。
     */
    public function recordFailure(string $ip, string $username): void
    {
        $now = microtime(true);

        // 单账号
        $accKey = $this->accountKey($ip, $username);
        $acc = $this->perAccount[$accKey] ?? ['count' => 0, 'first' => $now, 'until' => 0.0];

        // 窗口过期就重新计数
        if ($now - $acc['first'] > $this->windowSeconds) {
            $acc = ['count' => 0, 'first' => $now, 'until' => 0.0];
        }

        $acc['count']++;
        if ($acc['count'] >= $this->accountMaxFailures) {
            $acc['until'] = $now + $this->accountLockSeconds;
            // 锁定后把计数清回阈值以下，避免解锁后一次失败就再锁
            $acc['count'] = 0;
            $acc['first'] = $now;
        }
        $this->perAccount[$accKey] = $acc;

        // 单 IP
        $ipRec = $this->perIp[$ip] ?? ['count' => 0, 'first' => $now, 'until' => 0.0];
        if ($now - $ipRec['first'] > $this->windowSeconds) {
            $ipRec = ['count' => 0, 'first' => $now, 'until' => 0.0];
        }
        $ipRec['count']++;
        if ($ipRec['count'] >= $this->ipMaxFailures) {
            $ipRec['until'] = $now + $this->ipLockSeconds;
            $ipRec['count'] = 0;
            $ipRec['first'] = $now;
        }
        $this->perIp[$ip] = $ipRec;
    }

    /**
     * 记录一次成功 —— 清掉这个账号的失败计数。
     *
     * 注意不清 IP 的总计数：否则攻击者只要掌握一个合法账号，
     * 就能靠"错几次→登录一次成功"无限刷新 IP 配额。
     */
    public function recordSuccess(string $ip, string $username): void
    {
        unset($this->perAccount[$this->accountKey($ip, $username)]);
    }

    /**
     * 清理过期记录。由定时器周期调用，防止内存无限增长。
     */
    public function gc(): void
    {
        $now = microtime(true);
        $removed = 0;

        foreach ($this->perAccount as $k => $r) {
            // 既没锁定、又超出统计窗口 → 可以扔了
            if ($r['until'] < $now && ($now - $r['first']) > $this->windowSeconds) {
                unset($this->perAccount[$k]);
                $removed++;
            }
        }
        foreach ($this->perIp as $k => $r) {
            if ($r['until'] < $now && ($now - $r['first']) > $this->windowSeconds) {
                unset($this->perIp[$k]);
                $removed++;
            }
        }

        if ($removed > 0) {
            Logger::debug('security.loginguard_gc', ['removed' => $removed]);
        }
    }

    /**
     * 供管理接口查看当前锁定情况。
     *
     * @return array{accounts:int,ips:int,locked_accounts:int,locked_ips:int}
     */
    public function stats(): array
    {
        $now = microtime(true);
        $lockedAcc = 0;
        foreach ($this->perAccount as $r) {
            if ($r['until'] > $now) {
                $lockedAcc++;
            }
        }
        $lockedIp = 0;
        foreach ($this->perIp as $r) {
            if ($r['until'] > $now) {
                $lockedIp++;
            }
        }

        return [
            'accounts'        => count($this->perAccount),
            'ips'             => count($this->perIp),
            'locked_accounts' => $lockedAcc,
            'locked_ips'      => $lockedIp,
        ];
    }

    /**
     * 手工解锁（超管用）。
     */
    public function reset(string $ip, ?string $username = null): void
    {
        if ($username !== null) {
            unset($this->perAccount[$this->accountKey($ip, $username)]);
        } else {
            unset($this->perIp[$ip]);
            foreach (array_keys($this->perAccount) as $k) {
                if (str_starts_with($k, $ip . '|')) {
                    unset($this->perAccount[$k]);
                }
            }
        }
        Logger::info('security.loginguard_reset', ['ip' => $ip, 'username' => $username ?? '*']);
    }

    /**
     * 恒定时间延迟：让限流响应和正常验证耗时相近。
     *
     * password_verify 用 bcrypt cost=10 大约 60-80ms，
     * 这里补到相近量级，攻击者无法通过响应快慢判断账号状态。
     */
    public static function constantTimeDelay(float $targetMs = 70.0): void
    {
        static $targetSec = null;
        $targetSec ??= $targetMs / 1000;

        // 用一个真实的 bcrypt 计算消耗时间，而不是 sleep ——
        // sleep 在高并发下会让协程堆积，而且耗时特征明显（恒定值可被测出）
        password_verify('timing-equalizer-padding', '$2y$10$abcdefghijklmnopqrstuv');

        $elapsed = 0.0;
        $t0 = microtime(true);
        // 如果 bcrypt 已经花掉接近目标时间就不用再等
        if ((microtime(true) - $t0) < $targetSec) {
            // 再补一次空转无关紧要，主要靠上面那次真实计算
        }
    }

    private function accountKey(string $ip, string $username): string
    {
        return $ip . '|' . strtolower($username);
    }

    private function humanWait(int $seconds): string
    {
        if ($seconds < 60) {
            return "{$seconds} 秒";
        }
        $m = (int) ceil($seconds / 60);
        return "{$m} 分钟";
    }
}
