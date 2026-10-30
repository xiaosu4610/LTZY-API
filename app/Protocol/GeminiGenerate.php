<?php
declare(strict_types=1);

namespace Ltzy\Protocol;

/**
 * Google Gemini 协议（/v1beta/models/{model}:generateContent）。
 *
 * ── 与 OpenAI Chat 的主要差异 ──
 *
 *   OpenAI                        Gemini
 *   ----------------------------- ------------------------------------
 *   messages[].role: "user"       contents[].role: "user"（相同）
 *   messages[].role: "assistant"  contents[].role: "model"  ← 名字不同
 *   messages[].role: "system"     systemInstruction（独立字段）
 *   content: string               parts: [{text:"..."}]
 *   max_tokens                    generationConfig.maxOutputTokens
 *   stop                          generationConfig.stopSequences
 *   temperature                   generationConfig.temperature
 *   choices[0].message.content    candidates[0].content.parts[0].text
 *   delta.content                 candidates[0].content.parts[0].text（流式同结构）
 *   data: [DONE]                  无结束标记，靠 EOF
 *
 * ── 认证方式也不同 ──
 *
 * Gemini 官方用 ?key=xxx 查询参数或 x-goog-api-key 头，
 * 但我们的网关统一用 Authorization: Bearer，这点在网关层处理。
 */
final class GeminiGenerate implements InboundInterface
{
    public function name(): string
    {
        return 'gemini-generate';
    }

    public function path(): string
    {
        return '/v1beta/models/{model}:generateContent';
    }

    public function parse(array $body): array
    {
        // Gemini 的 model 在 URL 路径里，不在 body 里。
        // 上层会从路径解析后塞进 body['model']，这里做兜底检查。
        $model = (string) ($body['model'] ?? '');
        if ($model === '') {
            throw new ProtocolException('缺少模型名（应从路径解析）', 'missing_model', 400);
        }

        $messages = [];

        // systemInstruction 是独立字段
        $sysInst = $body['systemInstruction'] ?? ($body['system_instruction'] ?? null);
        if (is_array($sysInst)) {
            $text = $this->extractPartsText($sysInst['parts'] ?? []);
            if ($text !== '') {
                $messages[] = ['role' => 'system', 'content' => $text];
            }
        } elseif (is_string($sysInst) && $sysInst !== '') {
            $messages[] = ['role' => 'system', 'content' => $sysInst];
        }

        $contents = $body['contents'] ?? [];
        if (!is_array($contents) || $contents === []) {
            throw new ProtocolException('contents 不能为空', 'missing_contents', 400);
        }

        foreach ($contents as $c) {
            if (!is_array($c)) {
                continue;
            }
            // Gemini 用 "model" 表示 assistant
            $role = (string) ($c['role'] ?? 'user');
            if ($role === 'model') {
                $role = 'assistant';
            }

            $parts = $c['parts'] ?? [];
            $messages[] = [
                'role'    => $role,
                'content' => $this->normalizeParts($parts),
            ];
        }

        $genCfg = $body['generationConfig'] ?? ($body['generation_config'] ?? []);

        return Canonical::request($model, $messages, [
            'stream'      => (bool) ($body['stream'] ?? false),
            'max_tokens'  => isset($genCfg['maxOutputTokens']) ? (int) $genCfg['maxOutputTokens'] : null,
            'temperature' => isset($genCfg['temperature']) ? (float) $genCfg['temperature'] : null,
            'top_p'       => isset($genCfg['topP']) ? (float) $genCfg['topP'] : null,
            'stop'        => $genCfg['stopSequences'] ?? null,
            'tools'       => $body['tools'] ?? null,
            '_origin'     => 'gemini-generate',
        ]);
    }

    /**
     * Gemini 的 parts 数组 → Canonical content。
     *
     * parts 可能包含：
     *   {text: "..."}
     *   {inline_data: {mime_type:"image/png", data:"base64..."}}
     *   {file_data: {mime_type:"...", file_uri:"..."}}
     */
    private function normalizeParts(mixed $parts): string|array
    {
        if (!is_array($parts)) {
            return '';
        }

        $out = [];
        foreach ($parts as $p) {
            if (!is_array($p)) {
                continue;
            }

            if (isset($p['text'])) {
                $out[] = ['type' => 'text', 'text' => (string) $p['text']];
                continue;
            }

            $inline = $p['inline_data'] ?? ($p['inlineData'] ?? null);
            if (is_array($inline)) {
                $out[] = [
                    'type'   => 'image',
                    'source' => [
                        'type'       => 'base64',
                        'media_type' => (string) ($inline['mime_type'] ?? ($inline['mimeType'] ?? 'image/png')),
                        'data'       => (string) ($inline['data'] ?? ''),
                    ],
                ];
                continue;
            }

            $file = $p['file_data'] ?? ($p['fileData'] ?? null);
            if (is_array($file)) {
                $out[] = [
                    'type'   => 'image',
                    'source' => [
                        'type' => 'url',
                        'url'  => (string) ($file['file_uri'] ?? ($file['fileUri'] ?? '')),
                    ],
                ];
            }
        }

        if (count($out) === 1 && $out[0]['type'] === 'text') {
            return $out[0]['text'];
        }
        return $out;
    }

    private function extractPartsText(mixed $parts): string
    {
        if (!is_array($parts)) {
            return '';
        }
        $buf = [];
        foreach ($parts as $p) {
            if (is_array($p) && isset($p['text'])) {
                $buf[] = (string) $p['text'];
            }
        }
        return implode("\n", $buf);
    }

    /**
     * Canonical → Gemini 响应格式。
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
            'candidates' => [[
                'content' => [
                    'role'  => 'model',
                    'parts' => [['text' => $content]],
                ],
                'finishReason' => $this->mapFinishReason((string) $finish),
                'index'        => 0,
            ]],
            'usageMetadata' => [
                'promptTokenCount'     => (int) ($usage['prompt_tokens'] ?? 0),
                'candidatesTokenCount' => (int) ($usage['completion_tokens'] ?? 0),
                'totalTokenCount'      => (int) ($usage['total_tokens'] ?? 0),
            ],
            'modelVersion' => (string) ($canonical['model'] ?? ''),
        ];
    }

    /**
     * Canonical chunk → Gemini 流式帧。
     *
     * Gemini 流式也是 candidates 数组，每帧结构与非流式相同，
     * 没有 [DONE] 标记，靠连接关闭表示结束。
     */
    public function renderChunk(array $canonicalChunk): string
    {
        $delta = $canonicalChunk['choices'][0]['delta']['content'] ?? '';
        if ($delta === '' || $delta === null) {
            return '';
        }

        $payload = [
            'candidates' => [[
                'content' => [
                    'role'  => 'model',
                    'parts' => [['text' => (string) $delta]],
                ],
                'index' => 0,
            ]],
        ];

        return 'data: ' . json_encode($payload, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES) . "\n\n";
    }

    public function renderDone(): string
    {
        // Gemini 没有结束帧
        return '';
    }

    public function renderError(string $message, int $status, string $code = ''): array
    {
        return [
            'error' => [
                'code'    => $status,
                'message' => $message,
                'status'  => $this->grpcStatus($status),
            ],
        ];
    }

    private function mapFinishReason(string $reason): string
    {
        return match ($reason) {
            'length'     => 'MAX_TOKENS',
            'stop'       => 'STOP',
            'tool_calls' => 'OTHER',
            default      => 'STOP',
        };
    }

    private function grpcStatus(int $httpStatus): string
    {
        return match ($httpStatus) {
            400 => 'INVALID_ARGUMENT',
            401 => 'UNAUTHENTICATED',
            403 => 'PERMISSION_DENIED',
            404 => 'NOT_FOUND',
            429 => 'RESOURCE_EXHAUSTED',
            default => 'INTERNAL',
        };
    }
}
