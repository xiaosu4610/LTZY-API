<?php
declare(strict_types=1);

namespace Ltzy\Support;

/**
 * 请求上下文。
 *
 * 为什么不用静态属性：Swoole 是常驻内存 + 协程并发，静态属性是进程级的，
 * 会串到别的请求上（A 请求的 user_id 变成 B 请求的）。
 *
 * 为什么用 Coroutine::getContext()：Swoole 为每个协程维护独立的 context 数组，
 * 协程结束自动回收。这是协程环境里保存请求级状态的正确位置。
 */
final class Ctx
{
    public static function set(string $key, mixed $value): void
    {
        $ctx = \Swoole\Coroutine::getContext();
        if ($ctx !== null) {
            $ctx[$key] = $value;
        }
    }

    public static function get(string $key, mixed $default = null): mixed
    {
        $ctx = \Swoole\Coroutine::getContext();
        if ($ctx === null) {
            return $default;
        }
        return $ctx[$key] ?? $default;
    }

    public static function int(string $key, int $default = 0): int
    {
        $v = self::get($key);
        return $v === null ? $default : (int) $v;
    }

    public static function bool(string $key, bool $default = false): bool
    {
        $v = self::get($key);
        return $v === null ? $default : (bool) $v;
    }

    public static function str(string $key, string $default = ''): string
    {
        $v = self::get($key);
        return $v === null ? $default : (string) $v;
    }

    /**
     * 生成本次请求的 ID，并写入上下文。
     *
     * 格式：日期 + 12 位随机。带上日期是为了在日志里一眼看出是哪天的请求。
     */
    public static function initRequestId(): string
    {
        $rid = date('Ymd') . '-' . bin2hex(random_bytes(6));
        self::set('request_id', $rid);
        return $rid;
    }

    public static function requestId(): string
    {
        return self::str('request_id');
    }
}
