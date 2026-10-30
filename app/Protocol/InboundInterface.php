<?php
declare(strict_types=1);

namespace Ltzy\Protocol;

/**
 * 入站协议适配器接口。
 *
 * 职责：把「客户端发来的某种协议格式」翻译成 Canonical，
 * 以及把 Canonical 响应翻译回「该协议格式」。
 *
 * 实现类：
 *   OpenAIChat         /v1/chat/completions   （原生，零转换）
 *   OpenAIResponses    /v1/responses          （新版 Responses API）
 *   AnthropicMessages  /v1/messages           （Claude 官方）
 *   GeminiGenerate     /v1beta/models/{m}:generateContent （Gemini 官方）
 */
interface InboundInterface
{
    /**
     * 协议名，用于日志和错误信息。
     */
    public function name(): string;

    /**
     * 该协议对应的路径（支持占位符，如 /v1beta/models/{model}:generateContent）。
     */
    public function path(): string;

    /**
     * 把原始请求体解析成 Canonical 请求。
     *
     * @param array $body 已 json_decode 的请求体
     * @return array Canonical 请求
     * @throws ProtocolException 参数不合法时抛
     */
    public function parse(array $body): array;

    /**
     * 把 Canonical 响应渲染成该协议格式。
     */
    public function render(array $canonical): array;

    /**
     * 把 Canonical 的流式 chunk 渲染成该协议的 SSE 帧。
     *
     * @return string 完整的一帧（含 "event: ..." / "data: ..." / 空行）
     */
    public function renderChunk(array $canonicalChunk): string;

    /**
     * 流结束标记（如 OpenAI 的 "data: [DONE]"，Claude 的 "message_stop"）。
     */
    public function renderDone(): string;

    /**
     * 错误响应的格式。
     */
    public function renderError(string $message, int $status, string $code = ''): array;
}
