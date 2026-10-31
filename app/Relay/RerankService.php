<?php
declare(strict_types=1);

namespace Ltzy\Relay;

use Ltzy\Billing\Billing;
use Ltzy\Domain\Channel\Channel;
use Ltzy\Domain\Channel\ChannelSourceInterface;
use Ltzy\Domain\User\UserRepository;
use Ltzy\Support\Ctx;
use Ltzy\Support\Json;
use Ltzy\Support\Logger;

/**
 * Rerank 转发（/v1/rerank）。
 *
 * ── 为什么需要单独一个服务类 ──
 *
 * Rerank 既不是 chat 也不是 embedding：
 *
 *   Chat:      {model, messages:[...]}                    → {choices:[...]}
 *   Embedding: {model, input:"..."}                       → {data:[{embedding:[...]}]}
 *   Rerank:    {model, query:"...", documents:["a","b"]}  → {results:[{index,relevance_score}]}
 *
 * 三种结构完全不兼容，硬塞进 RelayService 会长出三套分支。
 *
 * ── 接口是 Jina / Cohere 风格，不是 OpenAI 标准 ──
 *
 * OpenAI 没有 rerank 标准。多数国内平台用的是
 * Jina AI 那套约定：
 *
 *   请求  {"model":"bge-reranker-v2-m3","query":"苹果","documents":["苹果手机","香蕉"]}
 *   响应  {"model":"...","usage":{...},"results":[
 *            {"index":0,"document":{"text":"苹果手机"},"relevance_score":0.98},
 *            {"index":1,"document":{"text":"香蕉"},    "relevance_score":0.01}
 *          ]}
 *
 * 两个容易踩的点（2026-10-29 实测确认）：
 *   1. `results[].document` 是**对象** `{"text": "..."}`，不是裸字符串；
 *   2. `results` 已按 relevance_score 降序排好，`index` 指回原始 documents 下标。
 *
 * 因为字段差异是"约定"而非"协议"级别，这里**不做结构转换**，
 * 直接把上游响应原样透传 —— 转换反而容易漏字段（比如有的平台还有
 * `document` 里带 `id`/`metadata` 的情况）。
 */
final class RerankService
{
    public function __construct(
        private readonly ChannelSourceInterface $channels,
        private readonly KeyPoolManager $pools,
        private readonly Billing $billing,
        private readonly UserRepository $users,
        private readonly int $timeout = 300,
        private readonly int $retryTimes = 3,
    ) {
    }

    /**
     * 执行一次 rerank 请求。
     *
     * @param string $model
     * @param string $query
     * @param array  $documents 字符串数组，或 [{text:...}] 对象数组
     * @param array  $extra     其他透传参数（top_n / return_documents 等）
     */
    public function create(string $model, string $query, array $documents, array $extra = []): array
    {
        $group = Ctx::str('group', 'default');
        $userId = Ctx::int('user_id');
        $user = $userId > 0 ? $this->users->findById($userId) : null;
        $isAdmin = $user !== null && $user->isAdmin();

        // 与 embedding 一致，走同一套收费判定
        $charge = $this->billing->charge($model, $group, $userId, $isAdmin);
        if (!$charge['allowed']) {
            return ['ok' => false, 'message' => $charge['reason'], 'code' => 'model_unavailable', 'status' => 404];
        }
        Ctx::set('charge_quota', $charge['quota']);

        $candidates = $this->channels->findForModel($model, $group);
        if ($candidates === []) {
            return [
                'ok'      => false,
                'message' => "模型 {$model} 不存在或暂不可用",
                'code'    => 'model_unavailable',
                'status'  => 404,
            ];
        }

        $lastError = null;
        $exhausted = [];
        $t0 = microtime(true);

        for ($i = 0; $i < max(1, $this->retryTimes); $i++) {
            $picked = $this->pick($candidates, $exhausted);
            if ($picked === null) {
                break;
            }
            [$channel, $key] = $picked;

            $result = $this->attempt($channel, $key, $model, $query, $documents, $extra);
            if ($result['ok']) {
                $this->settle($model, $isAdmin, $userId, $channel->id, $t0);
                return $result;
            }
            $lastError = $result;
        }

        return $lastError ?? ['ok' => false, 'message' => '服务暂时不可用', 'code' => 'unavailable', 'status' => 503];
    }

    /** @return array{0:Channel,1:string}|null */
    private function pick(array $candidates, array &$exhausted): ?array
    {
        foreach ($candidates as $ch) {
            if (isset($exhausted[$ch->id])) {
                continue;
            }
            $key = $this->pools->for($ch)->pick();
            if ($key !== null) {
                return [$ch, $key];
            }
            $exhausted[$ch->id] = true;
        }

        return null;
    }

    private function attempt(
        Channel $channel,
        string $key,
        string $model,
        string $query,
        array $documents,
        array $extra,
    ): array {
        $pool = $this->pools->for($channel);
        $upstreamModel = $channel->upstreamModel($model);

        Logger::info('relay.rerank_key_picked', [
            'request_id' => Ctx::requestId(),
            'channel_id' => $channel->id,
            'key_fp'     => substr(md5($key), 0, 8),
        ]);

        // 只白名单透传已知参数，避免客户端塞进奇怪字段污染上游请求
        $body = array_merge([
            'model'     => $upstreamModel,
            'query'     => $query,
            'documents' => $documents,
        ], array_intersect_key($extra, array_flip(['top_n', 'return_documents', 'instruction'])));

        $url = Forwarder::buildUrl($channel->baseUrl, '/v1/rerank');

        $result = Forwarder::requestCurl(
            $url,
            [
                'Content-Type'  => 'application/json',
                'Authorization' => 'Bearer ' . $key,
            ],
            Json::encode($body),
            $this->timeout,
        );

        if ($result['status'] !== 200 || ($result['errno'] ?? 0) !== 0) {
            $pool->markFailed($key, $result['status']);
            $err = Forwarder::extractError($result['body']);
            $msg = $err['message'] ?? ('上游返回 HTTP ' . $result['status']);
            Logger::warn('relay.rerank_error', [
                'request_id'  => Ctx::requestId(),
                'channel_id'  => $channel->id,
                'status_code' => $result['status'],
                'error'       => Logger::mask($msg),
            ]);

            return [
                'ok'      => false,
                'message' => $msg,
                'code'    => 'upstream_' . $result['status'],
                'status'  => $result['status'] >= 500 ? 502 : $result['status'],
            ];
        }

        if ($result['body'] === '') {
            return ['ok' => false, 'message' => '上游返回空响应', 'code' => 'empty_body', 'status' => 502];
        }

        Ctx::set('rerank_response', $result['body']);

        return ['ok' => true];
    }

    private function settle(string $model, bool $isAdmin, int $userId, int $channelId, float $t0): void
    {
        $quota = $isAdmin ? 0 : Ctx::int('charge_quota');

        if ($quota > 0 && $userId > 0) {
            try {
                $this->users->consume($userId, $quota);
            } catch (\Throwable $e) {
                Logger::warn('relay.charge_failed', ['error' => $e->getMessage()]);
            }
        }

        $this->billing->writeLog([
            'user_id'     => $userId,
            'token_id'    => Ctx::int('token_id'),
            'channel_id'  => $channelId,
            'model'       => $model,
            'quota'       => $quota,
            'elapsed_ms'  => (int) ((microtime(true) - $t0) * 1000),
            'is_stream'   => false,
            'status_code' => 200,
            'request_id'  => Ctx::requestId(),
            'detail'      => 'rerank',
        ]);
    }
}
