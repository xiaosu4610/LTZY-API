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
 * Embedding 转发。
 *
 * 与 chat 的区别：
 *   1. 端点固定是 /v1/embeddings
 *   2. 不流式（没有 SSE）
 *   3. 部分上游要求额外参数（如 asymmetric 嵌入模型的 input_type）
 *
 * 所以单独一个服务类，避免把 RelayService 撑得太胖。
 */
final class EmbeddingService
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
     * 执行一次 embedding 请求。
     *
     * @param string       $model
     * @param string|array $input
     * @param array        $extra 其他透传参数（encoding_format、dimensions 等）
     */
    public function create(string $model, string|array $input, array $extra = []): array
    {
        $group = Ctx::str('group', 'default');
        $userId = Ctx::int('user_id');
        $user = $userId > 0 ? $this->users->findById($userId) : null;
        $isAdmin = $user !== null && $user->isAdmin();

        // 权限：embedding 模型同样走收费判定
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

            $result = $this->attempt($channel, $key, $model, $input, $extra);
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

    private function attempt(Channel $channel, string $key, string $model, string|array $input, array $extra): array
    {
        $pool = $this->pools->for($channel);
        $upstreamModel = $channel->upstreamModel($model);

        Logger::info('relay.embedding_key_picked', [
            'request_id' => Ctx::requestId(),
            'channel_id' => $channel->id,
            'key_fp'     => substr(md5($key), 0, 8),
        ]);

        $body = array_merge([
            'model' => $upstreamModel,
            'input' => $input,
        ], array_intersect_key($extra, array_flip(['encoding_format', 'dimensions', 'user'])));

        // ⚠️ 部分上游的 asymmetric 嵌入模型要求显式传 input_type。
        // 实测报错：'input_type' parameter is required for asymmetric models
        // 默认传 query（检索场景），如果客户端明确传了 passage 就用它的。
        if (str_contains(strtolower($channel->baseUrl), 'nvidia')) {
            $body['input_type'] ??= (string) ($extra['input_type'] ?? 'query');
        }

        $url = Forwarder::buildUrl($channel->baseUrl, '/v1/embeddings');

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
            Logger::warn('relay.embedding_error', [
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

        Ctx::set('embedding_response', $result['body']);
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
            'detail'      => 'embedding',
        ]);
    }
}
