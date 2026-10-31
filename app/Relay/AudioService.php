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
 * 音频端点转发：ASR（语音识别）+ TTS（语音合成）。
 *
 * ── 和 chat / embedding / rerank 的本质区别 ──
 *
 * 前三种都是「JSON 进、JSON 出」，这里两个不是：
 *
 *   ASR  /v1/audio/transcriptions  multipart 进、JSON 出
 *   TTS  /v1/audio/speech          JSON 进、**二进制音频**出
 *
 * 所以响应侧不能走 jsonRaw()，必须走 Response::binary() 并把
 * 上游的音频字节原样吐给客户端。
 *
 * ── 摸力方舟的实测事实（2026-10-29）──
 *
 *   1. ASR 必须 multipart。用 application/json 会收到
 *      「400 不支持 Content-Type 'application/json'，请检查请求头」——
 *      这个报错说明接口存在，只是格式不对，跟「暂不支持该接口」要区分开。
 *   2. ASR 的两个模型返回结构略有差异：
 *        SenseVoiceSmall → {"text":"", "language":"auto"}
 *        GLM-ASR         → {"text":""}
 *      所以响应一律透传，不做 schema 归一化。
 *   3. TTS 的 /v1/audio/speech 是通的（ChatTTS 实测返回 35406 字节真 WAV），
 *      但 Spark-TTS-0.5B 恒报「暂不支持该接口」—— 那是模型侧没挂接口，
 *      不是我们调用方式的问题。别为此反复调参数。
 */
final class AudioService
{
    /** 允许的音频文件大小上限（字节）。8MB，与 nginx client_max_body_size 对齐 */
    private const MAX_FILE_BYTES = 8 * 1024 * 1024;

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
     * 语音识别。
     *
     * @param array{name:string,type:string,tmp_name:string,size:int} $file 上传的音频
     * @param array                                                   $fields 其他表单字段（language、prompt 等）
     */
    public function transcribe(string $model, array $file, array $fields = []): array
    {
        if ($file['size'] > self::MAX_FILE_BYTES) {
            return [
                'ok'      => false,
                'message' => '音频文件超过 8MB 上限',
                'code'    => 'file_too_large',
                'status'  => 413,
            ];
        }

        if (!is_file($file['tmp_name'])) {
            return ['ok' => false, 'message' => '上传文件已失效', 'code' => 'bad_file', 'status' => 400];
        }

        return $this->dispatch('/v1/audio/transcriptions', $model, function (Channel $ch, string $key) use ($model, $file, $fields): array {
            $mime = $file['type'] !== '' ? $file['type'] : 'application/octet-stream';
            $name = $file['name'] !== '' ? $file['name'] : 'audio.bin';

            $form = array_merge(
                array_intersect_key($fields, array_flip(['language', 'prompt', 'response_format', 'temperature'])),
                [
                    'model' => $ch->upstreamModel($model),
                    // CURLFile 让 curl 自己生成 boundary 和 filename，
                    // 比手拼 multipart 少踩一堆编码坑
                    'file'  => new \CURLFile($file['tmp_name'], $mime, $name),
                ]
            );

            return $this->callMultipart(
                Forwarder::buildUrl($ch->baseUrl, '/v1/audio/transcriptions'),
                $key,
                $form,
            );
        });
    }

    /**
     * 语音合成。
     *
     * @param array $body 已校验的请求体（input / voice / response_format 等）
     */
    public function speech(array $body): array
    {
        $model = (string) ($body['model'] ?? '');
        if ($model === '') {
            return ['ok' => false, 'message' => '缺少 model 字段', 'code' => 'missing_model', 'status' => 400];
        }

        $payload = array_merge(
            ['model' => $model],
            array_intersect_key($body, array_flip(['input', 'voice', 'response_format', 'speed', 'instructions']))
        );

        return $this->dispatch('/v1/audio/speech', $model, function (Channel $ch, string $key) use ($payload): array {
            $payload['model'] = $ch->upstreamModel((string) $payload['model']);

            $result = Forwarder::requestCurl(
                Forwarder::buildUrl($ch->baseUrl, '/v1/audio/speech'),
                [
                    'Content-Type'  => 'application/json',
                    'Authorization' => 'Bearer ' . $key,
                ],
                Json::encode($payload),
                $this->timeout,
            );

            if ($result['status'] !== 200 || ($result['errno'] ?? 0) !== 0) {
                return $this->fail($ch, $key, $result);
            }
            if ($result['body'] === '') {
                return ['ok' => false, 'message' => '上游返回空音频', 'code' => 'empty_body', 'status' => 502];
            }

            return [
                'ok'           => true,
                'content_type' => self::sniffAudioType($result['body']),
                'body'         => $result['body'],
            ];
        }, binaryOut: true);
    }

    /**
     * 渠道选择 + 重试 + 计费的公共骨架。
     *
     * @param callable(Channel,string):array $attempt
     */
    private function dispatch(string $endpoint, string $model, callable $attempt, bool $binaryOut = false): array
    {
        $group = Ctx::str('group', 'default');
        $userId = Ctx::int('user_id');
        $user = $userId > 0 ? $this->users->findById($userId) : null;
        $isAdmin = $user !== null && $user->isAdmin();

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

            $result = $attempt($channel, $key);
            if (($result['ok'] ?? false) === true) {
                $this->settle($model, $isAdmin, $userId, $channel->id, $t0, $endpoint);

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

    /** @param array<string,mixed> $form */
    private function callMultipart(string $url, string $key, array $form): array
    {
        $result = Forwarder::requestMultipart(
            $url,
            ['Authorization' => 'Bearer ' . $key],
            $form,
            $this->timeout,
        );

        if ($result['status'] !== 200 || ($result['errno'] ?? 0) !== 0) {
            return $this->failRaw($result);
        }
        if ($result['body'] === '') {
            return ['ok' => false, 'message' => '上游返回空响应', 'code' => 'empty_body', 'status' => 502];
        }

        return ['ok' => true, 'body' => $result['body']];
    }

    /** @param array{status:int,body:string,errno:int} $result */
    private function fail(Channel $ch, string $key, array $result): array
    {
        $this->pools->for($ch)->markFailed($key, $result['status']);

        return $this->failRaw($result);
    }

    /** @param array{status:int,body:string} $result */
    private function failRaw(array $result): array
    {
        $err = Forwarder::extractError($result['body']);
        $msg = $err['message'] ?? ('上游返回 HTTP ' . $result['status']);
        Logger::warn('relay.audio_error', [
            'request_id'  => Ctx::requestId(),
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

    /**
     * 从音频字节的魔数推断 MIME。
     *
     * 为什么不用上游的 Content-Type：Forwarder::requestCurl 目前不返回
     * 响应头（改它有风险，Swoole 的 curl hook 已经坑过一次）。
     * 而音频容器格式的魔数非常稳定，判断比问上游更可靠：
     *
     *   RIFF....WAVE  → audio/wav
     *   ID3 或 FFFx   → audio/mpeg
     *   OggS          → audio/ogg
     *   fLaC          → audio/flac
     *   #!AMR         → audio/amr
     */
    private static function sniffAudioType(string $bytes): string
    {
        if (strlen($bytes) < 12) {
            return 'application/octet-stream';
        }

        if (substr($bytes, 0, 4) === 'RIFF' && substr($bytes, 8, 4) === 'WAVE') {
            return 'audio/wav';
        }
        if (substr($bytes, 0, 3) === 'ID3') {
            return 'audio/mpeg';
        }
        // MP3 帧头：11111111 111xxxxx
        if (ord($bytes[0]) === 0xFF && (ord($bytes[1]) & 0xE0) === 0xE0) {
            return 'audio/mpeg';
        }
        if (substr($bytes, 0, 4) === 'OggS') {
            return 'audio/ogg';
        }
        if (substr($bytes, 0, 4) === 'fLaC') {
            return 'audio/flac';
        }
        if (substr($bytes, 0, 5) === '#!AMR') {
            return 'audio/amr';
        }

        return 'application/octet-stream';
    }

    private function settle(string $model, bool $isAdmin, int $userId, int $channelId, float $t0, string $endpoint): void
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
            'detail'      => $endpoint,
        ]);
    }
}
