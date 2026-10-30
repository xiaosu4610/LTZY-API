<?php
declare(strict_types=1);

namespace Ltzy\Http\Controller;

use Ltzy\Relay\RerankService;
use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Support\Ctx;

/**
 * Rerank 入口（/v1/rerank）。
 *
 * 接口采用 Jina / Cohere 约定，不是 OpenAI 标准（OpenAI 至今没有 rerank）。
 * 详见 RerankService 的类注释。
 *
 * 与 EmbeddingController 的分工完全对称：
 *   - 这里只做「参数校验 + 结果透传」
 *   - 渠道选择、重试、计费、日志全在 Service 层
 */
final class RerankController
{
    public function __construct(private readonly RerankService $reranks)
    {
    }

    /**
     * POST /v1/rerank
     */
    public function create(Request $req, Response $resp): void
    {
        // 鉴权必须先于参数校验。
        // 否则未登录请求会先收到「缺少 model 字段」这类 400，
        // 等于让攻击者在没有凭据的情况下探测参数结构，
        // 而且和 /v1/chat/completions 的 401 行为不一致（用户会以为坏了）。
        if (Ctx::int('user_id') <= 0 && !Ctx::bool('free_anonymous_enabled')) {
            $resp->openaiError(
                '请先注册并登录后使用',
                401,
                'invalid_request_error',
                'login_required'
            );

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

        // query：Jina 风格叫 query，Cohere 风格叫 query 也兼容；
        // 少数客户端会用 "input"，一并接受。
        $query = $body['query'] ?? $body['input'] ?? null;
        if (!is_string($query) || trim($query) === '') {
            $resp->openaiError('缺少 query 字段（字符串）', 400, 'invalid_request_error', 'missing_query');

            return;
        }

        $documents = $body['documents'] ?? null;
        if (!is_array($documents) || $documents === []) {
            $resp->openaiError('缺少 documents 字段（非空数组）', 400, 'invalid_request_error', 'missing_documents');

            return;
        }

        // documents 既可以是 ["a","b"]，也可以是 [{"text":"a"},{"text":"b"}]
        // 两种都是业界常见写法，这里都放行，原样透传上游。
        foreach ($documents as $d) {
            if (!is_string($d) && !is_array($d)) {
                $resp->openaiError(
                    'documents 的元素必须是字符串或 {text:...} 对象',
                    400,
                    'invalid_request_error',
                    'bad_documents'
                );

                return;
            }
        }

        $result = $this->reranks->create($model, $query, $documents, $body);

        if (!$result['ok']) {
            $resp->openaiError(
                $result['message'] ?? '服务暂时不可用',
                $result['status'] ?? 502,
                'invalid_request_error',
                $result['code'] ?? ''
            );

            return;
        }

        $raw = Ctx::str('rerank_response');
        if ($raw === '') {
            $resp->openaiError('上游返回空响应', 502, 'invalid_request_error', 'empty_body');

            return;
        }

        // 原样透传：不同平台的字段差异（document 是对象还是字符串、
        // 有没有 usage、有没有 index）全靠透传保住，转换必丢信息。
        $resp->jsonRaw($raw);
    }
}
