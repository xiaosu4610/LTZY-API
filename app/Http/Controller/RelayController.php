<?php
declare(strict_types=1);

namespace Ltzy\Http\Controller;

use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Relay\PromptPacker;
use Ltzy\Relay\RelayService;
use Ltzy\Support\Ctx;
use Ltzy\Support\Logger;

/**
 * 中继控制器。
 *
 * 保持极薄：解析请求 → 调 RelayService → 按结果决定响应形式。
 * 所有业务判断都在 RelayService 里，这里不做二次加工。
 *
 * 破甲包装（PromptPacker）插在这里而不是 RelayService：
 *   · RelayService 是已验证的主链路，不碰
 *   · 包装是纯请求体改写，在入口做一次就够
 *   · 白名单外的模型 pack() 原样返回，零开销
 */
final class RelayController
{
    public function __construct(
        private readonly RelayService $relay,
        private readonly ?PromptPacker $packer = null,
    ) {
    }

    public function chat(Request $req, Response $resp): void
    {
        $body = $req->json();
        if ($body === null) {
            $resp->openaiError('请求体不是合法 JSON', 400, 'invalid_request_error', 'invalid_json');
            return;
        }

        // ── 破甲包装：红队测试用，白名单外零开销 ──
        if ($this->packer !== null) {
            $body = $this->packer->pack($body);
        }

        $isStream = (bool) ($body['stream'] ?? false);
        $result = $this->relay->chat($body, $resp);

        if (!$result['ok']) {
            // 关键判断：响应头是否已经发出去了？
            //   已经发出去（流式已开始）→ 只能在流内发错误帧
            //   还没发出去 → 可以正常返回 HTTP 错误码
            if ($resp->isStreaming()) {
                $resp->writeEvent(json_encode([
                    'error' => [
                        'message' => $result['message'],
                        'type'    => 'upstream_error',
                        'code'    => $result['code'] ?? '',
                    ],
                ], JSON_UNESCAPED_UNICODE));
                $resp->write('data: [DONE]' . "\n\n");
                $resp->end();
            } else {
                // 错误类型统一成 invalid_request_error。
                // 原来的 authentication_error / permission_error 会泄漏
                // "这个模型存在但你没权限"，与我们中性的 message 冲突。
                $resp->openaiError(
                    $result['message'],
                    $result['status'] ?? 502,
                    'invalid_request_error',
                    $result['code'] ?? ''
                );
            }
            return;
        }

        if ($isStream) {
            // 流式已由 RelayService 逐块写完，这里补结束
            $resp->end();
            return;
        }

        // 非流式：body 由 RelayService 塞进上下文
        $raw = Ctx::str('response_body');
        if ($raw === '') {
            $resp->openaiError('上游返回空响应', 502, 'upstream_error', 'empty_body');
            return;
        }

        // 剥掉破甲模板要求模型输出的会话标记，用户拿到干净内容
        if ($this->packer !== null) {
            $raw = PromptPacker::stripMarkers($raw);
        }
        $resp->jsonRaw($raw);
    }
}
