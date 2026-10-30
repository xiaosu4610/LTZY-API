<?php
declare(strict_types=1);

namespace Ltzy\Security;

/**
 * 图形验证码存储（纯内存，单进程协程安全）。
 *
 * ── 为什么存内存而不是数据库/Redis ──
 *
 * 我们的服务是单进程 Swoole，协程在单线程里调度，PHP 数组的读写在协程切换
 * 点之间是原子的。验证码生命周期只有 5 分钟，重启丢失无所谓（用户刷新一下
 * 就有新的）。为这个引入 Redis 是给运维加负担，换不来任何东西。
 *
 * ── 一次性语义 ──
 *
 * verify() 无论对错都立刻删除该 id。这是防爆破的关键：
 * 不删的话，攻击者可以对同一个 id 反复猜，4 位字符 32^4 = 100 万种组合，
 * 但配合「每次刷新只能猜一次」就变成必须重新拿图，成本被拉高几个数量级。
 */
final class CaptchaStore
{
    /** @var array<string,array{code:string,expire:int}> */
    private array $items = [];

    public function __construct(
        private readonly int $ttl = 300,     // 5 分钟有效
        private readonly int $max = 5000,    // 上限，防止有人狂刷 /api/captcha 打爆内存
    ) {
    }

    /**
     * 发放一个验证码，返回 id（客户端提交时带回来）。
     */
    public function issue(string $code): string
    {
        $id = bin2hex(random_bytes(16));
        $this->items[$id] = ['code' => $code, 'expire' => time() + $this->ttl];

        // 超限先 GC，再不行就丢最老的
        if (count($this->items) > $this->max) {
            $this->gc();
            if (count($this->items) > $this->max) {
                $this->items = array_slice($this->items, -$this->max, null, true);
            }
        }

        return $id;
    }

    /**
     * 校验并立即销毁。
     *
     * 大小写不敏感（用户不该因为按了 caps lock 就失败），
     * 但字符集里已经剔除了 0/O/1/I/l，不会因形近字母误判。
     */
    public function verify(string $id, string $input): bool
    {
        if ($id === '') {
            return false;
        }
        $item = $this->items[$id] ?? null;
        if ($item === null) {
            return false;
        }

        // 一次性：无论对错，先删
        unset($this->items[$id]);

        if ($item['expire'] < time()) {
            return false;
        }

        return hash_equals($item['code'], strtoupper(trim($input)));
    }

    /**
     * 清理过期条目（由 Server 的定时器每分钟调一次）。
     */
    public function gc(): void
    {
        $now = time();
        foreach ($this->items as $id => $it) {
            if ($it['expire'] < $now) {
                unset($this->items[$id]);
            }
        }
    }

    public function count(): int
    {
        return count($this->items);
    }
}
