<?php
declare(strict_types=1);

namespace Ltzy\Http\Controller;

use Ltzy\Relay\EmbeddingService;
use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Support\Ctx;

/**
 * Embedding 入口（/v1/embeddings）。
 *
 * ── 为什么单独一个控制器 ──
 *
 * Embedding 和 Chat 的请求/响应结构完全不同：
 *
 *   Chat:      {model, messages:[...]}        → {choices:[{message}]}
 *   Embedding: {model, input:"..."| [...]}    → {data:[{embedding:[...]}]}
 *
 * 而且 embedding 是**非流式**的（没有 stream 概念），
 * 混在 RelayService 里会让那个类长出两套分支。
 *
 * ── 计费 ──
 *
 * Embedding 按 token 计费更合理，但我们的价格表是按次定价，
 * 所以这里沿用「一次调用 = 一次计费」，与 chat 保持一致。
 */
final class EmbeddingController
{
    public function __construct(private readonly EmbeddingService $embeddings)
    {
    }

    /**
     * POST /v1/embeddings
     */
    public function create(Request $req, Response $resp): void
    {
        // 鉴权前置：未登录时先给 401，不能让「缺少 model 字段」这类
        // 参数错误抢在鉴权前面 —— 那样等于允许无凭据探测参数结构，
        // 并且与 /v1/chat/completions 的 401 行为不一致。
        if (Ctx::int('user_id') <= 0 && !Ctx::bool('free_anonymous_enabled')) {
            $resp->openaiError('请先注册并登录后使用', 401, 'invalid_request_error', 'login_required');

            return;
        }

        $body = $req->json();
        if ($body === null) {
            $resp->openaiError('请求体不是合法 JSON', 400, 'invalid_request_error', 'invalid_json');
            return;
        }

        $model = (string) ($body['model'] ?? '');
        if ($model === '') {
            $resp->openaiError('缺少 model 字段', 400, 'invalid_request_error', 'missing_model');
            return;
        }

        $input = $body['input'] ?? null;
        if ($input === null || $input === '' || $input === []) {
            $resp->openaiError('缺少 input 字段', 400, 'invalid_request_error', 'missing_input');
            return;
        }

        // input 可以是字符串，也可以是字符串数组
        if (!is_string($input) && !is_array($input)) {
            $resp->openaiError('input 必须是字符串或字符串数组', 400, 'invalid_request_error', 'bad_input');
            return;
        }

        $result = $this->embeddings->create($model, $input, $body);

        if (!$result['ok']) {
            $resp->openaiError(
                $result['message'] ?? '服务暂时不可用',
                $result['status'] ?? 502,
                'invalid_request_error',
                $result['code'] ?? ''
            );
            return;
        }

        $raw = Ctx::str('embedding_response');
        if ($raw === '') {
            $resp->openaiError('上游返回空响应', 502, 'invalid_request_error', 'empty_body');
            return;
        }

        // Embedding 响应体积大（1536 维浮点数组），直接透传原始 JSON，
        // 避免 decode 再 encode 带来的性能开销和精度问题
        $resp->jsonRaw($raw);
    }
}
