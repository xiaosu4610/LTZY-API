<?php
declare(strict_types=1);

namespace Ltzy\Support;

/**
 * JSON 工具。
 *
 * 存在的意义：PHP 的 json_decode 在失败时返回 null 而不抛异常，
 * 调用方很容易漏掉错误检查 —— 表现在线上就是"上游返回了数据但解析出来是空的"。
 * 这里把它封成抛异常，强制调用方处理。
 */
final class Json
{
    /**
     * 解析 JSON，失败抛异常。
     *
     * @throws \JsonException
     */
    public static function decode(string $json, bool $assoc = true): mixed
    {
        return json_decode($json, $assoc, 512, JSON_THROW_ON_ERROR);
    }

    /**
     * 尝试解析，失败返回 null（用于容错场景，比如探测上游返回是不是 JSON）。
     */
    public static function tryDecode(string $json, bool $assoc = true): mixed
    {
        try {
            return json_decode($json, $assoc, 512, JSON_THROW_ON_ERROR);
        } catch (\JsonException) {
            return null;
        }
    }

    /**
     * 编码，失败抛异常。
     *
     * JSON_UNESCAPED_UNICODE：中文不转 \uXXXX，日志和响应都更可读。
     * JSON_UNESCAPED_SLASHES：URL 不转 \/。
     */
    public static function encode(mixed $value, int $flags = 0): string
    {
        return json_encode(
            $value,
            JSON_THROW_ON_ERROR | JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES | $flags
        );
    }
}
