<?php
declare(strict_types=1);

namespace Ltzy;

use Ltzy\Billing\Billing;
use Ltzy\Db\Connection;
use Ltzy\Domain\Channel\ChannelSourceInterface;
use Ltzy\Domain\Account\AccountRepository;
use Ltzy\Domain\User\AuthCache;
use Ltzy\Domain\User\Authenticator;
use Ltzy\Domain\User\UserRepository;
use Ltzy\Http\Controller\AdminController;
use Ltzy\Http\Controller\AuthController;
use Ltzy\Http\Controller\AccountController;
use Ltzy\Http\Controller\AudioController;
use Ltzy\Http\Controller\EmbeddingController;
use Ltzy\Http\Controller\ImageController;
use Ltzy\Http\Controller\InstallController;
use Ltzy\Http\Controller\MultiProtocolController;
use Ltzy\Http\Controller\PublicController;
use Ltzy\Http\Controller\RechargeController;
use Ltzy\Http\Controller\RelayController;
use Ltzy\Http\Controller\RerankController;
use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Install\Installer;
use Ltzy\Payment\PaymentService;
use Ltzy\Relay\AudioService;
use Ltzy\Relay\EmbeddingService;
use Ltzy\Relay\KeyPoolManager;
use Ltzy\Relay\PromptPacker;
use Ltzy\Relay\RelayService;
use Ltzy\Relay\RerankService;
use Ltzy\Security\CaptchaStore;
use Ltzy\Security\LoginGuard;
use Ltzy\Security\RateLimiter;
use Ltzy\Security\RegisterGuard;
use Ltzy\Support\Ctx;
use Ltzy\Support\Logger;

/**
 * 路由表。
 *
 * 三类用户：
 *   大众     /  /plaza     不登录，拿 BaseURL + 模型 ID 直接用
 *   登录用户 /login        统一入口，按 is_manager 决定显示哪些页签
 *   我们     /admin        管渠道、定价、开经销商账号
 */
final class Router
{
    private UserRepository $users;
    private Authenticator $auth;
    private Billing $billing;
    private AccountRepository $accounts;
    private LoginGuard $guard;
    private AuthController $authCtl;
    private CaptchaStore $captchas;
    private RegisterGuard $regGuard;
    private RateLimiter $limiter;
    private PaymentService $payment;
    private RechargeController $recharge;

    public function __construct(
        private readonly ChannelSourceInterface $channels,
        private readonly KeyPoolManager $pools,
        private readonly Connection $db,
        private readonly LoginGuard $guardRef,
        private readonly array $cfg,
        private readonly ?AuthCache $authCache = null,
        ?CaptchaStore $captchas = null,
        ?RegisterGuard $regGuard = null,
    ) {
        $this->users = new UserRepository($db);
        // 鉴权缓存：把「每次请求查一次 tokens JOIN users」降到 5 秒一次。
        // 压测显示这个查询的规划时间（4.6ms）比执行时间（0.2ms）高 20 倍，
        // 是带鉴权接口 QPS 只有无鉴权一半的主因。
        $this->auth = new Authenticator($this->users, $authCache);
        $this->accounts = new AccountRepository($db);
        $this->billing = new Billing($db, $this->accounts, $this->cfg);
        $this->guard = $guardRef;
        // 验证码与注册限流都放内存：单进程协程，生命周期短，不值得上 Redis
        $this->captchas = $captchas ?? new CaptchaStore();
        $this->regGuard = $regGuard ?? new RegisterGuard();
        // 通用限流器：验证码生成、下单、等需要"限个大概"的场景共用一份。
        // 放在这里而不是各控制器内部 new，是因为 Swoole 单进程里
        // 一个实例就是全进程共享状态，各建各的等于各限各的，限不住。
        $this->limiter = new RateLimiter();
        $this->authCtl = new AuthController(
            $this->users,
            $this->auth,
            $this->guard,
            $this->captchas,
            $this->regGuard,
        );

        // 支付配置：payment 段本身没有 site_url，回调地址要用站点地址拼，
        // 这里合并一次，PaymentService 内部只读一份 cfg。
        $payCfg = (array) ($this->cfg['payment'] ?? []);
        $payCfg['site_url'] = (string) ($this->cfg['site_url'] ?? '');
        $this->payment = new PaymentService($db, $payCfg);
        $this->recharge = new RechargeController($this->payment, $payCfg, $this->limiter);
    }

    /**
     * 定时清理安全组件的过期数据（由 Server 的 Timer 每分钟调用）。
     */
    public function gcSecurity(): void
    {
        $this->captchas->gc();
        $this->regGuard->gc();

        // 超时未支付的订单标为已关闭。
        // 不删记录 —— 对账要看「用户下过单但没付」，删了就没法判断
        // 是「没人想充」还是「支付流程有坑」。
        try {
            $closed = $this->payment->closeExpired();
            if ($closed > 0) {
                Logger::info('payment.orders_closed', ['count' => $closed]);
            }
        } catch (\Throwable $e) {
            Logger::warn('payment.close_failed', ['error' => Logger::mask($e->getMessage())]);
        }
    }

    public function dispatch(\Swoole\Http\Request $rawReq, \Swoole\Http\Response $rawResp): void
    {
        $t0 = microtime(true);

        // ⚠️ 隐藏技术栈。
        // Swoole 默认回 `Server: swoole-http-server`，等于告诉扫描器
        // "这是个 Swoole 服务"，可以进一步定位 PHP 版本和已知 CVE。
        // `server_header` 配置项对 Coroutine\Http\Server 不生效，只能手设。
        // 生产环境 nginx 反代会覆盖，但直连（或内网穿透）时就会暴露。
        $rawResp->header('Server', 'nginx');

        // 每个请求一个独立 ID，贯穿所有日志
        $rid = Ctx::initRequestId();

        $req = new Request($rawReq);
        $resp = new Response($rawResp);
        $path = $req->path();
        $method = $req->method();

        // 安全评估 L-2：HEAD 在语义上等价 GET（只是不返回 body）。
        // 原来只匹配 GET，导致 HEAD / 返回 404 —— UptimeRobot、nginx
        // 健康检查等大量监控工具默认用 HEAD 探活，会把站点误判为宕机。
        // 这里统一映射到 GET 分支，body 由 Response 层照常结束，
        // 客户端按 HTTP 规范会丢弃 HEAD 的 body。
        if ($method === 'HEAD') {
            $method = 'GET';
        }

        // ── 安装状态闸门 ──
        //
        // ⚠️ 必须放在身份解析（auth->resolve）之前。
        // 未安装时数据库还不存在，一旦走到鉴权就会抛连接异常，
        // 用户看到的是 500 而不是安装引导页 —— 那是最糟的首屏体验。
        //
        // 两条规则：
        //   未安装 → 除了 /install 和 /api/install/*，其余一律挡掉
        //   已安装 → /install 与 /api/install/* 返回 404（入口彻底关闭）
        $installer = new Installer();
        $isInstallRoute = $path === '/install' || str_starts_with($path, '/api/install/');

        if (!$installer->installed()) {
            if (!$isInstallRoute) {
                // 接口调用给 503 + 明确提示，页面跳转到安装向导。
                // 这里不用 302 跳 API 请求 —— 客户端拿到 302 跟到 HTML
                // 后会解析失败，报一个和「没装」毫不相关的错误。
                if (str_starts_with($path, '/api/') || str_starts_with($path, '/v1/')) {
                    $resp->fail('系统尚未安装，请先在浏览器打开 /install 完成初始化', 503, 'not_installed');
                } else {
                    $resp->redirect('/install');
                }
                return;
            }
        } elseif ($isInstallRoute) {
            // 装完之后安装入口一律消失：
            // 一个可重复访问的安装页 = 把重装权限开放给全互联网
            $resp->fail('页面不存在', 404, 'not_found');
            return;
        }

        Ctx::set('free_anonymous_enabled', $this->cfg['free_anonymous_enabled'] ?? false);

        // 身份解析放在路由之前 —— 所有端点都可能需要（公开接口用来展示"已登录"状态）
        $this->auth->resolve($req->header('authorization'));

        try {
            $this->route($req, $resp, $path, $method);
        } catch (\Throwable $e) {
            Logger::error('http.unhandled', [
                'request_id' => $rid,
                'path'       => $path,
                'error'      => Logger::mask($e->getMessage()),
                'trace'      => $e->getFile() . ':' . $e->getLine(),
            ]);
            if (!$resp->isFinished() && !$resp->isStreaming()) {
                $resp->json([
                    'success' => false,
                    'message' => '服务器内部错误（request id: ' . $rid . '）',
                    'data'    => null,
                ], 500);
            } else {
                $resp->end();
            }
        }

        // 请求完成日志。
        //
        // ⚠️ 用 debug 而不是 info：压测发现这条日志是单进程的吞吐瓶颈 ——
        // 每个请求都要做一次 JSON 编码 + 同步 fwrite，500 并发时把 QPS 压到 1158。
        // 正常请求没必要逐条打印（nginx 的 access log 也是同样的取舍），
        // 但慢请求和出错的请求升到 info/warn 保留下来。
        $ms = (int) ((microtime(true) - $t0) * 1000);
        $ctx = [
            'request_id' => $rid,
            'method'     => $method,
            'path'       => $path,
            'ms'         => $ms,
            'ip'         => $req->clientIp(),
            'stream'     => $resp->isStreaming(),
            'user_id'    => Ctx::int('user_id'),
        ];
        if ($ms > 3000) {
            $ctx['slow'] = true;
            Logger::warn('http.request_slow', $ctx);
        } else {
            Logger::debug('http.request', $ctx);
        }
    }

    private function route(Request $req, Response $resp, string $path, string $method): void
    {
        // ── 安装向导（只在未安装时可达，闸门在 dispatch 里）──
        if ($path === '/install' && $method === 'GET') {
            (new InstallController(new Installer()))->page($req, $resp);
            return;
        }
        if ($path === '/api/install/check' && $method === 'POST') {
            (new InstallController(new Installer()))->check($req, $resp);
            return;
        }
        if ($path === '/api/install/test-db' && $method === 'POST') {
            (new InstallController(new Installer()))->testDb($req, $resp);
            return;
        }
        if ($path === '/api/install/run' && $method === 'POST') {
            (new InstallController(new Installer()))->run($req, $resp);
            return;
        }

        $public = new PublicController($this->channels, $this->pools, $this->billing, $this->db);
        $account = new AccountController(
            $this->accounts,
            $this->auth,
            $this->billing,
            $this->channels,
            (int) ($this->cfg['log_retention_days'] ?? 180),
        );
        $admin = new AdminController($this->db, $this->channels, $this->pools, $this->billing, $this->auth, $this->guard);

        // 生图单独一个控制器：形态和对话差太远（非流式、按张计费、不需要协议转换），
        // 塞进 RelayService 会让那条主链路到处是 if。详见 ImageController 的类注释。
        $image = new ImageController(
            $this->channels,
            $this->pools,
            $this->billing,
            $this->users,
            '/v1/images/generations',
            (int) ($this->cfg['relay_timeout'] ?? 300),
            (int) ($this->cfg['retry_times'] ?? 3),
        );

        // 懒加载：只有走到中继才构造 RelayService
        $relay = function (): RelayController {
            // 破甲包装器：只对配置白名单里的模型生效（红队测试用）。
            // 白名单外的模型 pack() 原样返回，主链路零开销。
            $packer = new PromptPacker(
                (bool) ($this->cfg['jailbreak']['enabled'] ?? false),
                (array) ($this->cfg['jailbreak']['models'] ?? []),
            );
            return new RelayController(new RelayService(
                $this->channels,
                $this->pools,
                $this->billing,
                $this->users,
                $this->cfg['relay_timeout'] ?? 300,
                $this->cfg['retry_times'] ?? 3,
            ), $packer);
        };

        // 多协议入口（Responses / Claude / Gemini 共用）
        $multi = function () use ($relay, $resp): MultiProtocolController {
            return new MultiProtocolController(
                new RelayService(
                    $this->channels,
                    $this->pools,
                    $this->billing,
                    $this->users,
                    $this->cfg['relay_timeout'] ?? 300,
                    $this->cfg['retry_times'] ?? 3,
                )
            );
        };

        // Gemini 路径带模型名：/v1beta/models/{model}:generateContent
        if (preg_match('#^/v1beta/models/(.+?):(generateContent|streamGenerateContent)$#', $path, $gm)) {
            $multi()->handle('gemini-generate', $req, $resp, ['model' => $gm[1], 'action' => $gm[2]]);
            return;
        }

        // 路径参数：/api/token/123、/api/manage/accounts/45/models
        if (preg_match('#^/api/token/(\d+)$#', $path, $m)) {
            $req->setParam('id', $m[1]);
        }

        switch (true) {
            // ── 充值 ───────────────────────────────
            // config / notify / return 免登录：
            //   config 是前端首屏要拿的档位；notify 是支付机构发起的，
            //   它没有我们的 token，安全性靠验签；return 只做展示不写库。
            case $path === '/api/recharge/config' && $method === 'GET':
                $this->recharge->config($req, $resp);
                return;

            case $path === '/api/recharge/notify':
                $this->recharge->notify($req, $resp);
                return;

            case $path === '/api/recharge/return' && $method === 'GET':
                $this->recharge->returnPage($req, $resp);
                return;

            case $path === '/api/recharge/create' && $method === 'POST':
                $this->recharge->create($req, $resp);
                return;

            case $path === '/api/recharge/orders' && $method === 'GET':
                $this->recharge->orders($req, $resp);
                return;

            // ── 公开 ───────────────────────────────
            case $path === '/api/status' && $method === 'GET':
                $public->status($req, $resp);
                return;

            case $path === '/api/site' && $method === 'GET':
                $public->site($req, $resp);
                return;

            case $path === '/api/model/plaza' && $method === 'GET':
                $public->plaza($req, $resp);
                return;

            case $path === '/v1/models' && $method === 'GET':
                $public->models($req, $resp);
                return;

            // ── 统一登录 ───────────────────────────
            case $path === '/api/login' && $method === 'POST':
                $this->authCtl->login($req, $resp);
                return;

            case $path === '/api/register' && $method === 'POST':
                $this->authCtl->register($req, $resp);
                return;

            case $path === '/api/captcha' && $method === 'GET':
                // 安全评估 M-2：验证码图片是 GD + FreeType 实时渲染，
                // 纯 CPU 密集。原来完全没有限流 —— 实测 60 次连发全部 200，
                // 单进程协程模型下可以吃满 CPU 拖垮整个服务；
                // 同时等于给 OCR 训练免费供给样本。
                // 限 12 次/分钟：正常用户（注册、登录偶尔刷新）远用不到。
                if (!$this->limiter->allow('captcha:ip:' . $req->clientIp(), 12, 60)) {
                    // 注意：route() 内没有 $rawResp（只有 dispatch() 才有），
                    // 想加响应头必须走 $resp 暴露的方法，不能直接碰 Swoole 对象 ——
                    // 否则就是 "Call to a member function header() on null" 的 500。
                    $resp->fail('请求太频繁，请稍后再试', 429, 'rate_limited');

                    return;
                }
                $this->authCtl->captcha($req, $resp);
                return;

            case $path === '/api/logout' && $method === 'POST':
                $this->authCtl->logout($req, $resp);
                return;

            case $path === '/api/me' && $method === 'GET':
                $this->authCtl->me($req, $resp);
                return;

            // 令牌管理（管理员和经销商都能用）
            case $path === '/api/token' && $method === 'GET':
                $this->authCtl->listTokens($req, $resp);
                return;

            case $path === '/api/token' && $method === 'POST':
                $this->authCtl->createToken($req, $resp);
                return;

            case (bool) preg_match('#^/api/token/\d+$#', $path) && $method === 'DELETE':
                $this->authCtl->deleteToken($req, $resp);
                return;

            // ── 经销商 ─────────────────────────────
            case $path === '/api/account/self' && $method === 'GET':
                $account->self($req, $resp);
                return;

            case $path === '/api/account/models' && $method === 'GET':
                $account->models($req, $resp);
                return;

            case $path === '/api/account/logs' && $method === 'GET':
                $account->logs($req, $resp);
                return;

            case $path === '/api/account/usage' && $method === 'GET':
                $account->usage($req, $resp);
                return;

            // ── 超管 ───────────────────────────────
            case str_starts_with($path, '/api/manage/'):
                $admin->handle($req, $resp, $path, $method);
                return;

            // ── 页面 ───────────────────────────────
            case ($path === '/' || $path === '/portal') && $method === 'GET':
                $this->page($resp, 'portal.html');
                return;

            case $path === '/plaza' && $method === 'GET':
                $this->page($resp, 'plaza.html');
                return;

            // 唯一的登录/后台入口。
            //
            // ⚠️ 只保留 /login 和 /console 两个路径，且它们返回**同一个页面**。
            //
            // 删除的其他路径及原因：
            //   /admin            任何人裸访问都能拿到完整后台页面（HTTP 200）
            //   /dealer 一类       路径名本身暗示"系统里有某种角色"
            //   /xxx/dashboard     同上，还多暴露了一层结构
            //   登录响应里的 redirect 字段 —— 直接告诉对方管理员后台在哪
            //
            // 现在登录与后台同页，登录后不跳转。外人无从得知：
            //   有几个角色、有几个后台、管理功能在哪里。
            case ($path === '/login' || $path === '/console') && $method === 'GET':
                $this->page($resp, 'console.html');
                return;

            // ── 中继（多协议）─────────────────────
            case $path === '/v1/chat/completions' && $method === 'POST':
                $relay()->chat($req, $resp);
                return;

            // OpenAI Responses API（新版标准）
            case $path === '/v1/responses' && $method === 'POST':
                $multi()->handle('openai-responses', $req, $resp);
                return;

            // Anthropic Claude 官方接口
            case ($path === '/v1/messages' || $path === '/v1/messages/count_tokens') && $method === 'POST':
                $multi()->handle('anthropic-messages', $req, $resp);
                return;

            // 图像生成（生图模型的唯一入口，非流式）
            case $path === '/v1/images/generations' && $method === 'POST':
                $image->generate($req, $resp);
                return;

            // Embedding
            case $path === '/v1/embeddings' && $method === 'POST':
                (new EmbeddingController(new EmbeddingService(
                    $this->channels,
                    $this->pools,
                    $this->billing,
                    $this->users,
                    $this->cfg['relay_timeout'] ?? 300,
                    $this->cfg['retry_times'] ?? 3,
                )))->create($req, $resp);
                return;

            // Rerank（Jina / Cohere 风格，不是 OpenAI 标准）
            case $path === '/v1/rerank' && $method === 'POST':
                (new RerankController(new RerankService(
                    $this->channels,
                    $this->pools,
                    $this->billing,
                    $this->users,
                    $this->cfg['relay_timeout'] ?? 300,
                    $this->cfg['retry_times'] ?? 3,
                )))->create($req, $resp);
                return;

            // 音频：ASR（multipart）与 TTS（JSON 进、二进制出）
            case ($path === '/v1/audio/transcriptions' || $path === '/v1/audio/translations') && $method === 'POST':
                $this->audioCtl()->transcriptions($req, $resp);
                return;

            case $path === '/v1/audio/speech' && $method === 'POST':
                $this->audioCtl()->speech($req, $resp);
                return;

            // 容错：漏写 /v1 时（生产由 nginx rewrite 处理，本地直连也支持）
            case $path === '/chat/completions' && $method === 'POST':
                $relay()->chat($req, $resp);
                return;

            // ── 兜底 ───────────────────────────────
            default:
                // 安全评估 L-1：原来把请求路径原样拼进错误消息，
                // GET /<script>alert(1)</script> 会得到
                //   {"error":{"message":"Invalid URL (GET /<script>alert(1)</script>)"}}
                // Content-Type 是 application/json，现代浏览器不会执行；
                // 但配合「缺 X-Content-Type-Options: nosniff」，
                // 老浏览器或某些代理会做 MIME 嗅探并当 HTML 渲染 → XSS。
                // 双保险：① 不回显路径原文 ② 响应头补 nosniff（见 Response）
                $resp->json([
                    'error' => [
                        'message' => 'Invalid URL',
                        'type'    => 'invalid_request_error',
                        'param'   => '',
                        'code'    => 'not_found',
                    ],
                ], 404);
        }
    }

    /**
     * 音频控制器（懒加载）。
     *
     * 不放在构造函数里 new：AudioService 依赖 4 个组件，而绝大多数请求
     * 根本不碰音频端点，构造期创建纯属浪费。缓存成单例避免每次请求重建。
     */
    private function audioCtl(): AudioController
    {
        static $ctl = null;
        if ($ctl === null) {
            $ctl = new AudioController(new AudioService(
                $this->channels,
                $this->pools,
                $this->billing,
                $this->users,
                $this->cfg['relay_timeout'] ?? 300,
                $this->cfg['retry_times'] ?? 3,
            ));
        }

        return $ctl;
    }

    /**
     * 输出 web/ 下的静态页面。
     */
    private function page(Response $resp, string $file): void    {
        $full = dirname(__DIR__) . '/web/' . $file;
        $html = @file_get_contents($full);
        if ($html === false) {
            $resp->html('<h1>' . $file . ' 缺失</h1><p>检查 web/ 目录</p>', 500);
            return;
        }
        $resp->html($html);
    }
}
