<?php
declare(strict_types=1);

namespace Ltzy\Http\Controller;

use Ltzy\Billing\Billing;
use Ltzy\Db\Connection;
use Ltzy\Domain\Channel\ChannelSourceInterface;
use Ltzy\Domain\Account\AccountRepository;
use Ltzy\Domain\User\Authenticator;
use Ltzy\Domain\User\User;
use Ltzy\Domain\User\UserRepository;
use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Relay\KeyPoolManager;
use Ltzy\Support\Ctx;
use Ltzy\Support\Logger;

/**
 * 超管接口。
 *
 * 「我们」用的后台：管渠道、定全局价、创建经销商、给经销商分配模型。
 * 所有端点都要求 role >= 10。鉴权在 handle() 入口统一做，
 * 避免每个方法都写一遍（漏一个就是越权漏洞）。
 */
final class AdminController
{
    private UserRepository $users;
    private AccountRepository $accounts;

    public function __construct(
        private readonly Connection $db,
        private readonly ChannelSourceInterface $channels,
        private readonly KeyPoolManager $pools,
        private readonly Billing $billing,
        private readonly Authenticator $auth,
        private readonly \Ltzy\Security\LoginGuard $loginGuard,
    ) {
        $this->users = new UserRepository($db);
        $this->accounts = new AccountRepository($db);
    }

    public function handle(Request $req, Response $resp, string $path, string $method): void
    {
        // ── 统一鉴权 ─────────────────────────────
        $identity = $this->auth->resolve($req->header('authorization'));
        $user = $identity['user'];
        if ($user === null || !$user->isAdmin()) {
            // 话术中性：不说"需要管理员权限"（那等于确认管理员存在且这是管理接口）
            $resp->json(['success' => false, 'message' => '无权限访问'], 403);
            return;
        }

        $sub = substr($path, strlen('/api/manage/'));
        // 去掉子路径里的 id：channels/3 → channels, 3
        $parts = explode('/', trim($sub, '/'));
        $resource = $parts[0] ?? '';
        $id = isset($parts[1]) && ctype_digit($parts[1]) ? (int) $parts[1] : 0;

        switch (true) {
            // ── 系统状态（内部用，含技术栈等敏感信息）──
            case $resource === 'status' && $method === 'GET':
                $this->systemStatus($resp);
                return;

            // ── 渠道 ─────────────────────────────
            case $resource === 'channels' && $method === 'GET' && $id === 0:
                $this->listChannels($resp);
                return;

            case $resource === 'channels' && $method === 'POST' && $id > 0:
                $this->toggleChannel($req, $resp, $id);
                return;

            case $resource === 'channels' && $method === 'GET' && $id > 0:
                $this->channelDetail($resp, $id);
                return;

            // ── 模型价格 ─────────────────────────
            case $resource === 'prices' && $method === 'GET' && $id === 0:
                $this->listPrices($resp);
                return;

            case $resource === 'prices' && $method === 'PUT' && $id === 0:
                $this->setPrice($req, $resp);
                return;

            // ── 经销商 ───────────────────────────
            case $resource === 'accounts' && $method === 'GET' && $id === 0:
                $this->listAccounts($req, $resp);
                return;

            case $resource === 'accounts' && $method === 'POST' && $id === 0:
                $this->createAccount($req, $resp);
                return;

            // /api/manage/accounts/{id}/models
            case $resource === 'accounts' && ($parts[2] ?? '') === 'models':
                if ($method === 'GET') {
                    $this->accountModels($resp, $id);
                } else {
                    $this->setaccountModels($req, $resp, $id);
                }
                return;

            // ── 用户 ─────────────────────────────
            case $resource === 'users' && $method === 'GET' && $id === 0:
                $this->listUsers($req, $resp);
                return;

            case $resource === 'users' && $method === 'POST' && $id > 0:
                $this->updateUser($req, $resp, $id);
                return;

            // ── 统计 ─────────────────────────────
            case $resource === 'stats' && $method === 'GET':
                $this->stats($req, $resp);
                return;

            // ── 日志 ─────────────────────────────
            case $resource === 'logs' && $method === 'GET':
                $this->logs($req, $resp);
                return;

            // ── 登录防护状态 ─────────────────────
            case $resource === 'security' && $method === 'GET':
                $resp->ok([
                    'loginguard' => $this->guard()->stats(),
                ]);
                return;

            case $resource === 'security' && $method === 'POST':
                $ip = (string) $req->input('ip', '');
                if ($ip === '') {
                    $resp->json(['success' => false, 'message' => '请提供要解锁的 IP'], 400);
                    return;
                }
                $this->guard()->reset($ip, $req->input('username') !== null ? (string) $req->input('username') : null);
                $resp->ok(['ip' => $ip, 'unlocked' => true]);
                return;

            default:
                $resp->json(['success' => false, 'message' => '未知的管理接口: ' . $path], 404);
        }
    }

    private function guard(): \Ltzy\Security\LoginGuard
    {
        return $this->loginGuard;
    }

    /**
     * GET /api/manage/status
     *
     * 完整的系统状态。所有"不该给外人看"的信息集中在这里 ——
     * 有鉴权保护，只有我们自己能看。
     */
    private function systemStatus(Response $resp): void
    {
        $channels = $this->channels->all();
        $enabled = array_filter($channels, fn ($c) => $c->isEnabled());

        $resp->ok([
            'version'          => '2.0.0-dev',
            'php_version'      => PHP_VERSION,
            'swoole_version'   => SWOOLE_VERSION,
            'os'               => PHP_OS_FAMILY,
            'channels_total'   => count($channels),
            'channels_enabled' => count($enabled),
            'users_total'      => (int) $this->db->scalar('SELECT COUNT(*) FROM users'),
            'accounts_total'    => (int) $this->db->scalar('SELECT COUNT(*) FROM users WHERE role = 1'),
            'tokens_total'     => (int) $this->db->scalar('SELECT COUNT(*) FROM tokens'),
            'loginguard'       => $this->loginGuard->stats(),
            'memory_mb'        => round(memory_get_usage(true) / 1048576, 1),
            'uptime_sec'       => (int) ($_SERVER['REQUEST_TIME_FLOAT'] ?? 0),
        ]);
    }

    /**
     * GET /api/manage/accounts
     *
     * 只列 role=1 的账户（经销商），不含超管。
     */
    private function listAccounts(Request $req, Response $resp): void
    {
        $limit = min(200, max(1, (int) $req->input('limit', 100)));
        $offset = max(0, (int) $req->input('offset', 0));

        $rows = $this->db->query('
            SELECT u.id, u.username, u.display_name, u.email, u.status, u.grp,
                   u.quota, u.used_quota, u.request_count, u.created_at, u.last_login_at,
                   (SELECT COUNT(*) FROM account_models dm WHERE dm.user_id = u.id AND dm.enabled = 1) AS model_count
            FROM users u
            WHERE u.role = 1
            ORDER BY u.id DESC
            LIMIT ? OFFSET ?
        ', [$limit, $offset]);

        $total = (int) $this->db->scalar('SELECT COUNT(*) FROM users WHERE role = 1');

        $items = array_map(static function (array $r): array {
            $quota = (int) $r['quota'];
            return [
                'id'            => (int) $r['id'],
                'username'      => (string) $r['username'],
                'display_name'  => (string) $r['display_name'],
                'email'         => (string) $r['email'],
                'status'        => (int) $r['status'],
                'group'         => (string) $r['grp'],
                'quota'         => $quota,
                'quota_yuan'    => round($quota / 1000000, 2),
                'used_quota'    => (int) $r['used_quota'],
                'used_yuan'     => round((int) $r['used_quota'] / 1000000, 2),
                'request_count' => (int) $r['request_count'],
                'model_count'   => (int) $r['model_count'],
                'created_at'    => (string) $r['created_at'],
                'last_login_at' => $r['last_login_at'],
            ];
        }, $rows);

        $resp->ok(['items' => $items, 'total' => $total, 'limit' => $limit, 'offset' => $offset]);
    }

    /**
     * POST /api/manage/accounts
     *
     * body: {"username": "xxx", "password": "yyy", "display_name": "", "quota": 0, "group": "default"}
     */
    private function createAccount(Request $req, Response $resp): void
    {
        $username = trim((string) $req->input('username', ''));
        $password = (string) $req->input('password', '');
        $displayName = trim((string) $req->input('display_name', '')) ?: $username;
        $quota = (int) $req->input('quota', 0);
        $group = (string) $req->input('group', 'default');
        $email = trim((string) $req->input('email', ''));

        if (strlen($username) < 3 || strlen($username) > 32) {
            $resp->json(['success' => false, 'message' => '用户名长度需在 3-32 之间'], 400);
            return;
        }
        if (strlen($password) < 6) {
            $resp->json(['success' => false, 'message' => '密码至少 6 位'], 400);
            return;
        }
        if ($this->users->findByUsername($username) !== null) {
            $resp->json(['success' => false, 'message' => '用户名已存在'], 409);
            return;
        }

        $hash = password_hash($password, PASSWORD_DEFAULT);

        $this->db->execute('
            INSERT INTO users (username, password_hash, display_name, email, role, status, grp, quota)
            VALUES (?, ?, ?, ?, 1, 1, ?, ?)
        ', [$username, $hash, $displayName, $email, $group, $quota]);

        $id = (int) $this->db->scalar('SELECT id FROM users WHERE username = ?', [$username]);

        Logger::info('admin.account_created', [
            'account_id' => $id,
            'username'  => $username,
            'by'        => Ctx::int('user_id'),
        ]);

        $resp->ok(['id' => $id, 'username' => $username, 'quota' => $quota]);
    }

    /**
     * GET /api/manage/accounts/{id}/models
     */
    private function accountModels(Response $resp, int $id): void
    {
        $user = $this->db->first('SELECT id, username FROM users WHERE id = ? AND role = 1', [$id]);
        if ($user === null) {
            $resp->json(['success' => false, 'message' => '经销商不存在'], 404);
            return;
        }

        $rows = $this->accounts->models($id);
        $items = array_map(static fn(array $r) => [
            'model'      => $r['model'],
            'vendor'     => PublicController::vendorOf($r['model']),
            'price'      => $r['price'],
            'price_yuan' => round($r['price'] / 1000000, 6),
        ], $rows);

        $resp->ok([
            'user_id'  => $id,
            'username' => (string) $user['username'],
            'items'    => $items,
            'total'    => count($items),
        ]);
    }

    /**
     * PUT /api/manage/accounts/{id}/models
     *
     * body: {"items": [{"model": "xxx", "price": 3000}, ...]}
     *
     * 全量覆盖 —— 前端勾选完整体提交，不传就是清空。
     */
    private function setaccountModels(Request $req, Response $resp, int $id): void
    {
        $user = $this->db->first('SELECT id, username FROM users WHERE id = ? AND role = 1', [$id]);
        if ($user === null) {
            $resp->json(['success' => false, 'message' => '经销商不存在'], 404);
            return;
        }

        $items = $req->input('items', []);
        if (!is_array($items)) {
            $resp->json(['success' => false, 'message' => 'items 必须是数组'], 400);
            return;
        }

        // 校验模型名是否真实存在，避免手抖写出无效模型
        $allModels = [];
        foreach ($this->channels->all() as $ch) {
            foreach ($ch->models as $m) {
                $allModels[$m] = true;
            }
        }

        $clean = [];
        $invalid = [];
        foreach ($items as $it) {
            $m = (string) ($it['model'] ?? '');
            if ($m === '') {
                continue;
            }
            if (!isset($allModels[$m])) {
                $invalid[] = $m;
                continue;
            }
            $clean[] = ['model' => $m, 'price' => max(0, (int) ($it['price'] ?? 0))];
        }

        if ($invalid !== []) {
            $resp->json([
                'success' => false,
                'message' => '这些模型不存在：' . implode(', ', array_slice($invalid, 0, 5)),
            ], 400);
            return;
        }

        $n = $this->accounts->setModels($id, $clean);

        Logger::info('admin.account_models_set', [
            'account_id' => $id,
            'count'     => $n,
            'by'        => Ctx::int('user_id'),
        ]);

        $resp->ok(['user_id' => $id, 'saved' => $n]);
    }

    /**
     * GET /api/manage/channels
     */
    private function listChannels(Response $resp): void
    {
        $rows = $this->db->query('
            SELECT id, name, type, status, base_url, grp, priority, weight,
                   length("key") AS key_len, models
            FROM channels ORDER BY id
        ');

        $out = [];
        foreach ($rows as $r) {
            $ch = $this->channels->findById((int) $r['id']);
            $stats = $ch !== null ? $this->pools->for($ch)->stats() : null;
            $out[] = [
                'id'          => (int) $r['id'],
                'name'        => $r['name'],
                'type'        => $r['type'],
                'status'      => (int) $r['status'],
                'base_url'    => $r['base_url'],
                'group'       => $r['grp'],
                'priority'    => (int) $r['priority'],
                'weight'      => (int) $r['weight'],
                'models'      => $r['models'] === '' ? [] : explode(',', (string) $r['models']),
                // 密钥总数与可用数（冷却中的算不可用）
                'keys_total'     => $stats['total'] ?? 0,
                'keys_available' => $stats['available'] ?? 0,
                'keys_cooling'   => $stats['cooling'] ?? 0,
            ];
        }

        $resp->ok(['items' => $out, 'total' => count($out)]);
    }

    /**
     * GET /api/manage/channels/{id}
     */
    private function channelDetail(Response $resp, int $id): void
    {
        $row = $this->db->first('
            SELECT id, name, type, status, base_url, grp, priority, weight, models, model_mapping, config
            FROM channels WHERE id = ?
        ', [$id]);

        if ($row === null) {
            $resp->json(['success' => false, 'message' => '渠道不存在'], 404);
            return;
        }

        $ch = $this->channels->findById($id);
        $stats = $ch !== null ? $this->pools->for($ch)->stats() : null;

        // 注意：不返回 key 字段的明文。管理界面只需要知道有几把、可用几把。
        $resp->ok([
            'id'             => (int) $row['id'],
            'name'           => $row['name'],
            'type'           => $row['type'],
            'status'         => (int) $row['status'],
            'base_url'       => $row['base_url'],
            'group'          => $row['grp'],
            'priority'       => (int) $row['priority'],
            'weight'         => (int) $row['weight'],
            'models'         => $row['models'] === '' ? [] : explode(',', (string) $row['models']),
            'model_mapping'  => json_decode((string) $row['model_mapping'], true) ?: new \stdClass(),
            'config'         => json_decode((string) $row['config'], true) ?: new \stdClass(),
            'pool'           => $stats,
        ]);
    }

    /**
     * POST /api/manage/channels/{id}
     *
     * 目前只支持改 status（启用/禁用）。改配置需要重启进程才能生效
     * 的部分（密钥池）会在这里清掉对应缓存。
     */
    private function toggleChannel(Request $req, Response $resp, int $id): void
    {
        $status = (int) $req->input('status', 0);
        if (!in_array($status, [1, 2], true)) {
            $resp->json(['success' => false, 'message' => 'status 只能是 1（启用）或 2（禁用）'], 400);
            return;
        }

        $n = $this->db->execute('UPDATE channels SET status = ?, updated_at = now() WHERE id = ?', [$status, $id]);
        if ($n === 0) {
            $resp->json(['success' => false, 'message' => '渠道不存在'], 404);
            return;
        }

        // 渠道缓存要失效，否则新状态要等 TTL 到期才生效
        if ($this->channels instanceof \Ltzy\Domain\Channel\DbChannelSource) {
            $this->channels->invalidate();
        }
        // 禁用渠道时顺手清掉它的密钥池（下次启用会重建）
        if ($status === 2) {
            $this->pools->forget($id);
        }

        Logger::info('admin.channel_toggled', ['channel_id' => $id, 'status' => $status, 'by' => Ctx::int('user_id')]);

        $resp->ok(['id' => $id, 'status' => $status]);
    }

    /**
     * GET /api/manage/prices
     */
    private function listPrices(Response $resp): void
    {
        $rows = $this->db->query('
            SELECT model, per_request_price, input_price, output_price, updated_at
            FROM model_prices ORDER BY per_request_price DESC, model
        ');
        $resp->ok(['items' => $rows, 'total' => count($rows)]);
    }

    /**
     * PUT /api/manage/prices
     *
     * body: {"model": "xxx", "per_request_price": 3000}
     */
    private function setPrice(Request $req, Response $resp): void
    {
        $model = trim((string) $req->input('model', ''));
        $price = (int) $req->input('per_request_price', -1);

        if ($model === '') {
            $resp->json(['success' => false, 'message' => 'model 不能为空'], 400);
            return;
        }
        if ($price < 0) {
            $resp->json(['success' => false, 'message' => 'per_request_price 不能为负'], 400);
            return;
        }

        $this->db->execute('
            INSERT INTO model_prices (model, per_request_price) VALUES (?, ?)
            ON CONFLICT (model) DO UPDATE SET per_request_price = EXCLUDED.per_request_price, updated_at = now()
        ', [$model, $price]);

        Logger::info('admin.price_set', ['model' => $model, 'price' => $price, 'by' => Ctx::int('user_id')]);

        $resp->ok(['model' => $model, 'per_request_price' => $price]);
    }

    /**
     * GET /api/manage/users
     */
    private function listUsers(Request $req, Response $resp): void
    {
        $limit = min(200, max(1, (int) $req->input('limit', 50)));
        $offset = max(0, (int) $req->input('offset', 0));

        $rows = $this->db->query('
            SELECT id, username, display_name, role, status, grp, quota, used_quota,
                   request_count, created_at, last_login_at
            FROM users ORDER BY id DESC LIMIT ? OFFSET ?
        ', [$limit, $offset]);

        $total = (int) $this->db->scalar('SELECT COUNT(*) FROM users');

        $resp->ok(['items' => $rows, 'total' => $total, 'limit' => $limit, 'offset' => $offset]);
    }

    /**
     * POST /api/manage/users/{id}
     *
     * body: {"status": 1|2, "quota": 1000000, "group": "default", "role": 10}
     * 只更新传了的字段。
     */
    private function updateUser(Request $req, Response $resp, int $id): void
    {
        $row = $this->db->first('SELECT id, role FROM users WHERE id = ?', [$id]);
        if ($row === null) {
            $resp->json(['success' => false, 'message' => '用户不存在'], 404);
            return;
        }

        $sets = [];
        $params = [];

        if ($req->input('status') !== null) {
            $status = (int) $req->input('status');
            if (!in_array($status, [1, 2, 3], true)) {
                $resp->json(['success' => false, 'message' => 'status 只能是 1/2/3'], 400);
                return;
            }
            $sets[] = 'status = ?';
            $params[] = $status;
        }

        if ($req->input('quota') !== null) {
            $sets[] = 'quota = ?';
            $params[] = (int) $req->input('quota');
        }

        if ($req->input('group') !== null) {
            $sets[] = 'grp = ?';
            $params[] = (string) $req->input('group');
        }

        if ($req->input('role') !== null) {
            $role = (int) $req->input('role');
            // 只有超管能改角色，且不能把自己降级（避免把自己锁在外面）
            $me = Ctx::int('user_id');
            if ($me === $id) {
                $resp->json(['success' => false, 'message' => '不能修改自己的角色'], 400);
                return;
            }

            // ── 安全评估 M-4：原代码注释写"只有超管能改角色"，但这里
            // 压根没有校验调用者身份 —— AdminController::handle 只检查了
            // isAdmin()（role >= 10）。结果是一个 role=10 的普通管理员
            // 可以把任意账号提到 role=100（超管），再用那个账号登录，
            // 完整接管系统。同时 $role 没有白名单，可以设成 999 / -1。
            //
            // 三层收紧：
            //   ① 调用者必须本身是超管（role >= 100）
            //   ② 目标值必须在合法枚举内
            //   ③ 不能把别人提到与自己同级或更高（防平级互相提权）
            $meObj = Ctx::get('user_obj');
            $myRole = $meObj?->role ?? 0;
            if ($myRole < 100) {
                Logger::warn('security.role_escalation_blocked', [
                    'actor_id'    => $me,
                    'actor_role'  => $myRole,
                    'target_id'   => $id,
                    'wanted_role' => $role,
                ]);
                $resp->json(['success' => false, 'message' => '只有超级管理员能修改角色'], 403);
                return;
            }

            $allowedRoles = [1, 10, 100];
            if (!in_array($role, $allowedRoles, true)) {
                $resp->json(['success' => false, 'message' => 'role 只能是 1/10/100'], 400);
                return;
            }
            if ($role >= $myRole) {
                $resp->json(['success' => false, 'message' => '不能把用户提升到与自己同级或更高的角色'], 400);
                return;
            }

            $sets[] = 'role = ?';
            $params[] = $role;
        }

        if ($sets === []) {
            $resp->json(['success' => false, 'message' => '没有要更新的字段'], 400);
            return;
        }

        $params[] = $id;
        $this->db->execute('UPDATE users SET ' . implode(', ', $sets) . ' WHERE id = ?', $params);

        Logger::info('admin.user_updated', ['user_id' => $id, 'fields' => $sets, 'by' => Ctx::int('user_id')]);

        $resp->ok(['id' => $id, 'updated_fields' => count($sets)]);
    }

    /**
     * GET /api/manage/stats?days=7
     */
    private function stats(Request $req, Response $resp): void
    {
        $days = min(90, max(1, (int) $req->input('days', 7)));

        $summary = $this->db->first('
            SELECT COUNT(*) AS requests,
                   COALESCE(SUM(quota), 0) AS quota,
                   COALESCE(AVG(elapsed_ms), 0) AS avg_ms,
                   COALESCE(AVG(first_byte_ms) FILTER (WHERE first_byte_ms > 0), 0) AS avg_ttft
            FROM logs
            WHERE created_at >= now() - (? || \' days\')::interval
        ', [$days]) ?? [];

        // 按天聚合（PG 的 date_trunc 比在 PHP 里聚合快得多，数据量大了优势明显）
        $daily = $this->db->query('
            SELECT to_char(date_trunc(\'day\', created_at), \'YYYY-MM-DD\') AS day,
                   COUNT(*) AS requests,
                   COALESCE(SUM(quota), 0) AS quota
            FROM logs
            WHERE created_at >= now() - (? || \' days\')::interval
            GROUP BY 1 ORDER BY 1
        ', [$days]);

        $byModel = $this->db->query('
            SELECT model, COUNT(*) AS requests, COALESCE(SUM(quota), 0) AS quota
            FROM logs
            WHERE created_at >= now() - (? || \' days\')::interval
            GROUP BY model ORDER BY requests DESC LIMIT 20
        ', [$days]);

        $resp->ok([
            'days'     => $days,
            'summary'  => [
                'requests' => (int) ($summary['requests'] ?? 0),
                'quota'    => (int) ($summary['quota'] ?? 0),
                'avg_ms'   => (int) ($summary['avg_ms'] ?? 0),
                'avg_ttft' => (int) ($summary['avg_ttft'] ?? 0),
            ],
            'daily'    => $daily,
            'by_model' => $byModel,
        ]);
    }

    /**
     * GET /api/manage/logs?limit=50&user_id=&model=
     */
    private function logs(Request $req, Response $resp): void
    {
        $limit = min(200, max(1, (int) $req->input('limit', 50)));

        $where = ['1=1'];
        $params = [];

        $userId = (int) $req->input('user_id', 0);
        if ($userId > 0) {
            $where[] = 'user_id = ?';
            $params[] = $userId;
        }

        $model = (string) $req->input('model', '');
        if ($model !== '') {
            $where[] = 'model = ?';
            $params[] = $model;
        }

        $params[] = $limit;

        $rows = $this->db->query(
            'SELECT id, created_at, user_id, token_id, channel_id, model, quota,
                    elapsed_ms, first_byte_ms, is_stream, status_code, request_id
             FROM logs WHERE ' . implode(' AND ', $where) . '
             ORDER BY created_at DESC LIMIT ?',
            $params
        );

        $resp->ok(['items' => $rows, 'total' => count($rows)]);
    }
}
