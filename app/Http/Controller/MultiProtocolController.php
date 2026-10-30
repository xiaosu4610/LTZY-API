<?php
declare(strict_types=1);

namespace Ltzy\Http\Controller;

use Ltzy\Protocol\AnthropicMessages;
use Ltzy\Protocol\GeminiGenerate;
use Ltzy\Protocol\InboundInterface;
use Ltzy\Protocol\OpenAIChat;
use Ltzy\Protocol\OpenAIResponses;
use Ltzy\Protocol\ProtocolException;
use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Relay\RelayService;
use Ltzy\Support\Ctx;
use Ltzy\Support\Logger;

/**
 * 多协议入口。
 *
 * 支持 4 种客户端协议，内部统一转成 Canonical（OpenAI Chat 结构）后交给 RelayService：
 *
 *   /v1/chat/completions               OpenAI Chat（原生）
 *   /v1/responses                      OpenAI Responses
 *   /v1/messages                       Anthropic Claude
 *   /v1beta/models/{m}:generateContent Google Gemini
 *
 * ── 为什么要集中在同一个控制器 ──
 *
 * 四种协议的"解析 → 转发 → 渲染"流程完全一样，差别只在适配器。
 * 写成四个控制器会有大量重复代码，而且容易漏掉某处的日志/计费。
 *
 * ── 流式的处理 ──
 *
 * 上游统一返回 OpenAI 的 SSE 格式时，
 * 所以流式转发时只需要把上游的 chunk 解析成 Canonical chunk，
 * 再用入站协议适配器重新渲染。
 *
 * 当入站就是 OpenAI Chat 时，直接透传上游字节，零转换（性能最好，也最不容易出错）。
 */
final class MultiProtocolController
{
    /** @var array<string,InboundInterface> */
    private array $adapters = [];

    public function __construct(private readonly RelayService $relay)
    {
        foreach ([
            new OpenAIChat(),
            new OpenAIResponses(),
            new AnthropicMessages(),
            new GeminiGenerate(),
        ] as $a) {
            $this->adapters[$a->name()] = $a;
        }
    }

    /**
     * 处理请求。
     *
     * @param string $protocol 协议名：openai-chat / openai-responses / anthropic-messages / gemini-generate
     * @param array  $pathParams 从 URL 路径解析出的参数（如 Gemini 的 model）
     */
    public function handle(string $protocol, Request $req, Response $resp, array $pathParams = []): void
    {
        $adapter = $this->adapters[$protocol] ?? null;
        if ($adapter === null) {
            $resp->json(['error' => ['message' => '不支持的协议: ' . $protocol]], 400);
            return;
        }

        $body = $req->json();
        if ($body === null) {
            $this->sendError($adapter, $resp, '请求体不是合法 JSON', 400, 'invalid_json');
            return;
        }

        // Gemini 的模型名在 URL 里，需要注入到 body
        if ($protocol === 'gemini-generate') {
            $body['model'] = $pathParams['model'] ?? '';
            // Gemini 的流式用 :streamGenerateContent，这里统一处理
            if (($pathParams['action'] ?? '') === 'streamGenerateContent') {
                $body['stream'] = true;
            }
        }

        // ── 解析 ──
        try {
            $canonical = $adapter->parse($body);
        } catch (ProtocolException $e) {
            $this->sendError($adapter, $resp, $e->getMessage(), $e->httpStatus(), $e->errorCode());
            return;
        }

        // ── 转发 ──
        // 把 Canonical 交给 RelayService，它负责选渠道、取密钥、重试
        $result = $this->relay->chatCanonical($canonical, $resp, $adapter);

        if (!$result['ok']) {
            if ($resp->isStreaming()) {
                // 已经开始流式就不能再改 HTTP 状态码了
                $resp->end();
                return;
            }
            $this->sendError(
                $adapter,
                $resp,
                $result['message'] ?? '服务暂时不可用',
                $result['status'] ?? 502,
                $result['code'] ?? ''
            );
            return;
        }

        // ── 渲染 ──
        // 如果 RelayService 已经把原始响应写进了上下文（直通模式），直接用
        $raw = Ctx::str('response_body');
        if ($raw !== '') {
            $decoded = json_decode($raw, true);
            $upstreamEndpoint = Ctx::str('response_endpoint', 'chat');

            // 情形 1：入站 Chat + 上游 chat → 直通原始 JSON
            if ($protocol === 'openai-chat' && $upstreamEndpoint === 'chat') {
                $resp->jsonRaw($raw);
                return;
            }

            // 情形 2：入站 Responses + 上游走 responses 端点 → 直通
            //
            // 但部分上游不返回顶层 output_text（那是 OpenAI 的便利字段，
            // 不是规范必需项）。客户端普遍依赖它，这里补上，
            // 数据来源是 output[].content[].text，不改变任何原有字段。
            if ($protocol === 'openai-responses' && $upstreamEndpoint === 'responses') {
                if (is_array($decoded) && !isset($decoded['output_text'])) {
                    $decoded['output_text'] = $this->extractOutputText($decoded);
                    $resp->json($decoded);
                    return;
                }
                $resp->jsonRaw($raw);
                return;
            }

            // 情形 3：需要翻译（上游是 chat，入站是别的协议）
            if (is_array($decoded)) {
                $resp->json($adapter->render($decoded));
                return;
            }

            $resp->jsonRaw($raw);
            return;
        }

        // 流式已经由 RelayService 用适配器逐帧写出
        $resp->end();
    }

    /**
     * 按协议格式输出错误。
     */
    private function sendError(InboundInterface $adapter, Response $resp, string $message, int $status, string $code): void
    {
        $resp->json($adapter->renderError($message, $status, $code), $status);
    }

    /**
     * 从 Responses 的 output 数组提取纯文本。
     *
     * 结构：output[] 里 type=message 的项，其 content[] 里 type=output_text 的 text。
     * 多个 part 直接拼接（OpenAI 官方也是这么处理多段的）。
     */
    private function extractOutputText(array $response): string
    {
        $buf = [];
        foreach (($response['output'] ?? []) as $item) {
            if (!is_array($item) || ($item['type'] ?? '') !== 'message') {
                continue;
            }
            foreach (($item['content'] ?? []) as $part) {
                if (is_array($part) && ($part['type'] ?? '') === 'output_text') {
                    $buf[] = (string) ($part['text'] ?? '');
                }
            }
        }
        return implode('', $buf);
    }
}
