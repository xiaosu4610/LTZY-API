<?php
declare(strict_types=1);

namespace Ltzy\Relay\Upstream;

/**
 * 上游适配器接口。
 *
 * 默认实现假设上游兼容 OpenAI 协议，
 * 差异仅在 base_url、认证头格式和少量参数。
 */
interface UpstreamInterface
{
    /**
     * 拼出完整的请求路径与头。
     *
     * @param string $path 形如 /chat/completions
     * @return array{url:string,headers:array<string,string>}
     */
    public function prepare(string $baseUrl, string $key, string $path): array;

    /**
     * 是否需要把 model 名做映射（用户看到的 ↔ 上游真实的）。
     */
    public function supportsModelMapping(): bool;
}
