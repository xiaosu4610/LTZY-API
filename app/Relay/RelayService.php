<?php
declare(strict_types=1);

namespace Ltzy\Relay;

use Ltzy\Billing\Billing;
use Ltzy\Domain\Channel\Channel;
use Ltzy\Domain\Channel\ChannelSourceInterface;
use Ltzy\Domain\User\User;
use Ltzy\Domain\User\UserRepository;
use Ltzy\Http\Response;
use Ltzy\Support\Ctx;
use Ltzy\Support\Json;
use Ltzy\Support\Logger;

/**
 * 中继编排。
 *
 * 职责：把「用户请求」变成「上游请求」，把「上游响应」原样交回客户端。
 * 中间只做四件事：校验权限、选渠道、取密钥、失败换渠道重试。
 *
 * ⚠️ 不在这里做任何响应内容加工 —— 见 Forwarder 的说明。
 */
final class RelayService
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
     * 多协议入口：接收 Canonical 请求，转发，流式时用适配器逐帧渲染。
     *
     * 与 chat() 的区别：
     *   chat()          入站就是 OpenAI Chat，流式直接透传上游字节（零转换）
     *   chatCanonical() 入站是 Responses / Claude / Gemini，流式需要逐帧翻译
     *
     * @param array            $canonical
     * @param Response         $resp
     * @param \Ltzy\Protocol\InboundInterface|null $adapter 入站适配器；为 null 时按 OpenAI Chat 处理
     */
    public function chatCanonical(array $canonical, Response $resp, ?object $adapter = null): array
    {
        $model = (string) ($canonical['model'] ?? '');
        if ($model === '') {
            return ['ok' => false, 'message' => '缺少 model 字段', 'code' => 'missing_model', 'status' => 400];
        }

        $isStream = (bool) ($canonical['stream'] ?? false);
        $isNativeChat = ($adapter === null || $adapter->name() === 'openai-chat');

        $group = Ctx::str('group', 'default');
        $userId = Ctx::int('user_id');
        $tokenId = Ctx::int('token_id');
        $user = $userId > 0 ? $this->users->findById($userId) : null;

        $deny = $this->checkPermission($model, $user);
        if ($deny !== null) {
            return $deny;
        }
        $isAdmin = $user !== null && $user->isAdmin();

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
        $attempts = max(1, $this->retryTimes);
        $exhausted = [];
        $t0 = microtime(true);
        // 600 秒总预算，所有重试共享这一个 deadline。
        //
        // ⚠️ 不能用「$attempts × $this->timeout」来算 —— 那是 3 × 600 = 1800 秒。
        // 前端看到的必须是「最多等 600 秒就一定有结果」，
        // 所以预算在循环外定义一次，每次尝试只领剩余时间。
        $deadline = $t0 + $this->timeout;

        for ($i = 0; $i < $attempts; $i++) {
            if ($resp->isStreaming()) {
                break;
            }

            // 预算用尽立刻收口，不再发起新的上游连接。
            $budget = (int) ($deadline - microtime(true));
            if ($budget <= 0) {
                break;
            }

            $picked = $this->pickChannelAndKey($candidates, $exhausted);
            if ($picked === null) {
                break;
            }
            [$channel, $key] = $picked;

            $result = $this->attemptCanonical($channel, $key, $canonical, $resp, $isStream, $isNativeChat, $adapter, $budget);
            if ($result['ok']) {
                $this->settle($model, $isAdmin, $userId, $tokenId, $channel->id, $isStream, $t0);
                return $result;
            }
            $lastError = $result;

            if ($resp->isStreaming()) {
                break;
            }
        }

        // 失败也要落日志（2026-10-31 用户反馈：调用失败在日志里完全看不到）。
        //
        // 之前失败请求根本不进 logs 表，用户只看到「钱没少但调用没结果」，
        // 无从排查。现在失败也记一条：quota 恒为 0 —— 失败绝不扣费，
        // 状态码和错误详情原样带出来，用户日志页能看到失败原因。
        if ($lastError !== null) {
            $this->billing->writeLog([
                'user_id'           => $userId,
                'token_id'          => $tokenId,
                'channel_id'        => Ctx::int('last_channel_id'),
                'model'             => $model,
                'quota'             => 0,
                'elapsed_ms'        => (int) ((microtime(true) - $t0) * 1000),
                'first_byte_ms'     => Ctx::int('ttft_ms'),
                'prompt_tokens'     => Ctx::int('prompt_tokens'),
                'completion_tokens' => Ctx::int('completion_tokens'),
                'is_stream'         => $isStream,
                'status_code'       => (int) ($lastError['status'] ?? 500),
                'request_id'        => Ctx::requestId(),
                'detail'            => Ctx::str('upstream_detail', '') !== ''
                    ? Ctx::str('upstream_detail')
                    : (string) ($lastError['message'] ?? ''),
            ]);
        }

        return $lastError ?? [
            'ok'      => false,
            'message' => '服务暂时不可用，请稍后重试',
            'code'    => 'upstream_unavailable',
            'status'  => 503,
        ];
    }

    /**
     * 用 Canonical 请求尝试一个渠道。
     *
     * ── 直通 vs 翻译 ──
     *
     * 优先级：**原生直通 > 翻译**。直通不丢字段、延迟更低、出错面更小。
     *
     *   入站 Chat       + 上游 chat       → 直通 chat（发原始 body）
     *   入站 Responses  + 上游 responses  → 直通 responses（发原始 body）
     *   入站 Responses  + 上游只有 chat   → 翻译成 chat
     *   入站 Claude/Gemini               → 翻译成 chat
     */
    private function attemptCanonical(
        Channel $channel,
        string $key,
        array $canonical,
        Response $resp,
        bool $isStream,
        bool $isNativeChat,
        ?object $adapter,
        int $budget = 0,
    ): array {
        // $budget 是本次尝试可用的秒数（来自总预算的剩余部分）。
        // 传 0 表示走默认值（直接调用该方法的场景）。
        $timeout = $budget > 0 ? $budget : $this->timeout;

        // 记录本次尝试用的渠道，失败落库时能带出来
        Ctx::set('last_channel_id', $channel->id);

        $pool = $this->pools->for($channel);
        $upstreamModel = $channel->upstreamModel((string) $canonical['model']);
        $origin = $adapter?->name() ?? 'openai-chat';

        // ── 决定用哪个上游端点 ──
        //
        // 三种情况：
        //   native  → 原始 body 直接发（零转换，最好）
        //   compat  → 原始 body + 补齐 messages（保留 tools/reasoning 等我们还没适配的字段，
        //             比完全翻译成 chat 更接近原生）
        //   none    → 翻译成 chat
        $responsesMode = $channel->responsesMode;
        $useResponses = ($origin === 'openai-responses')
            && $channel->canUseResponses()
            && is_array($canonical['_raw'] ?? null);

        // 直通判断：
        //   入站 Chat                          → 直通（同构）
        //   入站 Responses + 走了 responses 端点 → 直通（响应是 Responses 格式）
        //
        // ⚠️ compat 模式也必须算直通 —— 它只是请求侧要补 messages，
        // 响应侧返回的是完整的标准 Responses 事件流（response.created /
        // output_text.delta / completed）。如果把它当翻译模式，就会拿
        // Responses 事件去当 Chat chunk 解析，结果一个字段都解析不出来。
        $passthrough = $isNativeChat || $useResponses;

        Logger::info('relay.key_picked', [
            'request_id' => Ctx::requestId(),
            'channel_id' => $channel->id,
            'key_fp'     => substr(md5($key), 0, 8),
            'protocol'   => $origin,
            'endpoint'   => $useResponses ? 'responses' : 'chat',
            'mode'       => $passthrough ? 'passthrough' : ($useResponses ? 'compat' : 'translate'),
            'pool'       => $pool->stats(),
        ]);

        // ── 构造请求体 ──
        if ($useResponses) {
            // 走 Responses 端点
            $upstreamBody = (array) $canonical['_raw'];
            $upstreamBody['model'] = $upstreamModel;
            $upstreamBody['stream'] = $isStream;

            if ($responsesMode === 'compat') {
                // 实测某上游：只给 input 会报 "必传参数: messages"。
                // 补上 messages 就通了，而且返回的是标准 Responses 结构。
                // 这样既保留了客户端原始字段（tools / reasoning / metadata），
                // 又满足了上游的参数校验。
                $upstreamBody['messages'] ??= $canonical['messages'];
            }

            $path = '/v1/responses';
        } else {
            // Chat 格式（直通或翻译后的结果）
            $upstreamBody = [
                'model'    => $upstreamModel,
                'messages' => $canonical['messages'],
                'stream'   => $isStream,
            ];
            foreach (['max_tokens', 'temperature', 'top_p', 'stop', 'tools', 'tool_choice'] as $f) {
                if (($canonical[$f] ?? null) !== null) {
                    $upstreamBody[$f] = $canonical[$f];
                }
            }

            // 流式请求显式要求上游在最后一块带上 usage。
            //
            // OpenAI 兼容协议默认不在流式响应里返回 usage（为了省带宽），
            // 必须用 stream_options.include_usage=true 才会在末尾补一个
            // 只含 usage 的 chunk。没有它，日志里的 Token 永远是 0。
            if ($isStream) {
                $upstreamBody['stream_options'] = ['include_usage' => true];
            }

            $path = '/v1/chat/completions';
        }

        $url = Forwarder::buildUrl($channel->baseUrl, $path);
        $headers = [
            'Content-Type'  => 'application/json',
            'Accept'        => $isStream ? 'text/event-stream' : 'application/json',
            'Authorization' => 'Bearer ' . $key,
        ];
        $payload = Json::encode($upstreamBody);
        $started = microtime(true);

        if ($isStream) {
            $resp->startStream();
            $buffer = '';

            $result = Forwarder::streamCurl(
                $url,
                $headers,
                $payload,
                $timeout,
                function (string $chunk, int $idx) use ($resp, &$buffer, $passthrough, $adapter, $upstreamModel, $started) {
                    if (Ctx::int('ttft_ms') === 0 && trim($chunk) !== '') {
                        Ctx::set('ttft_ms', (int) ((microtime(true) - $started) * 1000));
                    }

                    // 顺手抓 usage。放在最前面是因为直通模式直接 return，
                    // 放后面会漏掉；翻译模式也一样需要。
                    self::captureUsage($chunk);

                    // 直通：原样转发上游字节（最不容易出错）
                    if ($passthrough) {
                        $resp->write($chunk);
                        return;
                    }

                    // 翻译：把上游 SSE 解析成 Canonical chunk，再按目标协议渲染
                    $buffer .= $chunk;
                    while (($pos = strpos($buffer, "\n\n")) !== false) {
                        $frame = substr($buffer, 0, $pos);
                        $buffer = substr($buffer, $pos + 2);

                        $line = null;
                        foreach (explode("\n", $frame) as $l) {
                            if (str_starts_with($l, 'data: ')) {
                                $line = substr($l, 6);
                                break;
                            }
                        }
                        if ($line === null || trim($line) === '[DONE]') {
                            continue;
                        }

                        $decoded = json_decode($line, true);
                        if (!is_array($decoded)) {
                            continue;
                        }
                        $decoded['model'] ??= $upstreamModel;

                        $rendered = $adapter->renderChunk($decoded);
                        if ($rendered !== '') {
                            $resp->write($rendered);
                        }
                    }
                },
                (string) $canonical['model'],
                $upstreamModel,
            );

            // ── 判定这次流是否真的完成 ──
            //
            // ⚠️ 老版本只看 `status !== 200`，漏掉了一整类失败：
            //    HTTP 头已经收到 200，然后传到一半上游卡死，curl 报超时。
            //    这时 status 还是 200，判断为「成功」，于是：
            //      · 不记 stream_upstream_error（日志里看不到）
            //      · 照常补 [DONE]，客户端以为这是正常的完整回答
            //    用户看到的就是「模型答到一半突然没了」。
            //
            //    生产数据：24h 内 5,201 次 upstream_error，其中
            //    4,801 次的 chunks > 1000 —— 全都属于这一类。
            //
            // ⚠️ 2026-10-31 补充：还要排除「HTTP 200 但一个有效块都没有」。
            //   这是用户反馈的「空返回无内容」——上游回了响应头就结束，
            //   我们没有内容可转，却因为 status=200 判成成功并扣了钱。
            //   判失败后不扣费，客户端也能立刻重试而不是拿到空气。
            //
            // ⚠️ 2026-10-31 再补充（Issue #2）：chunks > 0 也可能是空响应。
            //   上游异常时会先发一帧只声明 role 的 chunk，紧接着直接 [DONE]。
            //   这时 chunks=2 却一个字都没产出，按 chunks>0 判成功依然会
            //   「假成功 + 扣费 ¥0.005 + token 0+0=0 + 耗时几百秒」。
            //   所以必须看"含真实内容的块数"（payload），而不是总块数。
            $empty = ($result['chunks'] ?? 0) === 0;
            $noOutput = ($result['payload'] ?? 0) === 0;
            $failed = ($result['status'] !== 200) || (($result['errno'] ?? 0) !== 0) || $empty || $noOutput;

            if ($failed) {
                // 超时用 503 记，避免把「上游挂了」记成 0 误导排查
                $pool->markFailed($key, $result['status'] ?: 503);

                $errMsg = $result['error'] !== ''
                    ? $result['error']
                    : ('上游返回 HTTP ' . $result['status']);

                // curl 28 = CURLE_OPERATION_TIMEDOUT。配合 LOW_SPEED 参数，
                // 这就是「上游 90 秒没吐数据」的那一类，和真正的网络故障分开记，
                // 排查时能一眼看出是哪边的锅。
                $isTimeout = ($result['errno'] ?? 0) === 28
                    || stripos($errMsg, 'too slow') !== false
                    || stripos($errMsg, 'timed out') !== false;

                Logger::warn('relay.stream_upstream_error', [
                    'request_id'  => Ctx::requestId(),
                    'channel_id'  => $channel->id,
                    'status_code' => $result['status'],
                    'chunks'      => $result['chunks'],
                    'errno'       => $result['errno'] ?? 0,
                    'timeout'     => $isTimeout,
                    'partial'     => $result['chunks'] > 0,
                    'error'       => Logger::mask($errMsg),
                ]);

                Ctx::set('upstream_error', [
                    'message' => $noOutput
                        // 零内容 —— Issue #2 的「假成功」场景，与「超时中断」区分开
                        ? '上游返回为空（未产出任何内容）'
                        : ($isTimeout && $result['chunks'] > 0
                            ? '上游超时中断，已收到部分内容'
                            : $errMsg),
                    // 零输出时日志必须记 502，不能记 200。
                    // 用户是按日志排查的，「状态 200 + 扣费」是最误导的组合。
                    'status'  => $noOutput ? 502 : ($result['status'] ?: 503),
                ]);

                // 断流时也要收尾，否则客户端会一直挂着等下一帧，
                // 直到它自己的读超时（有的客户端是 60s，有的是无限）。
                //
                // ⚠️ Issue #2：上游一个字都没产出，无论是否收到过帧，都不能扣费。
                //
                //   两种情况：
                //     · chunks = 0 —— 连响应头之后的数据都没有（连接空转）
                //     · chunks > 0 —— 只发了 role 帧就 [DONE]（假成功）
                //   以前这里写成 chunks > 0 才处理，导致 chunks=0 时照样扣钱。
                //
                //   同时必须显式发 error 事件，否则客户端只能等到自己超时，
                //   用户看到的是「卡死后无输出」，完全不知道发生了什么。
                if ($noOutput) {
                    Ctx::set('no_charge', true);
                    $resp->writeEvent(Json::encode([
                        'error' => [
                            'message' => '上游未返回任何内容，请稍后重试或更换模型',
                            'type'    => 'upstream_empty',
                            'code'    => 'upstream_empty',
                        ],
                    ]));
                }

                // 翻译模式我们自己掌控帧格式，直接补 renderDone()；
                // 直通模式不盲补 —— Responses 的结束标记不是 [DONE]，
                // 乱补反而会让客户端解析出错，宁可让它自己超时。
                // 但「零输出」已在上面发了 error 事件，客户端能正常收尾。
                if (!$passthrough) {
                    $done = $adapter->renderDone();
                    if ($done !== '') {
                        $resp->write($done);
                    }
                }

                return ['ok' => true];
            }

            // 翻译模式下要补结束帧；直通模式下上游已经发了自己的结束标记
            if (!$passthrough) {
                $done = $adapter->renderDone();
                if ($done !== '') {
                    $resp->write($done);
                }
            }

            Logger::info('relay.stream_done', [
                'request_id' => Ctx::requestId(),
                'channel_id' => $channel->id,
                'chunks'     => $result['chunks'],
                'mode'       => $passthrough ? 'passthrough' : 'translate',
                'ms'         => (int) ((microtime(true) - $started) * 1000),
                'ttft_ms'    => Ctx::int('ttft_ms'),
            ]);
            return ['ok' => true];
        }

        // 非流式
        $result = Forwarder::requestCurl($url, $headers, $payload, $timeout, (string) $canonical['model'], $upstreamModel);

        // 非流式：同样要看 errno —— 超时/连接重置时 status 可能还是 0 或 200，
        // 但 body 是残缺的，直接当成功会把半截 JSON 吐给客户端。
        if ($result['status'] !== 200 || ($result['errno'] ?? 0) !== 0) {
            $pool->markFailed($key, $result['status']);
            $err = Forwarder::extractError($result['body']);
            $msg = $err['message'] ?? ($result['error'] !== '' ? $result['error'] : ('上游返回 HTTP ' . $result['status']));
            Logger::warn('relay.upstream_error', [
                'request_id'  => Ctx::requestId(),
                'channel_id'  => $channel->id,
                'status_code' => $result['status'],
                'errno'       => $result['errno'] ?? 0,
                'endpoint'    => $useResponses ? 'responses' : 'chat',
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

        // HTTP 200 但包着错误体/无法解析的结构 → 按失败处理，不扣费。
        // 详情存进上下文，失败日志能看到原因，而不是只有一行状态码。
        $invalid = self::validateUpstreamBody($result['body']);
        if ($invalid !== null) {
            Ctx::set('upstream_detail', mb_substr($result['body'], 0, 300));

            return $invalid;
        }

        Ctx::set('response_body', $result['body']);
        // 记录实际用的端点，Controller 据此决定要不要翻译响应
        Ctx::set('response_endpoint', $useResponses ? 'responses' : 'chat');
        // 非流式的 usage 就藏在响应体里
        self::captureUsage($result['body']);
        Logger::info('relay.done', [
            'request_id' => Ctx::requestId(),
            'channel_id' => $channel->id,
            'endpoint'   => $useResponses ? 'responses' : 'chat',
            'mode'       => $passthrough ? 'passthrough' : 'translate',
            'ms'         => (int) ((microtime(true) - $started) * 1000),
        ]);
        return ['ok' => true];
    }

    /**
     * 处理一次对话请求。
     *
     * @return array{ok:bool,message?:string,code?:string,status?:int}
     */
    public function chat(array $body, Response $resp): array
    {
        $model = (string) ($body['model'] ?? '');
        if ($model === '') {
            return ['ok' => false, 'message' => '缺少 model 字段', 'code' => 'missing_model', 'status' => 400];
        }

        $isStream = (bool) ($body['stream'] ?? false);
        $displayModel = $model;
        $group = Ctx::str('group', 'default');
        $userId = Ctx::int('user_id');
        $tokenId = Ctx::int('token_id');

        $user = $userId > 0 ? $this->users->findById($userId) : null;

        // ── 权限校验 ─────────────────────────────
        $deny = $this->checkPermission($model, $user);
        if ($deny !== null) {
            return $deny;
        }

        $isAdmin = $user !== null && $user->isAdmin();

        // ── 选渠道 ───────────────────────────────
        $candidates = $this->channels->findForModel($model, $group);
        if ($candidates === []) {
            // ⚠️ 对外话术必须中性：不能提"渠道""分组"这些内部概念，
            // 也不能让调用方区分"模型不存在"和"临时没有可用上游"
            // （后者会暴露服务当前的负载状态）。
            return [
                'ok'      => false,
                'message' => "模型 {$model} 不存在或暂不可用",
                'code'    => 'model_unavailable',
                'status'  => 404,
            ];
        }

        $lastError = null;
        $attempts = max(1, $this->retryTimes);
        // 按渠道记录「是否已无可尝试的密钥」。
        //
        // ⚠️ 这里不能用「渠道是否已被试过」来判断 —— 那是错的。
        // 单个渠道可能挂多把密钥，一把 401 不代表这个渠道不可用，
        // 换下一把大概率能成功。只有当这个渠道的密钥**全部**冷却/用尽时，
        // 才应该换到别的渠道。
        $exhausted = [];
        $t0 = microtime(true);
        // 与 chatCanonical 同一套预算规则：600 秒总预算，重试共享，
        // 保证「到 600 秒统一报错」而不是 3×600。
        $deadline = $t0 + $this->timeout;

        for ($i = 0; $i < $attempts; $i++) {
            // 每次重试前检查：内容是否已经推给客户端了？
            // 已经推过就绝不能换密钥/渠道 —— 否则用户会看到「A 的半句话 + B 的完整回答」。
            if ($resp->isStreaming()) {
                Logger::warn('relay.abort_retry_content_flushed', [
                    'request_id' => Ctx::requestId(),
                    'model'      => $model,
                ]);
                break;
            }

            // 预算用尽立刻收口，不再发起新的上游连接。
            $budget = (int) ($deadline - microtime(true));
            if ($budget <= 0) {
                break;
            }

            $picked = $this->pickChannelAndKey($candidates, $exhausted);
            if ($picked === null) {
                break;
            }
            [$channel, $key] = $picked;

            $result = $this->attempt($channel, $key, $body, $resp, $displayModel, $isStream, $budget);
            if ($result['ok']) {
                $this->settle($model, $isAdmin, $userId, $tokenId, $channel->id, $isStream, $t0);
                return $result;
            }
            $lastError = $result;

            if ($resp->isStreaming()) {
                break;
            }
        }

        // 失败也要落日志（2026-10-31 用户反馈：调用失败在日志里完全看不到）。
        //
        // 之前失败请求根本不进 logs 表，用户只看到「钱没少但调用没结果」，
        // 无从排查。现在失败也记一条：quota 恒为 0 —— 失败绝不扣费，
        // 状态码和错误详情原样带出来，用户日志页能看到失败原因。
        if ($lastError !== null) {
            $this->billing->writeLog([
                'user_id'           => $userId,
                'token_id'          => $tokenId,
                'channel_id'        => Ctx::int('last_channel_id'),
                'model'             => $model,
                'quota'             => 0,
                'elapsed_ms'        => (int) ((microtime(true) - $t0) * 1000),
                'first_byte_ms'     => Ctx::int('ttft_ms'),
                'prompt_tokens'     => Ctx::int('prompt_tokens'),
                'completion_tokens' => Ctx::int('completion_tokens'),
                'is_stream'         => $isStream,
                'status_code'       => (int) ($lastError['status'] ?? 500),
                'request_id'        => Ctx::requestId(),
                'detail'            => Ctx::str('upstream_detail', '') !== ''
                    ? Ctx::str('upstream_detail')
                    : (string) ($lastError['message'] ?? ''),
            ]);
        }

        return $lastError ?? [
            'ok'      => false,
            'message' => '服务暂时不可用，请稍后重试',
            'code'    => 'upstream_unavailable',
            'status'  => 503,
        ];
    }

    /**
     * 权限校验。
     *
     * 三种身份：
     *   匿名   → 只能用免费模型（这是产品核心：不注册不登录直接用）
     *   经销商 → 用自己在 account_models 里被授权的模型，按专属价格计费
     *   管理员 → 全放开、不扣费
     */
    private function checkPermission(string $model, ?User $user): ?array
    {
        $userId = $user?->id ?? 0;
        $isAdmin = $user !== null && $user->isAdmin();
        $group = Ctx::str('group', 'default');

        $result = $this->billing->charge($model, $group, $userId, $isAdmin);

        if (!$result['allowed']) {
            // 分两种情况，不再一律中性：
            //
            //   ① 匿名（未登录）：明确告诉他去登录。
            //      以前这里刻意用「模型不可用」掩盖，是怕调用方能推断模型存在性。
            //      但广场页本来就把 79 个模型列出来了，存在性不是秘密 ——
            //      藏登录提示只会让新用户以为模型坏了，白白流失。
            //
            //   ② 已登录但没权限：仍然保持中性。
            //      这里才需要防探测 —— 不能让已登录用户靠错误码差异
            //      摸出「哪些模型是存在的但我没被分配」。
            if ($userId <= 0) {
                return [
                    'ok'      => false,
                    'message' => $result['reason'],
                    'code'    => $result['code'] ?? 'login_required',
                    'status'  => 401,
                ];
            }

            return [
                'ok'      => false,
                'message' => $result['reason'],
                'code'    => 'model_unavailable',
                'status'  => 404,
            ];
        }

        // 余额检查（管理员和免费模型跳过）
        if (!$isAdmin && $result['quota'] > 0 && $user !== null) {
            if (!$user->hasQuota($result['quota'])) {
                return [
                    'ok'      => false,
                    'message' => '余额不足，请到控制台「充值」页面充值后重试',
                    'code'    => 'insufficient_quota',
                    'status'  => 403,
                ];
            }
        }

        // 把算好的价格塞进上下文，结算时直接用，不用再算一遍
        Ctx::set('charge_quota', $result['quota']);

        return null;
    }

    /**
     * 结算：扣费 + 写日志。
     *
     * 注意这里在响应已经发出后执行 —— 扣费失败不能让用户拿不到内容。
     */
    /**
     * 校验上游非流式响应体是「可交付的成功结构」。
     *
     * 背景（2026-10-31 用户反馈：报错了依然算钱）：
     *   中转站经常用 HTTP 200 包一层错误 JSON（{"error":{...}}），
     *   原判定只看 status/errno/空串，这类响应会被当成成功并扣费，
     *   客户端拿到的却是没有 choices 的报错体。
     *
     * 规则（chat 与 responses 两种端点都覆盖）：
     *   1. JSON 解析失败            → 失败（HTML 错误页、截断等）
     *   2. 顶层带 error 字段        → 失败（成功响应不会有顶层 error）
     *   3. choices 和 output 都没有 → 失败（既不是 chat 也不是 responses 的合法结构）
     *
     * @return array|null null 表示校验通过；数组表示失败结果（含给客户端的报错文案）
     */
    private static function validateUpstreamBody(string $body): ?array
    {
        $decoded = json_decode($body, true);

        if (!is_array($decoded)) {
            return ['ok' => false, 'message' => '上游返回了无法解析的响应', 'code' => 'invalid_body', 'status' => 502];
        }

        // 顶层 error：把上游的报错文案带出来，比笼统的「上游错误」对用户有用
        if (isset($decoded['error'])) {
            $msg = is_array($decoded['error'])
                ? (string) ($decoded['error']['message'] ?? '')
                : (string) $decoded['error'];

            return ['ok' => false, 'message' => $msg !== '' ? $msg : '上游处理失败', 'code' => 'upstream_error_body', 'status' => 502];
        }

        // 既没有 choices（chat）也没有 output（responses）→ 不是合法成功结构
        if (!isset($decoded['choices']) && !isset($decoded['output'])) {
            return ['ok' => false, 'message' => '上游返回了空的结果', 'code' => 'empty_result', 'status' => 502];
        }

        // ── 有壳无内容：Issue #2 的非流式变体 ──
        //
        // HTTP 200、结构完整、usage 全 0、message.content 是空串 ——
        // 上游等于什么都没干，但按「结构合法」会被判成功并扣费。
        // 用户明确要求「只要未产出任何 token 就不应计费」。
        //
        // 注意不能只看 content：工具调用、推理内容、多模态 parts
        // 都可能是唯一产出，所以要逐项排除后再判空。
        if (isset($decoded['choices']) && is_array($decoded['choices']) && $decoded['choices'] !== []) {
            $msg = $decoded['choices'][0]['message'] ?? null;
            if (is_array($msg) && !self::messageHasOutput($msg)) {
                return [
                    'ok'      => false,
                    'message' => '上游未返回任何内容，请稍后重试或更换模型',
                    'code'    => 'empty_content',
                    'status'  => 502,
                ];
            }
        }

        return null;
    }

    /**
     * message 结构里是否含有真实产出。
     *
     * 覆盖：正文 content（字符串或 parts 数组）、reasoning/reasoning_content、
     * tool_calls、function_call、以及多模态 content parts 里的任意非空字段。
     *
     * @param array<string,mixed> $msg
     */
    private static function messageHasOutput(array $msg): bool
    {
        // 1. 正文：字符串
        $content = $msg['content'] ?? null;
        if (is_string($content) && trim($content) !== '') {
            return true;
        }
        // 2. 正文：多模态 parts 数组（[{"type":"text","text":"..."}]）
        if (is_array($content) && $content !== []) {
            foreach ($content as $part) {
                if (is_array($part)) {
                    foreach ($part as $v) {
                        if (is_string($v) && trim($v) !== '') {
                            return true;
                        }
                    }
                } elseif (is_string($part) && trim($part) !== '') {
                    return true;
                }
            }
        }
        // 3. 推理内容（部分模型只给 reasoning_content）
        foreach (['reasoning_content', 'reasoning', 'thinking'] as $k) {
            $v = $msg[$k] ?? null;
            if (is_string($v) && trim($v) !== '') {
                return true;
            }
        }
        // 4. 工具调用 / 函数调用
        foreach (['tool_calls', 'function_call'] as $k) {
            $v = $msg[$k] ?? null;
            if (is_array($v) && $v !== []) {
                return true;
            }
        }

        return false;
    }

    /**
     * 从上游响应里提取 token 用量，写进协程上下文。
     *
     * 两种形态都要处理：
     *   非流式 —— 整个响应体就是一段 JSON：
     *             {"usage":{"prompt_tokens":12,"completion_tokens":34}}
     *   流式   —— SSE 末尾的 data: {"usage":{...}}
     *             （需要上游支持 stream_options.include_usage）
     *
     * 拿不到就保持 0，绝不估算。日志里的数字必须能和上游账单对上，
     * 用字符数除三之类的办法凑出来的近似值只会误导用户。
     */
    private static function captureUsage(string $raw): void
    {
        if ($raw === '' || !str_contains($raw, '"usage"')) {
            return;
        }

        // 非流式：整体是一段 JSON
        $decoded = json_decode($raw, true);
        if (is_array($decoded) && isset($decoded['usage']) && is_array($decoded['usage'])) {
            self::applyUsage($decoded['usage']);

            return;
        }

        // 流式：逐行找 data: 行
        foreach (explode("\n", $raw) as $line) {
            $line = trim($line);
            if (!str_starts_with($line, 'data:')) {
                continue;
            }
            $json = trim(substr($line, 5));
            if ($json === '' || $json === '[DONE]' || !str_contains($json, '"usage"')) {
                continue;
            }
            $d = json_decode($json, true);
            if (is_array($d) && isset($d['usage']) && is_array($d['usage'])) {
                self::applyUsage($d['usage']);
            }
        }
    }

    /**
     * @param array<string,mixed> $usage
     */
    private static function applyUsage(array $usage): void
    {
        $p = (int) ($usage['prompt_tokens'] ?? 0);
        $c = (int) ($usage['completion_tokens'] ?? 0);

        // 只在上游真的给了非零数字时才写，避免后面的空 usage
        // 把前面已经拿到的值冲掉。
        if ($p > 0) {
            Ctx::set('prompt_tokens', $p);
        }
        if ($c > 0) {
            Ctx::set('completion_tokens', $c);
        }
    }

    private function settle(
        string $model,
        bool $isAdmin,
        int $userId,
        int $tokenId,
        int $channelId,
        bool $isStream,
        float $t0,
    ): void {
        // ── 扣费前置判断 ──
        //
        // ⚠️ Issue #2：流式请求失败时，attemptXxx 仍返回 ok=true
        //   （因为 HTTP 200 和部分帧已经发给客户端，状态码改不了了）。
        //   上层因此照样会调 settle() —— 如果这里不拦，就是
        //   「上游一个字都没产出、客户端收到 error，却照扣 ¥0.005」。
        //
        // 判定依据是 Ctx 里的 no_charge 标记：
        //   · 零内容（role 帧 + DONE）→ 不扣费
        //   · 有部分内容后中断       → 正常扣费（用户确实拿到了东西）
        $noCharge = Ctx::bool('no_charge');
        $quota = ($isAdmin || $noCharge) ? 0 : Ctx::int('charge_quota');

        if ($quota > 0 && $userId > 0) {
            try {
                $this->users->consume($userId, $quota);
                $this->users->consumeToken($tokenId, $quota);
            } catch (\Throwable $e) {
                Logger::warn('relay.charge_failed', [
                    'request_id' => Ctx::requestId(),
                    'error'      => Logger::mask($e->getMessage()),
                ]);
            }
        }

        // 上游报错时日志里的状态码要真实反映失败，不能一律记 200
        $upstreamError = Ctx::get('upstream_error');
        $statusCode = is_array($upstreamError)
            ? (int) ($upstreamError['status'] ?? 502)
            : 200;

        $this->billing->writeLog([
            'user_id'           => $userId,
            'token_id'          => $tokenId,
            'channel_id'        => $channelId,
            'model'             => $model,
            'quota'             => $quota,
            'elapsed_ms'        => (int) ((microtime(true) - $t0) * 1000),
            'first_byte_ms'     => Ctx::int('ttft_ms'),
            // Token 用量：从上游响应的 usage 里解析出来（见 captureUsage）。
            // 上游没给就是 0 —— 不能凭空估算，否则日志里的数字会对不上账单。
            'prompt_tokens'     => Ctx::int('prompt_tokens'),
            'completion_tokens' => Ctx::int('completion_tokens'),
            'is_stream'         => $isStream,
            'status_code'       => $statusCode,
            'request_id'        => Ctx::requestId(),
            // 详情优先级：上游错误信息 > 无用量说明 > 空
            'detail'            => is_array($upstreamError) && ($upstreamError['message'] ?? '') !== ''
                ? (string) $upstreamError['message']
                : (Ctx::str('upstream_detail', '') !== ''
                    ? Ctx::str('upstream_detail')
                    : (Ctx::int('prompt_tokens') === 0 && Ctx::int('completion_tokens') === 0
                        ? '上游未返回 token 用量（按次计费，不影响扣费金额）'
                        : '')),
        ]);
    }

    /**
     * 选一个「渠道 + 可用密钥」。
     *
     * 有可用密钥的渠道优先；某个渠道的密钥全在冷却时，标记为耗尽后换下一个渠道。
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
            // 这个渠道全部密钥都在冷却 —— 本轮之后不用再考虑它
            $exhausted[$ch->id] = true;
            Logger::warn('relay.channel_exhausted', [
                'request_id' => Ctx::requestId(),
                'channel_id' => $ch->id,
            ]);
        }
        return null;
    }

    private function attempt(Channel $channel, string $key, array $body, Response $resp, string $displayModel, bool $isStream, int $budget = 0): array
    {
        // $budget 是本次尝试可用的秒数（来自总预算的剩余部分）。
        // 传 0 表示走默认值（直接调用该方法的场景）。
        $timeout = $budget > 0 ? $budget : $this->timeout;

        // 记录本次尝试用的渠道，失败落库时能带出来
        Ctx::set('last_channel_id', $channel->id);

        $pool = $this->pools->for($channel);

        // 密钥指纹：只记前 8 位哈希，既能验证轮询是否真的在换密钥，
        // 又不会把密钥本身写进日志。排查"是不是总用同一把"时非常关键。
        Logger::info('relay.key_picked', [
            'request_id' => Ctx::requestId(),
            'channel_id' => $channel->id,
            'key_fp'     => substr(md5($key), 0, 8),
            'pool'       => $pool->stats(),
        ]);

        $upstreamModel = $channel->upstreamModel((string) $body['model']);
        $upstreamBody = $body;
        $upstreamBody['model'] = $upstreamModel;

        // 流式时显式要求带上 usage（与 attemptCanonical 同一套规则）
        if ($isStream) {
            $upstreamBody['stream_options'] = ['include_usage' => true];
        }

        // ⚠️ 路径必须带 /v1。
        // 渠道的 base_url 配到域名/前缀为止（如 https://api.example.com），**不含 /v1**。
        // 少了 /v1 会打到网站首页返回 HTML（实测踩过，排查了很久）。
        $url = Forwarder::buildUrl($channel->baseUrl, '/v1/chat/completions');

        $headers = [
            'Content-Type'  => 'application/json',
            'Accept'        => $isStream ? 'text/event-stream' : 'application/json',
            'Authorization' => 'Bearer ' . $key,
        ];

        $payload = Json::encode($upstreamBody);
        $started = microtime(true);

        if ($isStream) {
            $firstByteAt = 0.0;
            $resp->startStream();

            $result = Forwarder::streamCurl(
                $url,
                $headers,
                $payload,
                $timeout,
                function (string $chunk, int $idx) use ($resp, &$firstByteAt, $started) {
                    if ($firstByteAt <= 0.0 && trim($chunk) !== '') {
                        $firstByteAt = microtime(true) - $started;
                        Ctx::set('ttft_ms', (int) ($firstByteAt * 1000));
                    }

                    // 抓 usage（流式路径二）
                    self::captureUsage($chunk);

                    $resp->write($chunk);
                },
                $displayModel,
                $upstreamModel,
            );

            // 同 attemptCanonical：status 200 也可能是「传一半断了」，
            // 必须同时看 errno；零 chunk 或「有 chunk 但零内容」都按失败处理。
            // 详见那段注释（Issue #2：role 帧 + DONE 的假成功）。
            $empty = ($result['chunks'] ?? 0) === 0;
            $noOutput = ($result['payload'] ?? 0) === 0;
            $failed = ($result['status'] !== 200) || (($result['errno'] ?? 0) !== 0) || $empty || $noOutput;

            if ($failed) {
                $pool->markFailed($key, $result['status'] ?: 503);

                $errMsg = $result['error'] !== ''
                    ? $result['error']
                    : ('上游返回 HTTP ' . $result['status']);

                $isTimeout = ($result['errno'] ?? 0) === 28
                    || stripos($errMsg, 'too slow') !== false
                    || stripos($errMsg, 'timed out') !== false;

                Logger::warn('relay.stream_upstream_error', [
                    'request_id'  => Ctx::requestId(),
                    'channel_id'  => $channel->id,
                    'status_code' => $result['status'],
                    'chunks'      => $result['chunks'],
                    'errno'       => $result['errno'] ?? 0,
                    'timeout'     => $isTimeout,
                    'partial'     => $result['chunks'] > 0,
                    'error'       => Logger::mask($errMsg),
                ]);
                Ctx::set('upstream_error', [
                    'message' => $noOutput
                        ? '上游返回为空（未产出任何内容）'
                        : ($isTimeout && $result['chunks'] > 0
                            ? '上游超时中断，已收到部分内容'
                            : $errMsg),
                    // 零输出记 502（不是 200），否则日志里看不出失败
                    'status'  => $noOutput ? 502 : ($result['status'] ?: 503),
                ]);

                // 零输出：不扣费 + 明确告诉客户端
                // （chunks 为 0 和「role 帧 + DONE」都算，见另一处注释）
                if ($noOutput) {
                    Ctx::set('no_charge', true);
                    $resp->writeEvent(Json::encode([
                        'error' => [
                            'message' => '上游未返回任何内容，请稍后重试或更换模型',
                            'type'    => 'upstream_empty',
                            'code'    => 'upstream_empty',
                        ],
                    ]));
                }

                return ['ok' => true];
            }

            Logger::info('relay.stream_done', [
                'request_id' => Ctx::requestId(),
                'channel_id' => $channel->id,
                'chunks'     => $result['chunks'],
                'ms'         => (int) ((microtime(true) - $started) * 1000),
                'ttft_ms'    => Ctx::int('ttft_ms'),
            ]);
            return ['ok' => true];
        }

        // 非流式
        $result = Forwarder::requestCurl($url, $headers, $payload, $timeout, $displayModel, $upstreamModel);

        // 非流式：同 attemptCanonical，errno 也要判
        if ($result['status'] !== 200 || ($result['errno'] ?? 0) !== 0) {
            $pool->markFailed($key, $result['status']);
            $err = Forwarder::extractError($result['body']);
            $msg = $err['message'] ?? ($result['error'] !== '' ? $result['error'] : ('上游返回 HTTP ' . $result['status']));
            Logger::warn('relay.upstream_error', [
                'request_id'  => Ctx::requestId(),
                'channel_id'  => $channel->id,
                'status_code' => $result['status'],
                'errno'       => $result['errno'] ?? 0,
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

        // HTTP 200 但包着错误体/无法解析的结构 → 按失败处理，不扣费
        $invalid = self::validateUpstreamBody($result['body']);
        if ($invalid !== null) {
            Ctx::set('upstream_detail', mb_substr($result['body'], 0, 300));

            return $invalid;
        }

        Ctx::set('response_body', $result['body']);
        // 非流式的 usage 就藏在响应体里
        self::captureUsage($result['body']);
        Logger::info('relay.done', [
            'request_id' => Ctx::requestId(),
            'channel_id' => $channel->id,
            'ms'         => (int) ((microtime(true) - $started) * 1000),
        ]);
        return ['ok' => true];
    }
}
