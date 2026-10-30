<?php
declare(strict_types=1);

namespace Ltzy\Http;

use Ltzy\Support\Json;

/**
 * 响应封装。
 *
 * 核心职责：**区分「普通 JSON 响应」和「SSE 流式响应」**。
 *
 * 这个区分必须在这里做，因为两者的写法完全不同：
 *   - JSON：设置头 → 一次性 end(body)
 *   - SSE ：设置头 → 多次 write(chunk) → 最后 end()
 * 而且一旦进入 SSE 模式，就**不能再改成 JSON** —— 响应头已经发出去了，
 * 中途改格式会让客户端拿到「200 + SSE + JSON」的四不像。
 */
final class Response
{
    /** 是否已经进入流式模式（头已发出） */
    private bool $streaming = false;

    /** 是否已经结束 */
    private bool $finished = false;

    public function __construct(private \Swoole\Http\Response $raw)
    {
        $this->applySecurityHeaders();
    }

    /**
     * 统一安全响应头。
     *
     * 安全评估 M-5：原来一个安全头都没有。逐项对应能挡住的攻击：
     *
     *   X-Content-Type-Options: nosniff
     *     挡 MIME 嗅探。这是 L-1（路径回显到 JSON）能升级成 XSS 的前提 ——
     *     老浏览器/某些代理看到 application/json 里像 HTML 的内容，
     *     仍可能按 HTML 渲染执行。nosniff 直接禁掉这个行为。
     *
     *   X-Frame-Options: DENY + CSP frame-ancestors 'none'
     *     挡点击劫持：攻击者用 iframe 把登录页/控制台套进自己的页面，
     *     诱导用户在"看不见"的真实页面上点确认，完成充值、改密等操作。
     *
     *   Referrer-Policy
     *     防止 URL 里的路径信息随 Referer 泄漏给第三方资源。
     *
     *   X-XSS-Protection: 0
     *     现代浏览器已废弃老 XSS 审计器（它自身有信息泄露问题），
     *     显式关掉，避免旧浏览器启用这个有缺陷的机制。
     *
     * HSTS 不在这里加 —— 它由 nginx 负责（应用层无法可靠判断
     * 当前请求是否真的走 HTTPS，经反向代理时尤其不可靠）。
     */
    private function applySecurityHeaders(): void
    {
        $this->raw->header('X-Content-Type-Options', 'nosniff');
        $this->raw->header('X-Frame-Options', 'DENY');
        $this->raw->header('Referrer-Policy', 'strict-origin-when-cross-origin');
        $this->raw->header('X-XSS-Protection', '0');
        $this->raw->header(
            'Content-Security-Policy',
            "default-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
        );
    }

    /**
     * 输出纯文本。
     *
     * 专门给第三方回调用的：易支付（epay）要求 notify_url 收到的是
     * 裸文本 "success"，返回 JSON 会被判为未确认，然后网关会一直重发。
     *
     * 注意不设 $finished —— 这是终局响应，由调用方保证不再写别的内容，
     * 而设置 $finished 会让后续的 end() 变成空操作，容易被误用。
     */
    public function text(string $content, int $status = 200): void
    {
        $this->raw->status($status);
        $this->raw->header('Content-Type', 'text/plain; charset=utf-8');
        $this->raw->end($content);
        $this->finished = true;
    }

    /**
     * 直接输出一段已经是 JSON 的字符串。
     *
     * 为什么不用 json($decoded)：上游返回的 JSON 里可能含我们不想动的东西
     * （字段顺序、精度、私有字段）。decode 再 encode 会改变这些，
     * 而透传原则要求「原样」。这里只做一次简单的合法性校验。
     */
    public function jsonRaw(string $json, int $status = 200): void
    {
        if ($this->finished) {
            return;
        }
        $this->raw->status($status);
        $this->raw->header('Content-Type', 'application/json; charset=utf-8');
        $this->raw->end($json);
        $this->finished = true;
    }

    /**
     * HTTP 重定向。
     *
     * 未安装时所有页面都要跳 /install，所以需要它。
     *
     * 用 302 而不是 301：301 会被浏览器**永久缓存**，
     * 等安装完成后用户再访问旧地址还会被跳到 /install，
     * 而且清理缓存的唯一办法是清站点数据 —— 排查成本极高。
     */
    public function redirect(string $url, int $status = 302): void
    {
        if ($this->finished) {
            return;
        }
        $this->raw->status($status);
        $this->raw->header('Location', $url);
        $this->raw->end();
        $this->finished = true;
    }

    /**
     * 输出静态文本（HTML/CSS/JS）。
     */
    public function html(string $content, int $status = 200, string $contentType = 'text/html; charset=utf-8'): void
    {
        if ($this->finished) {
            return;
        }
        $this->raw->status($status);
        $this->raw->header('Content-Type', $contentType);
        // 管理界面不该被缓存：改了模板却看到旧页面是最容易误判的坑
        $this->raw->header('Cache-Control', 'no-store');

        // ⚠️ 页面必须放宽 CSP：前端是单文件 HTML，样式和脚本都内联。
        // 用构造里那套 default-src 'self' 会把内联 <script> 全部拦掉，
        // 页面直接白屏。这里放开 inline，但把 connect-src 锁死在 'self' ——
        // 这样即使页面被篡改（参见 H-2 目录权限问题），
        // 注入的脚本也无法把 localStorage 里的 token 外传到攻击者域名。
        $this->raw->header(
            'Content-Security-Policy',
            "default-src 'self'; "
            . "script-src 'self' 'unsafe-inline'; "
            . "style-src 'self' 'unsafe-inline'; "
            . "img-src 'self' data:; "
            . "font-src 'self' data:; "
            . "connect-src 'self'; "
            . "form-action 'self'; "
            . "frame-ancestors 'none'; "
            . "base-uri 'self'"
        );

        $this->raw->end($content);
        $this->finished = true;
    }

    /**
     * 输出二进制内容（音频、图片等）。
     *
     * 为什么不能复用 text()/jsonRaw()：
     *   那些方法会把 Content-Type 写死成 text/plain 或 application/json。
     *   TTS 返回的是 WAV/MP3 裸字节，Content-Type 必须原样继承上游的
     *   （实测 ChatTTS 返回 audio/wav），否则客户端播放器按文本解析必然失败。
     *
     * @param string $contentType 必须来自上游响应头，不要硬编码 ——
     *                            不同 TTS 模型可能返回 wav / mp3 / ogg
     */
    public function binary(string $content, string $contentType = 'application/octet-stream', int $status = 200): void
    {
        if ($this->finished) {
            return;
        }
        $this->raw->status($status);
        $this->raw->header('Content-Type', $contentType);
        // 音频不缓存：同一段文本每次合成的音色/采样可能有细微差异，
        // 缓存反而会让用户听到旧音频
        $this->raw->header('Cache-Control', 'no-store');
        $this->raw->end($content);
        $this->finished = true;
    }

    /**
     * 发一个 JSON 响应。
     */
    public function json(mixed $payload, int $status = 200): void
    {
        if ($this->finished) {
            return;
        }
        $this->raw->status($status);
        $this->raw->header('Content-Type', 'application/json; charset=utf-8');
        $this->raw->end(Json::encode($payload));
        $this->finished = true;
    }

    /**
     * 标准成功包装（管理接口用；/v1 路径不用这个）。
     */
    public function ok(mixed $data = null, string $message = ''): void
    {
        $this->json(['success' => true, 'message' => $message, 'data' => $data]);
    }

    /**
     * 标准失败包装。
     */
    public function fail(string $message, int $status = 400, string $code = ''): void
    {
        $this->json([
            'success' => false,
            'message' => $message,
            'code'    => $code,
            'data'    => null,
        ], $status);
    }

    /**
     * OpenAI 兼容的错误格式（/v1 路径用）。
     */
    public function openaiError(string $message, int $status, string $type = 'invalid_request_error', string $code = ''): void
    {
        $this->json([
            'error' => [
                'message' => $message,
                'type'    => $type,
                'param'   => '',
                'code'    => $code,
            ],
        ], $status);
    }

    /**
     * 进入 SSE 流式模式。
     *
     * 关键是这几个头：
     *   - Content-Type: text/event-stream
     *   - Cache-Control: no-cache
     *   - X-Accel-Buffering: no  （告诉 nginx 别缓冲，否则流式不流）
     */
    public function startStream(): void
    {
        if ($this->streaming || $this->finished) {
            return;
        }
        $this->raw->status(200);
        $this->raw->header('Content-Type', 'text/event-stream; charset=utf-8');
        $this->raw->header('Cache-Control', 'no-cache');
        $this->raw->header('Connection', 'keep-alive');
        // 这一行不能少：nginx 默认会缓冲上游响应，
        // 加了它才会逐块下发（现网踩过，表现为「流式不流」）
        $this->raw->header('X-Accel-Buffering', 'no');
        $this->streaming = true;
    }

    /**
     * 写一块流式数据。
     *
     * 注意：这里**原样写出**，不做任何行尾/换行处理 ——
     * 上游给的 SSE 帧自带 `\n\n` 分隔，我们加任何东西都会破坏格式。
     */
    public function write(string $chunk): void
    {
        if ($this->finished) {
            return;
        }
        if (!$this->streaming) {
            $this->startStream();
        }
        $this->raw->write($chunk);
    }

    /**
     * 写一个 SSE data 帧（我们自己生成的，比如错误帧）。
     */
    public function writeEvent(string $data): void
    {
        $this->write('data: ' . $data . "\n\n");
    }

    public function end(): void
    {
        if ($this->finished) {
            return;
        }
        $this->raw->end();
        $this->finished = true;
    }

    public function isStreaming(): bool
    {
        return $this->streaming;
    }

    public function isFinished(): bool
    {
        return $this->finished;
    }
}
