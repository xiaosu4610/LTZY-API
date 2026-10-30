<?php
declare(strict_types=1);

namespace Ltzy\Http\Controller;

use Ltzy\Relay\AudioService;
use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Support\Ctx;

/**
 * 音频端点入口：/v1/audio/transcriptions（ASR）与 /v1/audio/speech（TTS）。
 *
 * 两者放同一个控制器，因为它们共享同一个 Service，而且都是音频域；
 * 但请求/响应形态完全不同，所以是两个方法，不合并。
 */
final class AudioController
{
    public function __construct(private readonly AudioService $audio)
    {
    }

    /**
     * 鉴权前置检查。
     *
     * 为什么放在参数校验之前：未登录请求如果先收到「缺少 model 字段」，
     * 等于让没有凭据的人探测参数结构；而且和 /v1/chat/completions
     * 的 401 行为不一致，用户会误以为端点坏了。
     *
     * @return bool true = 已拒绝，调用方直接 return
     */
    private function rejectAnonymous(Response $resp): bool
    {
        if (Ctx::int('user_id') <= 0 && !Ctx::bool('free_anonymous_enabled')) {
            $resp->openaiError('请先注册并登录后使用', 401, 'invalid_request_error', 'login_required');

            return true;
        }

        return false;
    }

    /**
     * POST /v1/audio/transcriptions —— 语音识别。
     *
     * 客户端必须用 multipart/form-data，带 file 和 model 两个字段。
     * 用 JSON 会被上游拒（400 不支持 Content-Type），这里提前给出明确提示，
     * 省得用户拿上游的报错去猜。
     */
    public function transcriptions(Request $req, Response $resp): void
    {
        if ($this->rejectAnonymous($resp)) {
            return;
        }

        $files = $req->filesAll();
        if ($files === []) {
            $resp->openaiError(
                '缺少上传文件。请用 multipart/form-data 传 file 字段（音频文件）',
                400,
                'invalid_request_error',
                'missing_file'
            );

            return;
        }

        // 优先用名为 file 的字段，没有就取第一个
        $file = $files['file'] ?? reset($files);
        $form = $req->postAll();

        $model = trim((string) ($form['model'] ?? ''));
        if ($model === '') {
            $resp->openaiError('缺少 model 字段', 400, 'invalid_request_error', 'missing_model');

            return;
        }

        $result = $this->audio->transcribe($model, $file, $form);

        if (!$result['ok']) {
            $resp->openaiError(
                $result['message'] ?? '服务暂时不可用',
                $result['status'] ?? 502,
                'invalid_request_error',
                $result['code'] ?? ''
            );

            return;
        }

        // ASR 返回的是 JSON（结构随模型略有差异），原样透传
        $resp->jsonRaw((string) $result['body']);
    }

    /**
     * POST /v1/audio/speech —— 语音合成。
     *
     * JSON 进、二进制音频出。
     */
    public function speech(Request $req, Response $resp): void
    {
        if ($this->rejectAnonymous($resp)) {
            return;
        }

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

        // OpenAI 的字段名叫 input，但不少客户端按「文本转语音」的直觉传 text。
        // 两个都收，统一成 input 往上游发（上游实测只认 input：
        // 传 text 会报「请求参数缺少必填字段 input」）。
        if (!isset($body['input']) && isset($body['text'])) {
            $body['input'] = $body['text'];
        }

        if (!isset($body['input']) || !is_string($body['input']) || trim($body['input']) === '') {
            $resp->openaiError(
                '缺少 input 字段（要合成的文本）',
                400,
                'invalid_request_error',
                'missing_input'
            );

            return;
        }

        $body['model'] = $model;
        $result = $this->audio->speech($body);

        if (!$result['ok']) {
            $resp->openaiError(
                $result['message'] ?? '服务暂时不可用',
                $result['status'] ?? 502,
                'invalid_request_error',
                $result['code'] ?? ''
            );

            return;
        }

        $resp->binary(
            (string) $result['body'],
            (string) ($result['content_type'] ?? 'application/octet-stream')
        );
    }
}
