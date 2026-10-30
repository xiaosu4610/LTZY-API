<?php
declare(strict_types=1);

namespace Ltzy\Http\Controller;

use Ltzy\Billing\Billing;
use Ltzy\Domain\Channel\Channel;
use Ltzy\Domain\Channel\ChannelSourceInterface;
use Ltzy\Domain\User\UserRepository;
use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Relay\KeyPoolManager;
use Ltzy\Support\Ctx;
use Ltzy\Support\Logger;

/**
 * 图像生成中继（POST /v1/images/generations）。
 *
 * ── 为什么不复用 RelayService ──
 *
 * RelayService 那一套是围绕「对话」设计的：Canonical 中间格式、SSE 分块、
 * 协议互转（chat / responses / anthropic / gemini）。图片生成的形态完全不同：
 *
 *   · 一次性请求，没有流式（`stream` 参数上游不支持，传了也会被忽略）
 *   · 响应是 `{created, data:[{url}|{b64_json}]}`，不需要跨协议翻译
 *   · 计费口径是「按张/按次」，跟 token 无关
 *
 * 硬塞进 RelayService 会让那个类里到处是 `if (isImage)` 分支，
 * 反而更容易在对话主路径上引入回归。所以单独一个控制器，
 * 复用的是底层的鉴权上下文（Ctx）、计费（Billing）、密钥池（KeyPool）、渠道源。
 *
 * ── 计费口径 ──
 *
 * 上游是「按次计费」，一次请求一张图算一次钱，`n>1` 上游也按次收。
 * 所以这里**只按请求次数收一次**，不乘 n。
 * 但要限制 n 的上限：不然有人一次要来 20 张，上游成本没变，
 * 我们的响应体积和超时风险却会爆。
 *
 * ── 失败不计费 ──
 *
 * 上游 4xx/5xx/超时一律不扣用户额度（用户明确要求「失败不计费」）。
 * 只有拿到 2xx 且响应体非空才结算。
 */
final class ImageController
{
    private const MAX_N = 4;

    public function __construct(
        private readonly ChannelSourceInterface $channels,
        private readonly KeyPoolManager $pools,
        private readonly Billing $billing,
        private readonly UserRepository $users,
        private readonly string $path = '/v1/images/generations',
        private readonly int $timeout = 300,
        private readonly int $retryTimes = 3,
    ) {
    }

    public function generate(Request $req, Response $resp): void
    {
        $body = $req->json();
        if ($body === null) {
            $resp->openaiError('请求体不是合法 JSON', 400, 'invalid_request_error', 'invalid_json');
            return;
        }

        $model = trim((string) ($body['model'] ?? ''));
        if ($model === '') {
            $resp->openaiError('缺少 model 字段', 400, 'invalid_request_error', 'missing_model');
            return;
        }

        $prompt = trim((string) ($body['prompt'] ?? ''));
        if ($prompt === '') {
            $resp->openaiError('缺少 prompt 字段', 400, 'invalid_request_error', 'missing_prompt');
            return;
        }

        // n 上限：上游按次收费，我们按次收费，n 只影响响应大小和耗时
        $n = (int) ($body['n'] ?? 1);
        if ($n < 1) {
            $n = 1;
        }
        if ($n > self::MAX_N) {
            $resp->openaiError(
                'n 最大支持 ' . self::MAX_N,
                400,
                'invalid_request_error',
                'invalid_n'
            );
            return;
        }
        $body['n'] = $n;

        $group    = Ctx::str('group', 'default');
        $userId   = Ctx::int('user_id');
        $tokenId  = Ctx::int('token_id');
        $user     = $userId > 0 ? $this->users->findById($userId) : null;
        $isAdmin  = $user !== null && $user->isAdmin();

        // ── 计费判定 ──
        // 对外话术保持中性：不区分「模型不存在」和「你没被授权」，
        // 否则会泄露存在付费分层这件事。
        $charge = $this->billing->charge($model, $group, $userId, $isAdmin);
        if (!($charge['allowed'] ?? false)) {
            $resp->openaiError(
                (string) ($charge['reason'] ?? "模型 {$model} 不存在或暂不可用"),
                404,
                'invalid_request_error',
                'model_unavailable'
            );
            return;
        }
        $quota = $isAdmin ? 0 : (int) ($charge['quota'] ?? 0);

        // ── 选渠道 ──
        // 必须显式要求支持 images 端点：这个模型可能在别的渠道上只支持 chat，
        // 路由过去会拿到一个「参数不认识」的 4xx。
        $candidates = [];
        foreach ($this->channels->findForModel($model, $group) as $ch) {
            if ($ch->isEnabled() && $ch->supportsEndpoint('images')) {
                $candidates[] = $ch;
            }
        }
        if ($candidates === []) {
            $resp->openaiError(
                "模型 {$model} 不存在或暂不可用",
                404,
                'invalid_request_error',
                'model_unavailable'
            );
            return;
        }

        $t0 = microtime(true);
        $lastError = null;
        $exhausted = [];
        $attempts = max(1, $this->retryTimes);

        for ($i = 0; $i < $attempts; $i++) {
            $picked = $this->pickChannelAndKey($candidates, $exhausted);
            if ($picked === null) {
                break;
            }
            [$channel, $key] = $picked;

            $result = $this->forward($channel, $key, $body);

            if ($result['ok']) {
                // 只有真正拿到图才结算
                $this->settle($model, $channel->id, $userId, $tokenId, $quota, $t0);
                $resp->jsonRaw((string) $result['body']);
                return;
            }

            $lastError = $result;

            // 密钥/渠道层面的问题让密钥池冷却，超时不冷却（可能是单次长任务）
            if (in_array((int) ($result['status'] ?? 0), [401, 403, 429], true)) {
                $this->pools->for($channel)->markFailed($key, (int) $result['status']);
            }
        }

        // ── 全部失败：不扣费，但要留日志 ──
        $this->billing->writeLog([
            'user_id'     => $userId,
            'token_id'    => $tokenId,
            'channel_id'  => 0,
            'model'       => $model,
            'quota'       => 0,
            'elapsed_ms'  => (int) ((microtime(true) - $t0) * 1000),
            'is_stream'   => false,
            'status_code' => (int) ($lastError['status'] ?? 502),
            'request_id'  => Ctx::requestId(),
            'detail'      => 'image_failed: ' . substr((string) ($lastError['message'] ?? ''), 0, 200),
        ]);

        $resp->openaiError(
            (string) ($lastError['message'] ?? '生图服务暂时不可用，请稍后重试'),
            (int) ($lastError['status'] ?? 502),
            'upstream_error',
            (string) ($lastError['code'] ?? 'upstream_unavailable')
        );
    }

    /**
     * 选一个「支持 images 的渠道 + 可用密钥」。
     *
     * @param array<int,Channel> $candidates
     * @param array<int,bool>    $exhausted
     * @return array{0:Channel,1:string}|null
     */
    private function pickChannelAndKey(array $candidates, array &$exhausted): ?array
    {
        foreach ($candidates as $ch) {
            if (isset($exhausted[$ch->id])) {
                continue;
            }
            $key = $this->pools->for($ch)->pick();
            if ($key !== null) {
                return [$ch, $key];
            }
            // 这个渠道的密钥全在冷却
            $exhausted[$ch->id] = true;
            Logger::warn('image.channel_exhausted', [
                'request_id' => Ctx::requestId(),
                'channel_id' => $ch->id,
            ]);
        }
        return null;
    }

    /**
     * 转发到上游。
     *
     * 用 curl 而不是 Swoole 的 Http\Client：
     *   · curl 在 SWOOLE_HOOK_ALL 下是协程化的，不会阻塞
     *   · 图片响应可能几 MB，curl 的 CURLOPT_RETURNTRANSFER 处理大 body 更稳
     *   · 和 Forwarder 里流式那套保持同一技术栈，排查问题不用切两套心智
     *
     * @return array{ok:bool,body?:string,status?:int,code?:string,message?:string}
     */
    private function forward(Channel $channel, string $key, array $body): array
    {
        $url = rtrim($channel->baseUrl, '/') . $this->path;

        $ch = curl_init($url);
        curl_setopt_array($ch, [
            CURLOPT_POST           => true,
            CURLOPT_POSTFIELDS     => json_encode($body, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES),
            CURLOPT_HTTPHEADER     => [
                'Content-Type: application/json',
                'Authorization: Bearer ' . $key,
            ],
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT        => $this->timeout,
            CURLOPT_CONNECTTIMEOUT => 15,
        ]);

        $raw    = curl_exec($ch);
        $status = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
        $errno  = curl_errno($ch);
        $error  = curl_error($ch);

        if ($errno !== 0) {
            Logger::warn('image.upstream_curl_error', [
                'request_id' => Ctx::requestId(),
                'channel_id' => $channel->id,
                'errno'      => $errno,
                'error'      => $error,
            ]);
            return [
                'ok'      => false,
                'status'  => 504,
                'code'    => 'upstream_timeout',
                'message' => '上游响应超时，请稍后重试',
            ];
        }

        if ($status >= 200 && $status < 300 && is_string($raw) && $raw !== '') {
            return ['ok' => true, 'body' => $raw, 'status' => $status];
        }

        // 把上游的错误信息压缩成一句人话，不回传给用户原始报文
        // （原始报文可能含渠道标识、密钥前缀等内部信息）
        $detail = is_string($raw) ? substr($raw, 0, 300) : '';
        Logger::warn('image.upstream_error', [
            'request_id' => Ctx::requestId(),
            'channel_id' => $channel->id,
            'status'     => $status,
            'detail'     => $detail,
        ]);

        return [
            'ok'      => false,
            'status'  => $status >= 400 && $status < 500 ? 502 : ($status ?: 502),
            'code'    => 'upstream_error',
            'message' => '生图失败，请检查提示词或稍后重试',
        ];
    }

    /**
     * 结算：扣用户额度 + 扣令牌额度 + 写日志。
     */
    private function settle(
        string $model,
        int $channelId,
        int $userId,
        int $tokenId,
        int $quota,
        float $t0,
    ): void {
        if ($quota > 0 && $userId > 0) {
            try {
                $this->users->consume($userId, $quota);
                $this->users->consumeToken($tokenId, $quota);
            } catch (\Throwable $e) {
                // 扣费失败不影响已经生成的图 —— 图已经拿到了，
                // 让用户白等都是次要问题，先把响应给出去。
                Logger::warn('image.charge_failed', [
                    'request_id' => Ctx::requestId(),
                    'error'      => $e->getMessage(),
                ]);
            }
        }

        $this->billing->writeLog([
            'user_id'     => $userId,
            'token_id'    => $tokenId,
            'channel_id'  => $channelId,
            'model'       => $model,
            'quota'       => $quota,
            'elapsed_ms'  => (int) ((microtime(true) - $t0) * 1000),
            'is_stream'   => false,
            'status_code' => 200,
            'request_id'  => Ctx::requestId(),
        ]);
    }
}
