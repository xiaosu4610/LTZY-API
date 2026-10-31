<?php
declare(strict_types=1);

namespace Ltzy\Http\Controller;

use Ltzy\Billing\Billing;
use Ltzy\Domain\Account\AccountRepository;
use Ltzy\Domain\Channel\ChannelSourceInterface;
use Ltzy\Domain\User\Authenticator;
use Ltzy\Domain\User\User;
use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Support\Ctx;

/**
 * 经销商后台。
 *
 * 经销商由我们手工创建（不开放注册），登录后能看：
 *   - 自己的账户信息与余额
 *   - 分配给自己的模型清单与专属价格
 *   - 自己的调用日志（默认保留 180 天）
 *
 * ⚠️ 严格的隔离：所有查询都绑死当前登录的 user_id，
 * 不接受请求参数里的 user_id —— 否则就是水平越权。
 *
 * 2026-10-29 变更：「我的模型」不再是「只给我分配过的那几个」，
 * 而是「我这个账号此刻真正能调的全部模型」——
 * 免费池 + 计费模型（全局定价铺底，专属价覆盖）。
 * 原因是新注册用户没有 account_models，看到的清单是空的，
 * 充了钱也不知道能调什么，充值等于白充。
 */
final class AccountController
{
    public function __construct(
        private readonly AccountRepository $accounts,
        private readonly Authenticator $auth,
        private readonly Billing $billing,
        private readonly ChannelSourceInterface $channels,
        private readonly int $logRetentionDays = 180,
    ) {
    }

    /**
     * GET /api/account/self
     */
    public function self(Request $req, Response $resp): void
    {
        $user = $this->currentUser($req, $resp);
        if ($user === null) {
            return;
        }

        $days = $this->logRetentionDays;
        $stats = $this->accounts->stats($user->id, $days);

        $resp->ok([
            'user'          => $this->publicUser($user),
            'stats'         => $stats,
            'retention_days' => $days,
            'model_count'   => count($this->usableModels($user->id)),
        ]);
    }

    /**
     * GET /api/account/models
     *
     * 我这个账号此刻能调的全部模型 + 实际计费单价。
     *
     * 口径与 Billing::charge() 和 /v1/models 三处必须一致，
     * 否则会出现「列表里有、调了报无权限」或者反过来的最差体验。
     */
    public function models(Request $req, Response $resp): void
    {
        $user = $this->currentUser($req, $resp);
        if ($user === null) {
            return;
        }

        $items = $this->usableModels($user->id);

        $isAgent = $user->isAgent();

        $free = 0;
        $paid = 0;
        $savedTotal = 0.0;

        foreach ($items as $i => $it) {
            if ($it['tier'] === 'paid') {
                $paid++;

                // 代理额外返回零售价与差价：
                //   retail_price  平台对外零售价（全局 model_prices）
                //   saved         每单比零售价省多少（= 代理的毛利空间）
                //   discount      拿货价 ÷ 零售价，方便代理算利润率
                if ($isAgent) {
                    $retail = $this->billing->globalPrice($it['model']);
                    $items[$i]['retail_price']      = $retail;
                    $items[$i]['retail_price_yuan'] = round($retail / 1000000, 8);
                    $items[$i]['saved']             = max(0, $retail - $it['price']);
                    $items[$i]['saved_yuan']        = round(max(0, $retail - $it['price']) / 1000000, 8);
                    $items[$i]['discount']          = $retail > 0
                        ? round($it['price'] / $retail, 4)
                        : 1.0;
                    $savedTotal += max(0, $retail - $it['price']) / 1000000;
                }
            } else {
                $free++;
            }
        }

        $resp->ok([
            'items'       => $items,
            'total'       => count($items),
            'free_total'  => $free,
            'paid_total'  => $paid,
            'is_agent'    => $isAgent,
            // 代理界面用：全部计费模型每单合计能省多少
            'saved_total' => round($savedTotal, 6),
            'base_url'    => $req->origin() . '/v1',
        ]);
    }

    /**
     * 该用户可用的全部模型：免费池 + 计费模型。
     *
     * 免费池按渠道遍历（与 /v1/models 同一口径，用 isFree 判定），
     * 计费模型走 Billing::paidCatalogFor（全局价铺底 + 专属价覆盖）。
     *
     * 两条互斥：有专属价/全局价的模型绝不会同时出现在免费区。
     *
     * price 单位是 quota（1 元 = 1,000,000），price=0 即免费。
     *
     * @return array<int,array{model:string,vendor:string,price:int,price_yuan:float,tier:string}>
     */
    private function usableModels(int $userId): array
    {
        $paid = $this->billing->paidCatalogFor($userId);

        $rows = [];
        $seen = [];

        foreach ($this->channels->all() as $ch) {
            if (!$ch->isEnabled()) {
                continue;
            }
            foreach ($ch->models as $m) {
                if (isset($seen[$m]) || isset($paid[$m]) || !$this->billing->isFree($m)) {
                    continue;
                }
                $seen[$m] = true;
                $rows[$m] = [
                    'model'      => $m,
                    'vendor'     => PublicController::vendorOf($m),
                    'price'      => 0,
                    'price_yuan' => 0.0,
                    'tier'       => 'free',
                ];
            }
        }

        foreach ($paid as $m => $price) {
            $name = (string) $m;
            $rows[$name] = [
                'model'      => $name,
                'vendor'     => PublicController::vendorOf($name),
                'price'      => $price,
                'price_yuan' => round($price / 1000000, 8),
                'tier'       => 'paid',
            ];
        }

        ksort($rows);

        return array_values($rows);
    }

    /**
     * GET /api/account/logs?limit=&offset=
     */
    public function logs(Request $req, Response $resp): void
    {
        $user = $this->currentUser($req, $resp);
        if ($user === null) {
            return;
        }

        $limit = min(200, max(1, (int) $req->input('limit', 50)));
        $offset = max(0, (int) $req->input('offset', 0));
        $days = $this->logRetentionDays;

        // ⚠️ user_id 来自登录态，不是请求参数 —— 天然防越权
        $rows = $this->accounts->logs($user->id, $limit, $offset, $days);

        $items = array_map(static function (array $r): array {
            $quota = (int) $r['quota'];
            $elapsed = (int) $r['elapsed_ms'];
            $prompt = (int) $r['prompt_tokens'];
            $completion = (int) $r['completion_tokens'];

            // 输出速度（token/秒）。用户判断"快不快"靠这个，不是靠毫秒数 ——
            // 3000ms 生成 20 个 token 和 3000ms 生成 800 个 token 是两回事。
            // 分母用「总耗时 - 首字节」，因为首字之前的等待不算生成速度。
            $genMs = $elapsed - (int) $r['first_byte_ms'];
            $rate = ($genMs > 0 && $completion > 0)
                ? round($completion / ($genMs / 1000), 1)
                : 0.0;

            return [
                'id'                => (int) $r['id'],
                'time'              => (string) $r['created_at'],
                'model'             => (string) $r['model'],
                'quota'             => $quota,
                'quota_yuan'        => round($quota / 1000000, 8),
                'elapsed_ms'        => $elapsed,
                'first_byte_ms'     => (int) $r['first_byte_ms'],
                'is_stream'         => (bool) $r['is_stream'],
                'status_code'       => (int) $r['status_code'],
                'prompt_tokens'     => $prompt,
                'completion_tokens' => $completion,
                'total_tokens'      => $prompt + $completion,
                'tokens_per_sec'    => $rate,
                'request_id'        => (string) $r['request_id'],
                // 上游错误摘要（成功时为空）。截断到 160 字符：
                // 完整的上游报文可能有几 KB，塞进列表会把表格撑爆，
                // 真要看全文可以按 request_id 去查服务端日志。
                'detail'            => mb_substr((string) ($r['detail'] ?? ''), 0, 160),
            ];
        }, $rows);

        $resp->ok([
            'items'          => $items,
            'total'          => count($items),
            'limit'          => $limit,
            'offset'         => $offset,
            'retention_days' => $days,
        ]);
    }

    /**
     * GET /api/account/usage
     *
     * 用量图表数据（按天 + 按模型）。
     */
    public function usage(Request $req, Response $resp): void
    {
        $user = $this->currentUser($req, $resp);
        if ($user === null) {
            return;
        }

        $days = $this->logRetentionDays;

        $resp->ok([
            'summary'  => $this->accounts->stats($user->id, $days),
            'daily'    => $this->accounts->dailyUsage($user->id, $days),
            'by_model' => $this->accounts->usageByModel($user->id, $days),
            'days'     => $days,
        ]);
    }

    /**
     * 从请求解析当前经销商；未登录直接 401。
     */
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
     * 对外暴露的用户字段。
     *
     * ⚠️ 白名单原则，别加字段前先想清楚「前端真的需要吗」：
     *
     *   不含 password_hash / access_token —— 凭证。
     *   不含 role                       —— 数值角色一旦泄露，
     *      对方就知道存在分层角色体系、也知道该往哪个数字试。
     *      自己是不是管理员，用 /api/me 顶层的 is_manager 布尔值表达。
     *   不含 status                     —— 能登录进来就说明状态正常。
     *   不含 group                      —— 内部分组是部署方
     *      自己的编排概念，对经销商没有任何意义，只能说漏嘴。
     */
    private function publicUser(User $u): array
    {
        return [
            'id'            => $u->id,
            'username'      => $u->username,
            'display_name'  => $u->displayName,
            'email'         => $u->email,
            'quota'         => $u->quota,
            'quota_yuan'    => round($u->quota / 1000000, 8),
            'used_quota'    => $u->usedQuota,
            'used_yuan'     => round($u->usedQuota / 1000000, 8),
            'request_count' => $u->requestCount,
            'last_login_at' => $u->lastLoginAt,
        ];
    }
}
