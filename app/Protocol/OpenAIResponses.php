<?php
declare(strict_types=1);

namespace Ltzy\Protocol;

/**
 * OpenAI Responses API（/v1/responses）。
 *
 * 这是 OpenAI 2025 年推出的新接口，和 Chat Completions 的主要差异：
 *
 *   Chat               Responses
 *   ------------------ ------------------
 *   messages           input（可以是字符串或数组）
 *   max_tokens         max_output_tokens
 *   choices[].message  output[]（数组，含 message 类型项）
 *   delta.content      response.output_text.delta 事件
 *   无                 previous_response_id（服务端会话）
 *   无                 store（是否保存）
 *
 * 上游支持这个端点，所以能直通时优先直通；
 * 只有入站是 Chat、上游只认 Responses 时才需要转换。
 *
 * 这里实现的是「Responses 入站 → Canonical」方向，
 * 让客户端用 Responses 协议也能访问我们的模型。
 */
final class OpenAIResponses implements InboundInterface
{
    public function name(): string
    {
        return 'openai-responses';
    }

    public function path(): string
    {
        return '/v1/responses';
    }

    public function parse(array $body): array
    {
        $model = (string) ($body['model'] ?? '');
        if ($model === '') {
            throw new ProtocolException('缺少 model 字段', 'missing_model', 400);
        }

        // Responses 的 input 有两种形态：
        //   1. 纯字符串："你好"
        //   2. 消息数组：[{role:"user", content:[{type:"input_text",text:"..."}]}]
        $input = $body['input'] ?? null;
        if ($input === null) {
            throw new ProtocolException('缺少 input 字段', 'missing_input', 400);
        }

        $messages = [];

        if (is_string($input)) {
            $messages[] = ['role' => 'user', 'content' => $input];
        } elseif (is_array($input)) {
            // 判断是消息数组还是内容块数组
            $first = $input[0] ?? null;

            if (is_array($first) && isset($first['role'])) {
                // 消息数组
                foreach ($input as $m) {
                    if (!is_array($m) || !isset($m['role'])) {
                        continue;
                    }
                    $role = (string) $m['role'];
                    // Responses 的 system 叫 instructions 或 developer
                    if ($role === 'developer') {
                        $role = 'system';
                    }
                    $messages[] = [
                        'role'    => $role,
                        'content' => $this->normalizeInputContent($m['content'] ?? ''),
                    ];
                }
            } else {
                // 内容块数组，当作单条 user 消息
                $messages[] = ['role' => 'user', 'content' => $this->normalizeInputContent($input)];
            }
        } else {
            throw new ProtocolException('input 格式不合法', 'bad_input', 400);
        }

        // instructions 等价于 system prompt
        $instructions = $body['instructions'] ?? null;
        if (is_string($instructions) && $instructions !== '') {
            array_unshift($messages, ['role' => 'system', 'content' => $instructions]);
        }

        return Canonical::request($model, $messages, [
            'stream'      => (bool) ($body['stream'] ?? false),
            'max_tokens'  => isset($body['max_output_tokens']) ? (int) $body['max_output_tokens'] : null,
            'temperature' => isset($body['temperature']) ? (float) $body['temperature'] : null,
            'top_p'       => isset($body['top_p']) ? (float) $body['top_p'] : null,
            'tools'       => $body['tools'] ?? null,
            'tool_choice' => $body['tool_choice'] ?? null,
            '_origin'     => 'openai-responses',
            // 保留原始请求体：上游支持 /v1/responses 时直接转发它，
            // 这样 tools、reasoning、metadata 等我们还没适配的字段也不会丢。
            '_raw'        => $body,
        ]);
    }

    /**
     * 把 Responses 的内容块规范化。
     *
     * Responses 的类型名和 Chat 不同：
     *   input_text  → text
     *   input_image → image
     *   output_text → text
     */
    private function normalizeInputContent(mixed $content): string|array
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

            if ($type === 'input_text' || $type === 'output_text' || $type === 'text') {
                $parts[] = ['type' => 'text', 'text' => (string) ($p['text'] ?? '')];
                continue;
            }

            if ($type === 'input_image' || $type === 'image_url') {
                $url = $p['image_url'] ?? ($p['url'] ?? '');
                if (is_array($url)) {
                    $url = $url['url'] ?? '';
                }
                if (is_string($url) && $url !== '') {
                    if (preg_match('#^data:([^;]+);base64,(.*)$#s', $url, $m)) {
                        $parts[] = [
                            'type'   => 'image',
                            'source' => ['type' => 'base64', 'media_type' => $m[1], 'data' => $m[2]],
                        ];
                    } else {
                        $parts[] = ['type' => 'image', 'source' => ['type' => 'url', 'url' => $url]];
                    }
                }
                continue;
            }
        }

        if (count($parts) === 1 && $parts[0]['type'] === 'text') {
            return $parts[0]['text'];
        }
        return $parts;
    }

    /**
     * Canonical → Responses 格式。
     */
    public function render(array $canonical): array
    {
        $content = '';
        if (isset($canonical['choices'][0]['message']['content'])) {
            $content = (string) $canonical['choices'][0]['message']['content'];
        } elseif (isset($canonical['output_text'])) {
            $content = (string) $canonical['output_text'];
        }

        $usage = $canonical['usage'] ?? [];

        return [
            'id'         => (string) ($canonical['id'] ?? ('resp_' . bin2hex(random_bytes(12)))),
            'object'     => 'response',
            'created_at' => (int) ($canonical['created'] ?? time()),
            'model'      => (string) ($canonical['model'] ?? ''),
            'status'     => 'completed',
            'output'     => [[
                'type'    => 'message',
                'id'      => 'msg_' . bin2hex(random_bytes(12)),
                'role'    => 'assistant',
                'status'  => 'completed',
                'content' => [[
                    'type'        => 'output_text',
                    'text'        => $content,
                    'annotations' => [],
                ]],
            ]],
            'output_text' => $content,
            'usage'       => [
                'input_tokens'  => (int) ($usage['prompt_tokens'] ?? 0),
                'output_tokens' => (int) ($usage['completion_tokens'] ?? 0),
                'total_tokens'  => (int) ($usage['total_tokens'] ?? 0),
            ],
        ];
    }

    /**
     * Canonical chunk → Responses SSE 事件。
     *
     * Responses 的流式事件名和 Chat 完全不同，需要按官方规范发：
     *   response.created
     *   response.output_item.added
     *   response.output_text.delta   ← 正文增量
     *   response.output_text.done
     *   response.completed
     */
    public function renderChunk(array $canonicalChunk): string
    {
        $delta = $canonicalChunk['choices'][0]['delta']['content'] ?? '';
        if ($delta === '' || $delta === null) {
            return '';
        }

        $payload = [
            'type'  => 'response.output_text.delta',
            'delta' => (string) $delta,
        ];

        return 'event: response.output_text.delta' . "\n"
            . 'data: ' . json_encode($payload, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES) . "\n\n";
    }

    public function renderDone(): string
    {
        $payload = ['type' => 'response.completed'];
        return 'event: response.completed' . "\n"
            . 'data: ' . json_encode($payload, JSON_UNESCAPED_UNICODE) . "\n\n";
    }

    public function renderError(string $message, int $status, string $code = ''): array
    {
        return [
            'error' => [
                'message' => $message,
                'type'    => 'invalid_request_error',
                'code'    => $code,
            ],
        ];
    }
}
