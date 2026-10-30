<?php
declare(strict_types=1);

namespace Ltzy\Support;

/**
 * 结构化日志。
 *
 * 为什么不用 monolog：只需要写日志这一个功能，引入一个包不划算；
 * 而且 Swoole 常驻内存 + 协程环境下，日志库的内部锁可能成为瓶颈。
 *
 * 为什么结构化（JSON 字段而不是拼字符串）：出问题时想按 request_id
 * 把一次请求的所有记录捞出来，拼字符串的日志只能 grep，结构化可以直接过滤。
 */
final class Logger
{
    private const LEVELS = ['debug' => 0, 'info' => 1, 'warn' => 2, 'error' => 3];

    private static int $minLevel = 1;
    private static bool $toStdout = true;
    private static ?string $file = null;

    public static function init(string $level, bool $toStdout, ?string $file = null): void
    {
        self::$minLevel = self::LEVELS[strtolower($level)] ?? 1;
        self::$toStdout = $toStdout;
        self::$file = $file;
    }

    public static function debug(string $event, array $ctx = []): void
    {
        self::write('debug', $event, $ctx);
    }

    public static function info(string $event, array $ctx = []): void
    {
        self::write('info', $event, $ctx);
    }

    public static function warn(string $event, array $ctx = []): void
    {
        self::write('warn', $event, $ctx);
    }

    public static function error(string $event, array $ctx = []): void
    {
        self::write('error', $event, $ctx);
    }

    private static function write(string $level, string $event, array $ctx): void
    {
        if (self::LEVELS[$level] < self::$minLevel) {
            return;
        }

        $record = [
            'ts'    => date('c'),
            'level' => $level,
            'event' => $event,
        ] + $ctx;

        // 协程 ID 放进日志：同一个 request_id 下如果有多个协程，
        // 靠它才能分清哪条是哪条。
        if (class_exists(\Swoole\Coroutine::class, false)) {
            $cid = \Swoole\Coroutine::getCid();
            if ($cid > 0) {
                $record['cid'] = $cid;
            }
        }

        $line = json_encode($record, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES);

        if (self::$toStdout) {
            fwrite(STDOUT, $line . "\n");
        }
        if (self::$file !== null) {
            // FILE_APPEND | LOCK_EX：多进程写同一文件时靠 flock 串行化，
            // 不加锁会互相覆盖（日志出现半行）。
            @file_put_contents(self::$file, $line . "\n", FILE_APPEND | LOCK_EX);
        }
    }

    /**
     * 把可能含密钥的字符串脱敏。
     *
     * 上游的错误响应经常把密钥原样回显（"Incorrect API key provided: sk-abc..."），
     * 直接进日志就等于把密钥写进磁盘。凡是要落日志的外部错误信息都该过一遍。
     */
    public static function mask(string $text): string
    {
        // 形如 sk-xxx / nvapi-xxx / 长随机串
        $text = preg_replace('/\b(sk-|nvapi-|sk-proj-)([A-Za-z0-9_\-]{6})[A-Za-z0-9_\-]{6,}/',
            '$1$2***', $text);
        $text = preg_replace('/\b([A-Za-z0-9]{8})[A-Za-z0-9]{24,}/', '$1***', $text);
        return $text;
    }
}
