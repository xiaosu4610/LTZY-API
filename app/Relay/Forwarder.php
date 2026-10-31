<?php
declare(strict_types=1);

namespace Ltzy\Relay;

/**
 * 上游转发器。
 *
 * ⚠️ 全项目最关键的一段代码，决定 SSE 流式是否正常。
 *
 * 唯一的处理原则（docs/170 用惨痛教训换来的）：
 *   **上游给什么，客户端就收到什么。**
 *
 * 除了改写 `model` 字段（隐藏上游真实模型名），不做任何内容加工：
 *   - 不解析再重组 JSON（会改变字段结构，破坏 tool_calls）
 *   - 不剥离思考标签（会剥出空内容）
 *   - 不把 reasoning_content 镜像进 content（会污染正文）
 *   - 不在流末尾补发上游没发过的内容（会打乱客户端的 token 计数）
 *
 * ── 为什么用 curl 而不是 Swoole\Coroutine\Http\Client ──
 *
 * 实测：后者的 execute() 会**读完整个响应体**才开始返回（9255 字节 / 33 帧全在
 * $cli->body 里），recv() 拿不到任何东西。也就是说它不支持真正的流式读 ——
 * 用它做"流式转发"，等于等上游生成完再一次性返回，客户端看到的是长时间空白后
 * 突然全部内容。
 *
 * curl 配 CURLOPT_WRITEFUNCTION 才是真流式：每收到一块就回调一次（实测 30 次）。
 * 而 Swoole 的 SWOOLE_HOOK_CURL 会把 curl 调用转成协程，不阻塞 worker。
 */
final class Forwarder
{
    /**
     * 发起流式请求，每收到一块就回调。
     *
     * @param string   $url
     * @param string[] $headers HTTP 头（key => value）
     * @param string   $body    请求体
     * @param int      $timeout 硬超时（秒）
     * @param callable $onChunk function(string $chunk, int $idx): void
     *
     * @return array{status:int,chunks:int,error:string}
     */
    public static function streamCurl(
        string $url,
        array $headers,
        string $body,
        int $timeout,
        callable $onChunk,
        ?string $displayModel = null,
        ?string $actualModel = null,
    ): array {
        $chunks = 0;
        // 含真实内容的块数（0 = 上游一个 token 都没产出）
        $payload = 0;
        // 只要给了显示名就改写 —— 上游可能返回和请求完全不同的 model 名，
        // 所以不能靠"两者是否相等"来决定是否改写（见 rewriteModelField 的说明）。
        $rewrite = $displayModel !== null && $displayModel !== '';

        $ch = curl_init($url);
        if ($ch === false) {
            return ['status' => 0, 'chunks' => 0, 'payload' => 0, 'error' => 'curl_init 失败'];
        }

        curl_setopt_array($ch, [
            CURLOPT_POST           => true,
            CURLOPT_POSTFIELDS     => $body,
            CURLOPT_HTTPHEADER     => self::formatHeaders($headers),
            CURLOPT_TIMEOUT        => $timeout,
            // 建连超时 30 秒。
            //
            // 2026-10-30 定为 30 秒。依据：
            //   · 本机到上游链路实测 ICMP 丢包 16.7%，TLS 握手需 4 个往返，
            //     正常情况下握手 0.31-0.33 秒，偶发抖到 1-3 秒。
            //   · 首次上线阶段上游可能冷启动，握手会更慢。
            //   · 但也不能跟着总超时一起放到 600 秒 —— 连接建立（DNS+TCP+TLS）
            //     本质上是个「行就行、不行就不行」的阶段，30 秒足够覆盖所有正常情况，
            //     超过就说明链路有问题，早点放弃换下一次尝试更有价值。
            CURLOPT_CONNECTTIMEOUT => 30,

            // ── 低速检测：上游"卡死"时快速失败 ──
            //
            // 生产数据（24h 采样）：某上游池出现数千次流式失败，
            // 其中 4,801 次是「已经传了 1000+ 块之后卡住不懂」，
            // 错误统一是 Connection timed out —— 全是硬等 300 秒超时。
            //
            // 用户感受到的是「模型答到一半突然没了」，等 5 分钟才报错。
            //
            // ⚠️ 2026-10-29 安全测试中发现：这里原来配的是
            //   CURLOPT_LOW_SPEED_LIMIT => 1
            //   CURLOPT_LOW_SPEED_TIME  => 90
            // 但 Swoole 的 curl hook 根本不支持 option[19]（LOW_SPEED_LIMIT），
            // 直接抛 swoole_curl_setopt(): option[19] is not supported ——
            // 导致**所有 /v1/chat/completions 请求 500**，relay 完全不可用。
            // 这类故障很难从日志第一时间归因到"两个 curl 选项"，
            // 所以在此标注：不要在这加 Swoole 不支持的 CURLOPT_*。
            //
            // 现在的兜底策略：
            //   - CURLOPT_TIMEOUT      硬上限，整条请求最多 $timeout 秒
            //   - CURLOPT_CONNECTTIMEOUT 建连 10 秒
            //   - streamCurl 内部按 chunk 计时（见下方 $lastChunkAt 逻辑）
            // 静默等待由调用方（RelayService）根据 chunks 数判断是否部分输出。

            // 关键：不复用连接，避免上游复用导致的分块边界错乱
            CURLOPT_FORBID_REUSE   => true,
            // 逐块回调 —— 这是"真流式"的唯一开关
            CURLOPT_WRITEFUNCTION  => function ($ch, $data) use (&$chunks, &$payload, $onChunk, $rewrite, $displayModel, $actualModel) {
                $chunks++;
                // 统计「含真实内容的块数」。
                //
                // 不能只看 $chunks：上游异常时会先发一帧只声明 role 的 chunk，
                // 紧接着直接 [DONE]。此时 chunks=2 却一个字都没产出，
                // 按 chunks>0 判成功就会「假成功 + 扣费」。
                if (self::hasRealOutput($data)) {
                    $payload++;
                }

                // ⚠️ 先记住原始长度。改写 model 后长度会变
                // （internlm3 → internlm3-8b-instruct 就长了 12 字节），
                // 但必须向 curl 报告**实际接收**的字节数，否则它会认为
                // 写入不完整/越界，可能截断传输。
                $received = strlen($data);

                if ($rewrite) {
                    $data = self::rewriteModelField($data, $displayModel, $actualModel);
                }

                $onChunk($data, $chunks);

                return $received;
            },
        ]);

        curl_exec($ch);
        $status = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
        $error  = curl_error($ch);
        $errno  = curl_errno($ch);

        // ⚠️ 显式释放句柄。
        //    以前靠 GC 回收，但这是长驻进程：高并发 + 外部扫描流量下，
        //    GC 跟不上分配速度，实测 6 分钟内存从 24MB 涨到 147MB。
        //    curl_close 在 8.5 只是 deprecated（不是移除），@ 抑制警告即可。
        @curl_close($ch);

        return [
            'status'  => $status,
            'chunks'  => $chunks,
            // payload = 含真实内容的块数。0 表示上游一个 token 都没产出。
            'payload' => $payload,
            'error'   => $error,
            'errno'   => $errno,
        ];
    }

    /**
     * 判断这个 SSE 块里是否含有真实输出内容。
     *
     * 各协议的内容字段名不同：
     *   Chat Completions  delta.content / delta.reasoning_content
     *   Responses API      delta.text / output_text
     *   Claude Messages    delta.text / content_block_delta
     *
     * 只做字符串检查不做完整 JSON 解析 —— 这个函数在每个数据块上都会跑，
     * 高并发下 json_decode 的开销不可忽视。
     *
     * 判定规则：字段名后紧跟的字符不是结束引号，就是有内容；
     * `"content":""` 这种空串会被正确排除。
     */
    public static function hasRealOutput(string $chunk): bool
    {
        static $fields = [
            '"content":"',
            '"reasoning_content":"',
            '"text":"',
            '"output_text":"',
            '"thinking":"',
        ];

        foreach ($fields as $needle) {
            $pos = 0;
            while (($pos = strpos($chunk, $needle, $pos)) !== false) {
                $start = $pos + strlen($needle);
                if (isset($chunk[$start]) && $chunk[$start] !== '"') {
                    return true;
                }
                $pos = $start;
            }
        }

        // tool_calls 也算产出（有些模型只返回工具调用、没有正文）
        return str_contains($chunk, '"tool_calls":[')
            && !str_contains($chunk, '"tool_calls":[]');
    }

    /**
     * 非流式请求：一次性拿到完整响应体。
     *
     * @return array{status:int,body:string,error:string}
     */
    public static function requestCurl(
        string $url,
        array $headers,
        string $body,
        int $timeout,
        ?string $displayModel = null,
        ?string $actualModel = null,
    ): array {
        $ch = curl_init($url);
        if ($ch === false) {
            return ['status' => 0, 'body' => '', 'error' => 'curl_init 失败'];
        }

        curl_setopt_array($ch, [
            CURLOPT_POST           => true,
            CURLOPT_POSTFIELDS     => $body,
            CURLOPT_HTTPHEADER     => self::formatHeaders($headers),
            CURLOPT_TIMEOUT        => $timeout,
            // 建连超时 30 秒。
            //
            // 2026-10-30 定为 30 秒。依据：
            //   · 本机到上游链路实测 ICMP 丢包 16.7%，TLS 握手需 4 个往返，
            //     正常情况下握手 0.31-0.33 秒，偶发抖到 1-3 秒。
            //   · 首次上线阶段上游可能冷启动，握手会更慢。
            //   · 但也不能跟着总超时一起放到 600 秒 —— 连接建立（DNS+TCP+TLS）
            //     本质上是个「行就行、不行就不行」的阶段，30 秒足够覆盖所有正常情况，
            //     超过就说明链路有问题，早点放弃换下一次尝试更有价值。
            CURLOPT_CONNECTTIMEOUT => 30,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_FORBID_REUSE   => true,
        ]);

        $resp = curl_exec($ch);
        $status = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
        $error = curl_error($ch);
        $errno = curl_errno($ch);

        @curl_close($ch);

        $text = is_string($resp) ? $resp : '';

        if ($displayModel !== null && $displayModel !== '') {
            $text = self::rewriteModelField($text, $displayModel, $actualModel);
        }

        return ['status' => $status, 'body' => $text, 'error' => $error, 'errno' => $errno];
    }

    /**
     * 拼接上游 URL。
     *
     * base_url 配到域名/前缀为止（如 https://api.example.com），不含 /v1。
     * path 必须自带 /v1 —— 少了它会打到网站首页返回 HTML。
     */
    public static function buildUrl(string $baseUrl, string $path): string
    {
        return rtrim($baseUrl, '/') . '/' . ltrim($path, '/');
    }

    /**
     * multipart/form-data 请求（ASR 上传音频用）。
     *
     * 为什么不复用 requestCurl：那个方法的 $body 是 string，传数组会
     * 被 curl 当成 multipart 但类型声明过不去。单独开一个方法比改签名安全。
     *
     * $fields 里可以是普通字符串（变成表单项），也可以是 CURLFile
     * （变成文件项，curl 自动生成 boundary 和 filename）。
     *
     * @param array<string,mixed>  $fields
     * @param array<string,string> $headers 不要传 Content-Type，
     *                                       curl 会自己带 boundary，手写反而会冲突
     * @return array{status:int,body:string,error:string,errno:int}
     */
    public static function requestMultipart(
        string $url,
        array $headers,
        array $fields,
        int $timeout,
    ): array {
        $ch = curl_init($url);
        if ($ch === false) {
            return ['status' => 0, 'body' => '', 'error' => 'curl_init 失败', 'errno' => -1];
        }

        curl_setopt_array($ch, [
            CURLOPT_POST           => true,
            CURLOPT_POSTFIELDS     => $fields,
            CURLOPT_HTTPHEADER     => self::formatHeaders($headers),
            CURLOPT_TIMEOUT        => $timeout,
            // 建连超时 30 秒。
            //
            // 2026-10-30 定为 30 秒。依据：
            //   · 本机到上游链路实测 ICMP 丢包 16.7%，TLS 握手需 4 个往返，
            //     正常情况下握手 0.31-0.33 秒，偶发抖到 1-3 秒。
            //   · 首次上线阶段上游可能冷启动，握手会更慢。
            //   · 但也不能跟着总超时一起放到 600 秒 —— 连接建立（DNS+TCP+TLS）
            //     本质上是个「行就行、不行就不行」的阶段，30 秒足够覆盖所有正常情况，
            //     超过就说明链路有问题，早点放弃换下一次尝试更有价值。
            CURLOPT_CONNECTTIMEOUT => 30,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_FORBID_REUSE   => true,
        ]);

        $resp = curl_exec($ch);
        $status = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
        $error = curl_error($ch);
        $errno = curl_errno($ch);

        @curl_close($ch);

        return [
            'status' => $status,
            'body'   => is_string($resp) ? $resp : '',
            'error'  => $error,
            'errno'  => $errno,
        ];
    }

    /**
     * @param array<string,string> $headers
     * @return string[]
     */
    private static function formatHeaders(array $headers): array
    {
        $out = [];
        foreach ($headers as $k => $v) {
            $out[] = $k . ': ' . $v;
        }
        return $out;
    }

    /**
     * 把响应里的 model 字段统一改成用户请求时用的名字。
     *
     * ── 为什么必须无条件改写 ──
     *
     * model:internlm3`"model":"internlm3"`
     * —— 上游自己改了名。如果不改写，客户端拿到的 model 和它请求的对不上，
     * 上游的真实模型名就漏出去了。
     *
     * 所以判定条件不是"displayModel !== actualModel"（那要求提前知道上游会返回什么），
     * 而是"响应里的 model 值 !== displayModel"就改。
     *
     * ── 为什么用正则而不是 json_decode + json_encode ──
     *
     *   1. 后者会改变字段顺序、丢失上游私有字段（如 prompt_logprobs）；
     *   2. 流式分块里可能夹着 [DONE] 或注释，不是合法 JSON；
     *   3. 每个分块 decode+encode 有性能开销。
     *
     * 正则 `"model"\s*:\s*"[^"]*"` 不会误伤内容里的同名字符串
     * （正文里的引号是 `\"` 转义过的，前导引号是 `\"` 而非 `"`）。
     */
    public static function rewriteModelField(string $data, string $displayModel, ?string $actualModel = null): string
    {
        if (!str_contains($data, 'model')) {
            return $data;
        }

        $safe = addcslashes($displayModel, '"\\');

        // 快路径：知道上游用的名字且确实存在，直接用 str_replace（比正则快一个量级）
        if ($actualModel !== null && $actualModel !== '' && $actualModel !== $displayModel) {
            $needle = '"model":"' . $actualModel . '"';
            if (str_contains($data, $needle)) {
                return str_replace($needle, '"model":"' . $safe . '"', $data);
            }
            // 上游可能带空格
            $pattern = '/"model"\s*:\s*"' . preg_quote($actualModel, '/') . '"/';
            if (preg_match($pattern, $data)) {
                return preg_replace($pattern, '"model":"' . $safe . '"', $data, 1) ?? $data;
            }
        }

        // 兜底：不管上游返回什么名字，全部 model 字段一律改成 displayModel。
        //
        // ⚠️ 不能只改第一个（limit=1）—— curl 的一个 WRITEFUNCTION 回调里
        // 可能粘了多个 SSE 帧（TCP 粘包），实测 33 帧里就有中间几帧的 model 没被改到。
        // 所以必须全局替换。
        //
        // 安全性：正文里的引号是转义过的（\"model\"），正则要求前导是裸引号，
        // 不会误伤用户消息内容里的同名文字。
        $result = preg_replace('/"model"\s*:\s*"[^"]*"/', '"model":"' . $safe . '"', $data);

        return $result ?? $data;
    }

    /**
     * 从响应体里提取上游错误信息。
     */
    public static function extractError(string $body): ?array
    {
        $json = json_decode($body, true);
        if (!is_array($json) || !isset($json['error'])) {
            return null;
        }
        $err = $json['error'];
        return [
            'message' => is_array($err) ? (string) ($err['message'] ?? '') : (string) $err,
            'type'    => is_array($err) ? (string) ($err['type'] ?? 'upstream_error') : 'upstream_error',
            'code'    => is_array($err) ? (string) ($err['code'] ?? '') : '',
        ];
    }
}

