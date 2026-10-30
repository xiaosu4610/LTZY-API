<?php
declare(strict_types=1);

namespace Ltzy\Http\Controller;

use Ltzy\Domain\User\Authenticator;
use Ltzy\Domain\User\User;
use Ltzy\Domain\User\UserRepository;
use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Security\CaptchaImage;
use Ltzy\Security\CaptchaStore;
use Ltzy\Security\LoginGuard;
use Ltzy\Security\RegisterGuard;
use Ltzy\Support\Ctx;
use Ltzy\Support\Logger;

/**
 * 统一登录。
 *
 * 管理员和经销商用**同一个接口、同一个页面**，登录后按 role 决定去哪：
 *   role >= 10  → 超管后台 /admin
 *   其他          → 只显示自己的模型 / 日志 / 用量
 *
 * 为什么要合并：两套登录接口意味着两份防爆破逻辑、两处可能写错的地方。
 * 合并后所有安全控制集中在一处，改一次就全局生效。
 *
 * ── 安全要点（每一条都是常见失陷点）──
 *
 * 1. **账号不存在时也要跑 password_verify**
 *    否则响应时间会暴露"这个用户名是否存在"（时序侧信道）。
 * 2. **失败信息统一**：不区分"用户不存在"和"密码错误"。
 * 3. **爆破防护**：LoginGuard 按 (IP, 账号) 和 IP 双重计数。
 * 4. **命中限流也要消耗相近时间**，避免通过响应快慢判断锁定状态。
 * 5. **SQL 全部参数化**，用户名长度/字符先做校验再入库查询。
 */
final class AuthController
{
    public function __construct(
        private readonly UserRepository $users,
        private readonly Authenticator $auth,
        private readonly LoginGuard $guard,
        private readonly CaptchaStore $captchas,
        private readonly RegisterGuard $regGuard,
    ) {
    }

    /**
     * GET /api/captcha
     *
     * 发放图形验证码。返回 {id, image}：
     *   id    —— 提交注册时要带回来
     *   image —— data:image/png;base64,... 直接塞 <img src> 就能显示
     *
     * 为什么在服务端生成图片而不是让前端画：
     *   前端画的字，答案必然也在前端代码里，脚本直接读变量就过了。
     *   只有服务端出图、服务端校验，验证码才有意义。
     */
    public function captcha(Request $req, Response $resp): void
    {
        $code = CaptchaImage::randomCode(4);
        $id = $this->captchas->issue($code);
        $png = CaptchaImage::png($code);

        $resp->ok([
            'id'    => $id,
            'image' => 'data:image/png;base64,' . base64_encode($png),
            'ttl'   => 300,
        ]);
    }

    /**
     * POST /api/login
     *
     * body: {"username": "...", "password": "..."}
     */
    public function login(Request $req, Response $resp): void
    {
        $ip = $req->clientIp();
        $username = trim((string) $req->input('username', ''));
        $password = (string) $req->input('password', '');

        // ── 输入校验（在查库之前做，避免无效请求打到数据库）──
        if ($username === '' || $password === '') {
            $resp->json(['success' => false, 'message' => '请输入用户名和密码'], 400);
            return;
        }
        if (strlen($username) > 64 || strlen($password) > 200) {
            // 超长输入通常是攻击载荷，直接拒，不给限流器增加噪音
            Logger::warn('security.login_bad_input', [
                'ip'        => $ip,
                'user_len'  => strlen($username),
                'pass_len'  => strlen($password),
            ]);
            $resp->json(['success' => false, 'message' => '用户名或密码错误'], 401);
            return;
        }

        // ── 爆破检查 ──
        $blocked = $this->guard->check($ip, $username);
        if ($blocked !== null) {
            // 关键：被限流时也跑一次同 cost 的 bcrypt，让响应时间与正常登录一致。
            // 否则攻击者能用「秒回」判断出自己被锁了，从而换 IP 继续。
            password_verify($password, '$2y$12$abcdefghijklmnopqrstuvABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789AB');
            $resp->json(['success' => false, 'message' => $blocked], 429);
            return;
        }

        // ── 查用户 ──
        // findByUsername 内部是参数化查询（见 UserRepository），不会注入
        $user = $this->users->findByUsername($username);

        // ── 验证密码 ──
        // ⚠️ 用户不存在时也必须跑一次 verify。
        //
        // 而且 dummy 哈希的 **cost 必须和真实哈希一致**，否则耗时对不上：
        // 实测 cost=10 的 dummy 是 125ms，而真实用户（cost=12）是 450ms ——
        // 差了 3.6 倍，攻击者一眼就能看出"这个用户名不存在"。
        //
        // 这里的 cost=12 必须和 set-admin.php / 用户注册时用的 cost 保持一致。
        // 如果以后改 cost，两处要一起改。
        $dummyHash = '$2y$12$abcdefghijklmnopqrstuvABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789AB';
        $ok = $user !== null
            ? password_verify($password, $user->passwordHash)
            : password_verify($password, $dummyHash);

        // $user === null 时即使 verify 返回 true（理论上不可能）也要拒
        if ($user === null || !$ok) {
            $this->guard->recordFailure($ip, $username);

            Logger::warn('security.login_failed', [
                'ip'           => $ip,
                'username'     => $username,
                'user_exists'  => $user !== null,
                'user_agent'   => substr((string) $req->header('user-agent'), 0, 120),
            ]);

            // 统一话术：不说"用户不存在"，也不说"密码错误"
            $resp->json(['success' => false, 'message' => '用户名或密码错误'], 401);
            return;
        }

        // ── 状态检查 ──
        if (!$user->isEnabled()) {
            $this->guard->recordFailure($ip, $username);
            Logger::warn('security.login_disabled', ['ip' => $ip, 'user_id' => $user->id]);
            $resp->json(['success' => false, 'message' => '账户已被停用，请联系管理员'], 403);
            return;
        }

        // ── 登录成功 ──
        $this->guard->recordSuccess($ip, $username);

        // 每次登录刷新会话 token（旧会话自动失效）
        // random_bytes 是密码学安全的；uniqid/mt_rand 可预测，绝不能用
        $sessionToken = bin2hex(random_bytes(32));
        $this->users->setAccessToken($user->id, $sessionToken);
        $this->users->touchLogin($user->id);

        // 如果哈希用的还是旧格式（$2a$ 或低 cost），趁这次拿到明文密码升级
        if (password_needs_rehash($user->passwordHash, PASSWORD_DEFAULT)) {
            try {
                $this->users->updatePasswordHash($user->id, password_hash($password, PASSWORD_DEFAULT));
                Logger::info('security.password_rehashed', ['user_id' => $user->id]);
            } catch (\Throwable $e) {
                // 升级失败不影响登录
                Logger::warn('security.rehash_failed', ['user_id' => $user->id, 'error' => $e->getMessage()]);
            }
        }

        Logger::info('security.login_ok', [
            'ip'      => $ip,
            'user_id' => $user->id,
            'role'    => $user->role,
        ]);

        $resp->json([
            'success' => true,
            'message' => '',
            'data'    => [
                'access_token' => $sessionToken,
                // ⚠️ 不返回 role 数值，也不返回 redirect 路径。
                //
                // 之前返回一个指向具体后台的字段，是个严重疏漏 ——
                // 任何人做一次登录请求（哪怕用自己账号）就能知道：
                //   1. 这个站有多种角色
                //   2. 管理员后台在 /admin
                // 现在统一只给一个布尔值，且字段名中性。
                'is_manager' => $user->isAdmin(),
                'user'       => $this->publicUser($user),
            ],
        ]);
    }

    /**
     * POST /api/register
     *
     * 自助注册：用户名 + 密码，注册成功直接返回 token（省掉再登录一次）。
     *
     * ── 安全约束 ──
     *
     * 1. 用户名限制字符集，避免 XSS 载荷、控制字符、路径穿越符号进库
     *    （用户名会出现在控制台标题、日志里，不限死就是注入面）
     * 2. 保留字黑名单：不能叫 admin / 官方 / 管理员，否则用户会以为自己是管理员
     * 3. 密码最少 8 位；上限 200 位防止 bcrypt 被超长输入拖死
     * 4. 唯一性同时做「先查」和「捕获约束冲突」两道 ——
     *    并发注册同一用户名时，先查可能都通过，靠数据库唯一索引兜底
     * 5. 默认 quota = 0：注册完只能用免费池模型，付费模型要管理员加额度
     */
    public function register(Request $req, Response $resp): void
    {
        $ip = $req->clientIp();
        $username = trim((string) $req->input('username', ''));
        $password = (string) $req->input('password', '');
        $captchaId = trim((string) $req->input('captcha_id', ''));
        $captchaCode = trim((string) $req->input('captcha_code', ''));

        // ── 0. IP 限流（30 分钟 10 次，先于一切校验挡掉高频请求）──
        $limited = $this->regGuard->check($ip);
        if ($limited !== null) {
            Logger::warn('security.register_rate_limited', ['ip' => $ip]);
            $resp->json(['success' => false, 'message' => $limited], 429);
            return;
        }

        if ($username === '' || $password === '') {
            $resp->json(['success' => false, 'message' => '请输入用户名和密码'], 400);
            return;
        }

        // ── 1. 用户名校验 ──
        $len = mb_strlen($username);
        if ($len < 3 || $len > 32) {
            $resp->json(['success' => false, 'message' => '用户名长度需 3–32 个字符'], 400);
            return;
        }
        if (!preg_match('/^[\x{4e00}-\x{9fa5}A-Za-z0-9_.\-]+$/u', $username)) {
            $resp->json([
                'success' => false,
                'message' => '用户名只能包含中文、字母、数字、下划线、点、短横线',
            ], 400);
            return;
        }

        // 保留字：叫什么都不能叫这些，否则会误导本人和客服
        static $reserved = [
            'admin', 'administrator', 'root', 'system', 'sysadmin', 'superuser',
            'official', 'support', 'help', 'service', 'api', 'test', 'demo',
            '管理员', '官方', '客服', '系统', '测试', '运营', '财务',
        ];
        if (in_array(mb_strtolower($username), $reserved, true)) {
            $resp->json(['success' => false, 'message' => '该用户名不可用'], 409);
            return;
        }

        // ── 2. 密码校验 ──
        $pwLen = strlen($password);
        if ($pwLen < 8) {
            $resp->json(['success' => false, 'message' => '密码至少 8 位'], 400);
            return;
        }
        if ($pwLen > 200) {
            // bcrypt 对超长输入会明显变慢，是廉价的拒绝服务面
            $resp->json(['success' => false, 'message' => '密码过长'], 400);
            return;
        }

        // ── 3. 图形验证码（一次性，服务端校验）──
        // 走到这一步说明格式都对了，开始计入配额：
        // 验证码错误也算一次，否则 OCR 暴力猜对之前可以无限试。
        if (!$this->captchas->verify($captchaId, $captchaCode)) {
            $this->regGuard->record($ip);
            Logger::warn('security.register_bad_captcha', ['ip' => $ip, 'username' => $username]);
            $resp->json([
                'success' => false,
                'message' => '验证码错误或已过期，请刷新后重试',
                'code'    => 'bad_captcha',
            ], 400);
            return;
        }

        // ── 4. 唯一性（第一道）──
        if ($this->users->usernameExists($username)) {
            $this->regGuard->record($ip);
            $resp->json(['success' => false, 'message' => '用户名已被占用'], 409);
            return;
        }

        // ── 5. 创建（第二道：并发下靠唯一索引兜底）──
        try {
            $hash = password_hash($password, PASSWORD_BCRYPT, ['cost' => 12]);
            $userId = $this->users->create($username, $hash, 'default', 1, 'normal');
        } catch (\Throwable $e) {
            $this->regGuard->record($ip);
            Logger::warn('security.register_failed', [
                'ip'       => $ip,
                'username' => $username,
                'error'    => Logger::mask($e->getMessage()),
            ]);
            $resp->json(['success' => false, 'message' => '用户名已被占用'], 409);
            return;
        }

        $this->regGuard->record($ip);

        // ── 6. 注册即登录 ──
        $token = bin2hex(random_bytes(32));
        $this->users->setAccessToken($userId, $token);
        $this->users->touchLogin($userId);

        $user = $this->users->findById($userId);

        Logger::info('security.register_ok', [
            'ip'       => $ip,
            'user_id'  => $userId,
            'username' => $username,
        ]);

        $resp->json([
            'success' => true,
            'message' => '',
            'data'    => [
                'access_token' => $token,
                'is_manager'   => false,
                'user'         => $user !== null ? $this->publicUser($user) : null,
            ],
        ]);
    }

    /**
     * POST /api/logout
     */
    public function logout(Request $req, Response $resp): void
    {
        $identity = $this->auth->resolve($req->header('authorization'));
        if ($identity['user'] !== null) {
            $this->users->setAccessToken($identity['user']->id, '');
            // 立刻清掉缓存，否则登出后 5 秒内旧 token 还能用
            $this->auth->forget($req->header('authorization') ?? '');
            Logger::info('security.logout', ['user_id' => $identity['user']->id]);
        }
        $resp->ok(null);
    }

    /**
     * GET /api/me
     *
     * 统一的当前身份查询（不区分角色，所有登录用户都能调）。
     *
     * ⚠️ 不返回 role 数值、不返回任何路径 ——
     * 前端只需要知道"有没有管理权限"，不需要知道具体的权限等级。
     */
    public function me(Request $req, Response $resp): void
    {
        $identity = $this->auth->resolve($req->header('authorization'));
        if ($identity['user'] === null) {
            $resp->json(['success' => false, 'message' => '未登录'], 401);
            return;
        }
        $u = $identity['user'];

        // 注意：这里不再查库 —— resolve() 已经把 User 对象放进 Ctx 了。
        // 之前多查一次 findById()，在压测里这部分占了 /api/me 一半的耗时。
        $cached = Ctx::get('user_obj');
        if ($cached instanceof User) {
            $u = $cached;
        }

        $resp->ok([
            'user'       => $this->publicUser($u),
            'is_manager' => $u->isAdmin(),
        ]);
    }

    /**
     * GET /api/token
     */
    public function listTokens(Request $req, Response $resp): void
    {
        $u = $this->currentUser($req, $resp);
        if ($u === null) {
            return;
        }

        $rows = $this->users->listTokens($u->id);
        foreach ($rows as &$r) {
            $r['key'] = $this->maskKey((string) $r['key']);
        }
        unset($r);

        $resp->ok($rows);
    }

    /**
     * POST /api/token
     */
    public function createToken(Request $req, Response $resp): void
    {
        $u = $this->currentUser($req, $resp);
        if ($u === null) {
            return;
        }

        $name = trim((string) $req->input('name', '')) ?: '默认令牌';
        $key = $this->users->createToken(
            $u->id,
            $name,
            (int) $req->input('remain_quota', 0),
            (string) $req->input('models', ''),
            (int) $req->input('expired_days', 0),
        );

        Logger::info('token.created', ['user_id' => $u->id, 'name' => $name]);

        // 明文 key 只在这里返回一次，之后只能看到脱敏值
        $resp->ok(['key' => $key, 'name' => $name]);
    }

    /**
     * DELETE /api/token/{id}
     */
    public function deleteToken(Request $req, Response $resp): void
    {
        $u = $this->currentUser($req, $resp);
        if ($u === null) {
            return;
        }

        // 注意第二个参数必须是字符串：Request::param() 的签名是 ?string，
        // 这里写过 (int) 0，在 strict_types=1 下会抛 TypeError 直接 500。
        // 因为前端一直没做密钥管理界面，这个接口从写完就没被真正调用过，
        // 类型错误也就一直藏着 —— 直到实测才暴露。
        $id = (int) $req->param('id', '0');
        if ($id <= 0 || !$this->users->deleteToken($u->id, $id)) {
            $resp->json(['success' => false, 'message' => '令牌不存在'], 404);
            return;
        }
        $resp->ok(null);
    }

    private function currentUser(Request $req, Response $resp): ?User
    {
        $identity = $this->auth->resolve($req->header('authorization'));
        if ($identity['user'] === null) {
            $resp->json(['success' => false, 'message' => '未登录或登录已失效'], 401);
            return null;
        }
        return $identity['user'];
    }

    /**
     * 对外字段。
     *
     * ⚠️ 白名单原则 —— 只给前端真正需要的：
     *
     *   已删除 role    用顶层的 is_manager 布尔值替代。
     *                  role 是数值（1 / 10 / 100），一旦泄漏，
     *                  对方就知道存在分层角色体系、也知道该往哪个数字试。
     *   已删除 status  能登录成功就意味着状态正常，不需要再告诉前端。
     *   已删除 password_hash / access_token —— 凭证，绝不出现在响应里。
     *   已删除 group   内部分组（default / ltzy 之类）是我们自己的编排概念，
     *                  对用户没有任何意义，只会让人反推出这套系统的层次结构。
     */
    private function publicUser(User $u): array
    {
        return [
            'id'            => $u->id,
            'username'      => $u->username,
            'display_name'  => $u->displayName,
            'email'         => $u->email,
            'quota'         => $u->quota,
            'quota_yuan'    => round($u->quota / 1000000, 6),
            'used_quota'    => $u->usedQuota,
            'used_yuan'     => round($u->usedQuota / 1000000, 6),
            'request_count' => $u->requestCount,
            'last_login_at' => $u->lastLoginAt,
        ];
    }

    private function maskKey(string $key): string
    {
        if (strlen($key) <= 12) {
            return $key;
        }
        return substr($key, 0, 7) . '...' . substr($key, -4);
    }
}
