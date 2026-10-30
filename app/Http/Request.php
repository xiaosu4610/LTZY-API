<?php
declare(strict_types=1);

namespace Ltzy\Http;

use Ltzy\Support\Json;

/**
 * 请求封装。
 *
 * 包一层而不是直接用 Swoole 的 Request：业务代码不应该到处
 * `$request->server['request_uri']`、`$request->get[]`，
 * 换实现时（比如以后从 Swoole 换到别的东西）只改这一处。
 */
final class Request
{
    private ?array $json = null;
    private ?string $rawBody = null;

    public function __construct(private readonly \Swoole\Http\Request $raw)
    {
    }

    public function method(): string
    {
        return strtoupper($this->raw->server['request_method'] ?? 'GET');
    }

    public function path(): string
    {
        $uri = $this->raw->server['request_uri'] ?? '/';
        // 去掉 query string
        $q = strpos($uri, '?');
        return $q === false ? $uri : substr($uri, 0, $q);
    }

    public function query(string $key, ?string $default = null): ?string
    {
        return $this->raw->get[$key] ?? $default;
    }

    /**
     * 全部查询参数。
     *
     * 支付回调需要原样拿到网关发来的所有字段去验签 ——
     * 不能一个个 query() 取，因为签名算法要求「所有非空参数都参与」，
     * 漏一个字段就算不出正确签名。
     *
     * @return array<string,string>
     */
    public function queryAll(): array
    {
        $out = [];
        foreach (($this->raw->get ?? []) as $k => $v) {
            if (is_string($v)) {
                $out[(string) $k] = $v;
            }
        }

        return $out;
    }

    /**
     * 全部表单参数（application/x-www-form-urlencoded 或 multipart）。
     *
     * @return array<string,string>
     */
    public function postAll(): array
    {
        $out = [];
        foreach (($this->raw->post ?? []) as $k => $v) {
            if (is_string($v)) {
                $out[(string) $k] = $v;
            }
        }

        return $out;
    }

    public function header(string $name): ?string
    {
        // Swoole 的 header 键统一小写
        return $this->raw->header[strtolower($name)] ?? null;
    }

    /**
     * 上传的文件（multipart/form-data 里的 file 字段）。
     *
     * 供 ASR/TTS 这类需要传文件的端点使用。Swoole 已经把文件落到临时目录，
     * 这里只做结构归一化，把单文件和多文件统一成「一维数组套文件信息」。
     *
     * 返回结构：
     *   ['file' => ['name'=>..., 'type'=>..., 'tmp_name'=>..., 'size'=>...]]
     *
     * ⚠️ 临时文件由 Swoole 管理，请求结束后可能被清理 ——
     * 必须在当前请求内读完或转发完，不能留到下一个协程用。
     *
     * @return array<string,array{name:string,type:string,tmp_name:string,size:int}>
     */
    public function filesAll(): array
    {
        $out = [];
        foreach (($this->raw->files ?? []) as $k => $f) {
            if (!is_array($f) || !isset($f['tmp_name'])) {
                continue;
            }
            // 多文件上传时 Swoole 给的是 ['name'=>[...], 'tmp_name'=>[...]] 这种列式结构，
            // 这里只取第一个（ASR 场景一次传一个音频足够）
            $tmp = is_array($f['tmp_name']) ? ($f['tmp_name'][0] ?? '') : $f['tmp_name'];
            if (!is_string($tmp) || $tmp === '') {
                continue;
            }
            $out[(string) $k] = [
                'name'     => is_array($f['name']) ? (string) ($f['name'][0] ?? '') : (string) ($f['name'] ?? ''),
                'type'     => is_array($f['type'] ?? '') ? (string) ($f['type'][0] ?? '') : (string) ($f['type'] ?? ''),
                'tmp_name' => $tmp,
                'size'     => (int) (is_array($f['size'] ?? 0) ? ($f['size'][0] ?? 0) : ($f['size'] ?? 0)),
            ];
        }

        return $out;
    }

    /**
     * 原始请求体（只读一次，缓存起来避免重复读）。
     */
    public function rawBody(): string
    {
        if ($this->rawBody === null) {
            $this->rawBody = $this->raw->getContent() ?: '';
        }
        return $this->rawBody;
    }

    /**
     * 解析后的 JSON body，失败返回 null。
     *
     * 为什么容忍失败：非流式请求的 body 一定是 JSON，但
     * 解析失败时应该由业务层返回 400，而不是在这里抛异常打断整个请求链。
     */
    public function json(): ?array
    {
        if ($this->json === null) {
            $decoded = Json::tryDecode($this->rawBody());
            $this->json = is_array($decoded) ? $decoded : [];
        }
        return $this->json ?: null;
    }

    /**
     * 当前请求用的协议。
     *
     * 为什么不能写死 'http'：生产走 nginx 反代 + HTTPS，
     * 后端看到的连接永远是本地 http，如果按这个拼链接，
     * 给用户的接口地址就会是 http://，用户复制过去调不通（或被中间人看光）。
     *
     * 优先级：X-Forwarded-Proto（nginx 设的）> Swoole 的 https 标记 > http
     */
    public function scheme(): string
    {
        $proto = strtolower(trim((string) ($this->header('x-forwarded-proto') ?? '')));
        if ($proto === 'https' || $proto === 'http') {
            return $proto;
        }
        $https = strtolower((string) ($this->raw->server['https'] ?? ''));
        return ($https !== '' && $https !== 'off') ? 'https' : 'http';
    }

    /**
     * 对外地址，如 https://api.example.com。用于给用户展示接口地址、生成 curl 示例。
     *
     * 安全评估 M-6：原实现直接信任 Host 头 ——
     *   Host: evil.example.com  →  /api/site 返回 "base_url": "https://evil.example.com/v1"
     * 攻击者可借此做钓鱼：受害者从「官方站点」复制到的接口地址、curl 示例
     * 全是攻击者域名，之后所有请求（含 API Key）都会发到攻击者服务器。
     * 若将来在前面加 CDN/缓存层，还可能被缓存投毒扩散给其他用户。
     *
     * 修复：Host 必须在白名单内，否则回落到配置的 SITE_URL。
     * 这样无论攻击者怎么伪造 Host，对外展示的地址永远是官方地址。
     *
     * ── 开源后的调整 ──
     *
     * 原来白名单里写死了本项目自己的域名。别人拿去部署到自己的域名上，
     * 所有页面都会显示错误的接口地址 —— 属于「一行代码限定一个租户」的典型。
     * 现在改成从配置推导，零硬编码：
     *
     *   SITE_URL 里的 host      自动加入（配了就能用，不用重复配）
     *   SITE_ALLOWED_HOSTS      追加（一站多域名时用）
     *   127.0.0.1 / localhost   永远放行（本地开发）
     */
    public function origin(): string
    {
        $host = strtolower(trim((string) ($this->header('host') ?? '')));
        $hostOnly = trim(explode(':', $host)[0]);

        static $allowed = null;
        if ($allowed === null) {
            // 本机调试地址永远放行
            $allowed = ['127.0.0.1', 'localhost', '::1'];

            // SITE_URL 的主机名自动进白名单 —— 部署时只配一个 SITE_URL 就够
            $siteUrl = (string) (getenv('SITE_URL') ?: '');
            if ($siteUrl !== '') {
                $h = parse_url($siteUrl, PHP_URL_HOST);
                if (is_string($h) && $h !== '') {
                    $allowed[] = strtolower($h);
                }
            }

            // 额外允许的 Host（逗号分隔），用于一个站点绑定多个域名
            $extra = (string) (getenv('SITE_ALLOWED_HOSTS') ?: '');
            if ($extra !== '') {
                foreach (explode(',', $extra) as $h) {
                    $h = strtolower(trim($h));
                    if ($h !== '') {
                        $allowed[] = $h;
                    }
                }
            }

            $allowed = array_values(array_unique($allowed));
        }

        if ($hostOnly !== '' && in_array($hostOnly, $allowed, true)) {
            return $this->scheme() . '://' . $host;
        }

        // 未知 Host（伪造、扫描器、SNI 错配）→ 回落到配置的正式地址。
        // 连 SITE_URL 都没配时，退回当前请求自身的 scheme+host ——
        // 这是唯一不依赖任何硬编码、又能让安装向导首屏正常工作的选择。
        $site = rtrim((string) (getenv('SITE_URL') ?: ''), '/');
        if ($site !== '') {
            return $site;
        }

        return $this->scheme() . '://' . ($host !== '' ? $host : 'localhost');
    }

    public function clientIp(): string
    {
        // 优先 X-Real-IP（nginx 设的），其次 X-Forwarded-For 最后一跳，最后 remote_addr
        if ($ip = $this->header('x-real-ip')) {
            return $ip;
        }
        if ($xff = $this->header('x-forwarded-for')) {
            $parts = explode(',', $xff);
            return trim(end($parts));
        }
        // Swoole 的 remote_ip 是真实的 TCP 对端地址
        return $this->raw->server['remote_addr'] ?? '0.0.0.0';
    }

    public function bearerKey(): string
    {
        $auth = $this->header('authorization') ?? '';
        $key = preg_replace('/^Bearer\s+/i', '', $auth) ?? $auth;
        return trim($key);
    }

    /**
     * 路径参数（由 Router 在匹配时注入，如 /api/token/{id} 里的 id）。
     */
    private array $params = [];

    public function setParam(string $name, string $value): void
    {
        $this->params[$name] = $value;
    }

    public function param(string $name, ?string $default = null): ?string
    {
        return $this->params[$name] ?? $default;
    }

    /**
     * 取输入值，优先级：JSON body > query string。
     *
     * 为什么不区分 GET/POST：前端有时把参数放在 query（如筛选条件），
     * 有时放在 body，统一处理省得每个 controller 都判断。
     */
    public function input(string $key, mixed $default = null): mixed
    {
        $json = $this->json();
        if (is_array($json) && array_key_exists($key, $json)) {
            return $json[$key];
        }
        $q = $this->raw->get[$key] ?? null;
        return $q !== null ? $q : $default;
    }
}
