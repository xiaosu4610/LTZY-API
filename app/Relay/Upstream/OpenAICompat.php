<?php
declare(strict_types=1);

namespace Ltzy\Relay\Upstream;

/**
 * OpenAI 协议兼容的上游。
 *
 * 绝大多数上游都走这个 —— 它们的 API 与 OpenAI 兼容，
 * 差异只是 base_url 和少数参数名。
 *
 * 用一份实现覆盖两个上游，而不是各写一份：协议相同就该复用，
 * 两份代码在维护时必然漂移（改了 A 忘了 B）。
 */
final class OpenAICompat implements UpstreamInterface
{
    public function __construct(
        private readonly string $authStyle = 'bearer',
    ) {
    }

    public function prepare(string $baseUrl, string $key, string $path): array
    {
        $url = rtrim($baseUrl, '/') . '/' . ltrim($path, '/');

        $headers = [
            'Content-Type' => 'application/json',
            'Accept'       => 'text/event-stream',
        ];

        $headers += match ($this->authStyle) {
            'bearer' => ['Authorization' => 'Bearer ' . $key],
            'x-api-key' => ['x-api-key' => $key],
            // 极少数上游要求把 key 放在 query，这里交回调用方处理
            default => ['Authorization' => 'Bearer ' . $key],
        };

        return ['url' => $url, 'headers' => $headers];
    }

    public function supportsModelMapping(): bool
    {
        return true;
    }
}
