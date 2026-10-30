<?php
declare(strict_types=1);

namespace Ltzy\Protocol;

/**
 * OpenAI Chat Completions 协议（/v1/chat/completions）。
 *
 * 这是 Canonical 的原生格式，所以 parse/render 几乎是直通 ——
 * 只做两件事：规范化 content（图片可能是 image_url 格式），补默认值。
 */
final class OpenAIChat implements InboundInterface
{
    public function name(): string
    {
        return 'openai-chat';
    }

    public function path(): string
    {
        return '/v1/chat/completions';
    }

    public function parse(array $body): array
    {
        $model = (string) ($body['model'] ?? '');
        if ($model === '') {
            throw new ProtocolException('缺少 model 字段', 'missing_model', 400);
        }

        $rawMessages = $body['messages'] ?? null;
        if (!is_array($rawMessages) || $rawMessages === []) {
            throw new ProtocolException('messages 不能为空', 'missing_messages', 400);
        }

        $messages = [];
        foreach ($rawMessages as $m) {
            if (!is_array($m) || !isset($m['role'])) {
                continue;
            }
            $messages[] = [
                'role'    => (string) $m['role'],
                'content' => Canonical::normalizeContent($m['content'] ?? ''),
            ];
        }

        // 有些客户端用 max_completion_tokens（新字段），统一成 max_tokens
        $maxTokens = $body['max_tokens'] ?? ($body['max_completion_tokens'] ?? null);

        return Canonical::request($model, $messages, [
            'stream'      => (bool) ($body['stream'] ?? false),
            'max_tokens'  => $maxTokens !== null ? (int) $maxTokens : null,
            'temperature' => isset($body['temperature']) ? (float) $body['temperature'] : null,
            'top_p'       => isset($body['top_p']) ? (float) $body['top_p'] : null,
            'stop'        => $body['stop'] ?? null,
            'tools'       => $body['tools'] ?? null,
            'tool_choice' => $body['tool_choice'] ?? null,
            '_origin'     => 'openai-chat',
        ]);
    }

    /**
     * 渲染成 OpenAI Chat 格式。
     *
     * 如果是上游直接返回的原始 JSON（透传场景），调用方会用 jsonRaw 而不是这里。
     * 这个方法用于"我们自己构造响应"的情况（如翻译后的结果）。
     */
    public function render(array $canonical): array
    {
        return $canonical;
    }

    public function renderChunk(array $canonicalChunk): string
    {
        return 'data: ' . json_encode($canonicalChunk, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES) . "\n\n";
    }

    public function renderDone(): string
    {
        return "data: [DONE]\n\n";
    }

    public function renderError(string $message, int $status, string $code = ''): array
    {
        return [
            'error' => [
                'message' => $message,
                'type'    => 'invalid_request_error',
                'param'   => '',
                'code'    => $code,
            ],
        ];
    }
}
