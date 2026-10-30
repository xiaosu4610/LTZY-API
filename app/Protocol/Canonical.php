<?php
declare(strict_types=1);

namespace Ltzy\Protocol;

/**
 * 统一中间格式（Canonical）。
 *
 * ── 为什么需要一个中间层 ──
 *
 * 客户端可能用 4 种协议，上游可能用 3 种协议，如果两两直连需要 12 个翻译器。
 * 引入中间格式后只需要 4 + 3 = 7 个，而且加一种新协议只写 1 个适配器。
 *
 * ── 为什么选 OpenAI Chat 作为中间格式 ──
 *
 * 1. 它是事实标准，几乎所有网关和客户端都认
 * 2. 结构最简单（messages + 少量参数字段）
 * 3. 上游都是 OpenAI 兼容，直通时零转换
 *
 * ── 字段约定 ──
 *
 * messages 里的图片统一用：
 *   ['type' => 'image', 'source' => ['type' => 'base64'|'url', 'data' => '...'|'url' => '...']]
 *
 * 这样 Claude（base64 + media_type）、Gemini（inline_data / file_data）、
 * OpenAI（image_url）三种格式都能无损映射。
 */
final class Canonical
{
    /**
     * 构造一个标准请求。
     *
     * @param array<int,array{role:string,content:string|array}> $messages
     * @param array $opts 可选参数；其中 `_raw` 用于保留客户端原始请求体，
     *                    让「入站 Responses + 上游支持 Responses」能原生直通
     */
    public static function request(
        string $model,
        array $messages,
        array $opts = [],
    ): array {
        return [
            'model'       => $model,
            'messages'    => $messages,
            'stream'      => (bool) ($opts['stream'] ?? false),
            'max_tokens'  => $opts['max_tokens'] ?? null,
            'temperature' => $opts['temperature'] ?? null,
            'top_p'       => $opts['top_p'] ?? null,
            'stop'        => $opts['stop'] ?? null,
            'tools'       => $opts['tools'] ?? null,
            'tool_choice' => $opts['tool_choice'] ?? null,
            // 元信息：记录来源协议，便于日志和调试
            '_origin'     => $opts['_origin'] ?? 'unknown',
            // 原始请求体：直通时直接发这个，避免 Canonical 往返转换丢字段
            '_raw'        => $opts['_raw'] ?? null,
        ];
    }

    /**
     * 构造标准响应。
     */
    public static function response(
        string $id,
        string $model,
        string $content,
        array $opts = [],
    ): array {
        return [
            'id'      => $id,
            'object'  => 'chat.completion',
            'created' => $opts['created'] ?? time(),
            'model'   => $model,
            'choices' => [[
                'index'         => 0,
                'message'       => [
                    'role'    => 'assistant',
                    'content' => $content,
                ],
                'finish_reason' => $opts['finish_reason'] ?? 'stop',
            ]],
            'usage' => $opts['usage'] ?? [
                'prompt_tokens'     => 0,
                'completion_tokens' => 0,
                'total_tokens'      => 0,
            ],
        ];
    }

    /**
     * 构造流式 chunk。
     */
    public static function chunk(
        string $id,
        string $model,
        string $delta,
        ?string $finishReason = null,
    ): array {
        return [
            'id'      => $id,
            'object'  => 'chat.completion.chunk',
            'created' => time(),
            'model'   => $model,
            'choices' => [[
                'index'         => 0,
                'delta'         => $finishReason === null
                    ? ['content' => $delta]
                    : new \stdClass(),
                'finish_reason' => $finishReason,
            ]],
        ];
    }

    /**
     * 从 Canonical 请求提取纯文本（用于简单场景如标题生成）。
     */
    public static function plainText(array $messages): string
    {
        $out = [];
        foreach ($messages as $m) {
            $c = $m['content'] ?? '';
            if (is_string($c)) {
                $out[] = $c;
            } elseif (is_array($c)) {
                foreach ($c as $part) {
                    if (($part['type'] ?? '') === 'text') {
                        $out[] = (string) ($part['text'] ?? '');
                    }
                }
            }
        }
        return implode("\n", $out);
    }

    /**
     * 规范化 content：把各种格式统一成「字符串 或 标准 part 数组」。
     *
     * Claude 和 Gemini 的 content 结构不同，这里统一。
     */
    public static function normalizeContent(mixed $content): string|array
    {
        if (is_string($content)) {
            return $content;
        }
        if (!is_array($content)) {
            return '';
        }

        // OpenAI 格式：[{type:"text",text:"..."},{type:"image_url",image_url:{url:"..."}}]
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

            // OpenAI image_url
            if ($type === 'image_url') {
                $url = $p['image_url']['url'] ?? '';
                if (is_string($url) && str_starts_with($url, 'data:')) {
                    // data:image/png;base64,xxxx
                    if (preg_match('#^data:([^;]+);base64,(.*)$#s', $url, $m)) {
                        $parts[] = [
                            'type'   => 'image',
                            'source' => ['type' => 'base64', 'media_type' => $m[1], 'data' => $m[2]],
                        ];
                        continue;
                    }
                }
                $parts[] = [
                    'type'   => 'image',
                    'source' => ['type' => 'url', 'url' => $url],
                ];
                continue;
            }

            // 已经是标准格式
            if ($type === 'image') {
                $parts[] = $p;
                continue;
            }
        }

        // 只有一段文本就退化成字符串，让上游少解析一层
        if (count($parts) === 1 && $parts[0]['type'] === 'text') {
            return $parts[0]['text'];
        }

        return $parts;
    }
}
