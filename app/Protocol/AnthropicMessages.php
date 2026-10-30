<?php
declare(strict_types=1);

namespace Ltzy\Protocol;

/**
 * Anthropic Messages 协议（/v1/messages）。
 *
 * ── 与 OpenAI Chat 的主要差异 ──
 *
 *   OpenAI                      Claude
 *   --------------------------- -----------------------------
 *   messages[].role: "system"   顶层 system 字段（不是消息）
 *   content: string | array     content: array of blocks（必须数组）
 *   max_tokens                  必填（OpenAI 可选）
 *   stop                        stop_sequences
 *   choices[0].message.content  content[0].text
 *   delta.content               content_block_delta.delta.text
 *   data: [DONE]                event: message_stop
 *
 * ── 为什么模型名要映射 ──
 *
 * 客户端可能直接用 claude-3-5-sonnet 之类的原始名调我们，
 * 但我们上游没有这些模型。上层会用 model_mapping 做转换，
 * 这里只负责结构转换，不碰模型名。
 */
final class AnthropicMessages implements InboundInterface
{
    public function name(): string
    {
        return 'anthropic-messages';
    }

    public function path(): string
    {
        return '/v1/messages';
    }

    public function parse(array $body): array
    {
        $model = (string) ($body['model'] ?? '');
        if ($model === '') {
            throw new ProtocolException('缺少 model 字段', 'missing_model', 400);
        }

        // Claude 的 max_tokens 是必填项，但有些客户端会漏，给个合理默认
        $maxTokens = isset($body['max_tokens']) ? (int) $body['max_tokens'] : 4096;

        $messages = [];

        // system 是顶层字段，要转成一条 system 消息
        $system = $body['system'] ?? null;
        if ($system !== null) {
            $sysText = '';
            if (is_string($system)) {
                $sysText = $system;
            } elseif (is_array($system)) {
                // [{type:"text", text:"..."}]
                $buf = [];
                foreach ($system as $s) {
                    if (is_array($s) && ($s['type'] ?? '') === 'text') {
                        $buf[] = (string) ($s['text'] ?? '');
                    }
                }
                $sysText = implode("\n", $buf);
            }
            if ($sysText !== '') {
                $messages[] = ['role' => 'system', 'content' => $sysText];
            }
        }

        $rawMessages = $body['messages'] ?? [];
        if (!is_array($rawMessages) || $rawMessages === []) {
            throw new ProtocolException('messages 不能为空', 'missing_messages', 400);
        }

        foreach ($rawMessages as $m) {
            if (!is_array($m) || !isset($m['role'])) {
                continue;
            }
            $messages[] = [
                'role'    => (string) $m['role'],
                'content' => $this->normalizeClaudeContent($m['content'] ?? ''),
            ];
        }

        return Canonical::request($model, $messages, [
            'stream'      => (bool) ($body['stream'] ?? false),
            'max_tokens'  => $maxTokens,
            'temperature' => isset($body['temperature']) ? (float) $body['temperature'] : null,
            'top_p'       => isset($body['top_p']) ? (float) $body['top_p'] : null,
            'stop'        => $body['stop_sequences'] ?? null,
            'tools'       => $this->normalizeTools($body['tools'] ?? null),
            'tool_choice' => $body['tool_choice'] ?? null,
            '_origin'     => 'anthropic-messages',
        ]);
    }

    /**
     * Claude 的 content 是 block 数组：
     *   [{type:"text", text:"..."}]
     *   [{type:"image", source:{type:"base64", media_type:"image/png", data:"..."}}]
     *   [{type:"tool_use", id:"...", name:"...", input:{...}}]
     *   [{type:"tool_result", tool_use_id:"...", content:"..."}]
     */
    private function normalizeClaudeContent(mixed $content): string|array
    {
        if (is_string($content)) {
            return $content;
        }
        if (!is_array($content)) {
            return '';
        }

        $parts = [];
        foreach ($content as $p) {
            if (!is_array($p)) {
                continue;
            }
            $type = $p['type'] ?? '';

            if ($type === 'text') {
                $parts[] = ['type' => 'text', 'text' => (string) ($p['text'] ?? '')];
                continue;
            }

            if ($type === 'image') {
                $src = $p['source'] ?? [];
                $srcType = $src['type'] ?? 'base64';
                if ($srcType === 'base64') {
                    $parts[] = [
                        'type'   => 'image',
                        'source' => [
                            'type'       => 'base64',
                            'media_type' => (string) ($src['media_type'] ?? 'image/png'),
                            'data'       => (string) ($src['data'] ?? ''),
                        ],
                    ];
                } else {
                    $parts[] = [
                        'type'   => 'image',
                        'source' => ['type' => 'url', 'url' => (string) ($src['url'] ?? '')],
                    ];
                }
                continue;
            }

            // tool_use / tool_result 目前先当文本转换，完整工具支持后续再做
            if ($type === 'tool_result') {
                $c = $p['content'] ?? '';
                $text = is_string($c) ? $c : json_encode($c, JSON_UNESCAPED_UNICODE);
                $parts[] = ['type' => 'text', 'text' => '[tool_result] ' . $text];
                continue;
            }
        }

        if (count($parts) === 1 && $parts[0]['type'] === 'text') {
            return $parts[0]['text'];
        }
        return $parts;
    }

    /**
     * Claude tools → OpenAI tools 格式。
     */
    private function normalizeTools(mixed $tools): ?array
    {
        if (!is_array($tools) || $tools === []) {
            return null;
        }

        $out = [];
        foreach ($tools as $t) {
            if (!is_array($t) || !isset($t['name'])) {
                continue;
            }
            $out[] = [
                'type'     => 'function',
                'function' => [
                    'name'        => (string) $t['name'],
                    'description' => (string) ($t['description'] ?? ''),
                    'parameters'  => $t['input_schema'] ?? ['type' => 'object', 'properties' => new \stdClass()],
                ],
            ];
        }
        return $out === [] ? null : $out;
    }

    /**
     * Canonical → Claude Messages 响应。
     */
    public function render(array $canonical): array
    {
        $content = '';
        if (isset($canonical['choices'][0]['message']['content'])) {
            $content = (string) $canonical['choices'][0]['message']['content'];
        }

        $usage = $canonical['usage'] ?? [];
        $finish = $canonical['choices'][0]['finish_reason'] ?? 'stop';

        return [
            'id'            => (string) ($canonical['id'] ?? ('msg_' . bin2hex(random_bytes(12)))),
            'type'          => 'message',
            'role'          => 'assistant',
            'model'         => (string) ($canonical['model'] ?? ''),
            'content'       => [[
                'type' => 'text',
                'text' => $content,
            ]],
            'stop_reason'   => $this->mapFinishReason((string) $finish),
            'stop_sequence' => null,
            'usage'         => [
                'input_tokens'  => (int) ($usage['prompt_tokens'] ?? 0),
                'output_tokens' => (int) ($usage['completion_tokens'] ?? 0),
            ],
        ];
    }

    /**
     * Canonical chunk → Claude SSE 事件。
     */
    public function renderChunk(array $canonicalChunk): string
    {
        $delta = $canonicalChunk['choices'][0]['delta']['content'] ?? '';
        if ($delta === '' || $delta === null) {
            return '';
        }

        $payload = [
            'type'  => 'content_block_delta',
            'index' => 0,
            'delta' => [
                'type' => 'text_delta',
                'text' => (string) $delta,
            ],
        ];

        return 'event: content_block_delta' . "\n"
            . 'data: ' . json_encode($payload, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES) . "\n\n";
    }

    public function renderDone(): string
    {
        $payload = ['type' => 'message_stop'];
        return 'event: message_stop' . "\n"
            . 'data: ' . json_encode($payload, JSON_UNESCAPED_UNICODE) . "\n\n";
    }

    public function renderError(string $message, int $status, string $code = ''): array
    {
        return [
            'type'  => 'error',
            'error' => [
                'type'    => 'invalid_request_error',
                'message' => $message,
            ],
        ];
    }

    private function mapFinishReason(string $reason): string
    {
        return match ($reason) {
            'length'     => 'max_tokens',
            'stop'       => 'end_turn',
            'tool_calls' => 'tool_use',
            default      => 'end_turn',
        };
    }
}
