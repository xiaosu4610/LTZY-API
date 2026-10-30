<?php
declare(strict_types=1);

namespace Ltzy\Billing;

use Ltzy\Db\Connection;
use Ltzy\Domain\Account\AccountRepository;
use Ltzy\Support\Ctx;

/**
 * 计费。
 *
 * ── 单位约定 ──
 *
 * 1 元 = 1,000,000 quota。与生产 Go 版一致，便于对账。
 *
 * ── 两套价格体系 ──
 *
 * 1. **未登录**：一律拒绝（2026-10-29 起取消匿名白嫖，详见 charge() 内注释）。
 * 2. **已登录用户**：查 account_models 里的专属价格（一人一价），
 *    没有专属价就回落到全局 model_prices。
 *
 * 为什么不做成「全局价格 + 折扣倍率」：
 *   经销商要的是「我拿到的模型清单和价格和别人不一样」，
 *   而不是「同一份价格打不同折扣」。直接按 (经销商, 模型) 存价格最清楚，
 *   也不用担心倍率连乘算错。
 *
 * ── 为什么按次计价而不是按 token ──
 *
 * 上游用量字段不可靠：有的渠道不返回 usage，流式响应里 usage 经常缺失。
 * 按次计价确定、可预测，用户能提前算清成本。
 */
final class Billing
{
    /** @var array<string,int> 模型 → 全局按次价格（匿名/免费判定用） */
    private array $priceCache = [];

    private float $cacheAt = 0.0;

    public function __construct(
        private readonly Connection $db,
        private readonly AccountRepository $accounts,
        private readonly array $cfg = [],
        private readonly int $cacheTtl = 60,
    ) {
    }

    /**
     * 模型是否免费（对匿名用户而言）。
     *
     * 判定依据是全局 model_prices：0 表示免费。
     * 查不到也视为免费 —— 新模型上线默认不收费，避免误扣用户余额。
     */
    public function isFree(string $model): bool
    {
        return $this->globalPrice($model) === 0;
    }

    /**
     * 模型对所有匿名用户的价格。
     */
    public function globalPrice(string $model): int
    {
        $this->refreshIfStale();
        return $this->priceCache[$model] ?? 0;
    }

    /**
     * 全局「对外可售」的收费模型清单（model => 单价）。
     *
     * 只收 per_request_price > 0 的。0 和查不到都算免费，走免费池，
     * 不在这里出现 —— 判定口径与 isFree() 严格互斥，
     * 否则 /v1/models 里同一个模型会既出现在免费区又出现在收费区。
     *
     * @return array<string,int>
     */
    public function paidCatalog(): array
    {
        $this->refreshIfStale();

        $out = [];
        foreach ($this->priceCache as $model => $price) {
            if ($price > 0) {
                $out[$model] = $price;
            }
        }
        ksort($out);

        return $out;
    }

    /**
     * 某个用户实际能用的收费模型清单（model => 单价）。
     *
     * 语义与 charge() 完全对齐，三步：
     *   1. 先铺全局清单（model_prices）—— 这是「新注册用户默认能看到的」，
     *      不需要管理员手工分配 account_models，充值即可用；
     *   2. 再用 account_models 的专属价覆盖 —— 经销商拿到的批量价优先；
     *   3. 价 0 的剔除（0 = 免费，不该出现在收费区）。
     *
     * 返回的是「可见 + 可用」，不判断余额够不够：列表接口不该按余额过滤，
     * OpenAI 也不这么做 —— 余额不足应该在真正调用时报错，而不是让模型凭空消失。
     *
     * @return array<string,int>
     */
    public function paidCatalogFor(int $userId): array
    {
        $out = $this->paidCatalog();

        if ($userId <= 0) {
            return $out;
        }

        // 专属价覆盖。注意不能反过来「先查专属价再看全局」——
        // 那会让任何有一个专属价的用户丢掉其余全部全局模型。
        foreach ($this->accounts->models($userId) as $row) {
            $price = (int) $row['price'];
            if ($price > 0) {
                $out[(string) $row['model']] = $price;
            }
        }

        ksort($out);

        return $out;
    }

    /**
     * 计算一次请求的费用。
     *
     * @param string   $model      用户请求的模型名
     * @param string   $group      用户分组（匿名时是 default）
     * @param int      $userId     0 = 匿名
     * @param bool     $isAdmin    管理员免计费
     *
     * @return array{quota:int,allowed:bool,reason:string}
     *         allowed=false 表示这个模型对当前用户不可用
     */
    public function charge(string $model, string $group, int $userId = 0, bool $isAdmin = false): array
    {
        // 管理员：全放开、不收费
        if ($isAdmin) {
            return ['quota' => 0, 'allowed' => true, 'reason' => ''];
        }

        // ⚠️ 下面所有 reason 都是**给外部访客看的**，必须中性：
        //    不能说"需要经销商账号"（暴露账号体系），
        //    不能说"未开通"（暴露存在付费层），
        //    只能说"不存在或暂不可用" —— 让对方无法区分
        //    "这个模型没有" 和 "你没有权限"。

        // 匿名用户：一律拒绝。
        //
        // 产品策略变更（2026-10-29）：取消「免登录白嫖免费池」。
        // 原因有三，都有数据支撑：
        //   1. 24 小时里匿名调用 19,237 次，而 14 个登录用户只有 266 次 ——
        //      免费池被 178 个爬虫 IP 吃光，真实用户反而拿不到资源；
        //   2. 上游密钥池 98% 的超时，根子就是这些匿名流量把密钥打到冷却；
        //   3. 充值功能上线后，必须能定位到「谁充了钱、谁没充」，
        //      匿名请求没有 user_id，计费链路根本没法闭环。
        //
        // 提示语必须可操作：告诉用户去哪儿、怎么解决，而不是干巴巴一句"无权限"。
        if ($userId <= 0) {
            $url = (string) ($this->cfg['site_url'] ?? '');
            // 没配站点地址时不要拼出 "/login" 这种残缺链接 ——
            // 那会让用户以为登录页在根路径下，点进去 404
            $reason = $url !== ''
                ? "请先注册并登录后使用，登录地址：{$url}/login"
                : '请先注册并登录后使用';

            return [
                'quota'   => 0,
                'allowed' => false,
                'reason'  => $reason,
                'code'    => 'login_required',
            ];
        }

        // 已登录用户：先查专属价（一人一价），没有再回落全局定价
        $price = $this->accounts->priceFor($userId, $model);

        if ($price === null) {
            // ── 回落全局定价（2026-10-29 新增）──
            //
            // 原来这里只有两条路：有专属价，或者免费模型。
            // 除此之外一律「不可用」—— 后果是：
            //
            //   265 个老用户（special）有专属价，3445 行；
            //   所有新注册用户（normal）专属价行数 = 0。
            //
            // 也就是说充值功能上线后，新用户充了钱，
            // 那 13 个付费模型（glm-5.3 / kimi-k3 / gpt-image-2.5 …）
            // 依然一个都调不了，只能干等管理员手工分配 account_models。
            // 充值等于白充。
            //
            // 现在改为：没有专属价就按 model_prices.per_request_price 走，
            // 有钱就能用，管理员不需要逐个开通。
            // 专属价仍然优先 —— 经销商拿到的批量价不会被这条覆盖。
            $global = $this->globalPrice($model);

            if ($global === 0) {
                // 免费模型：公共资源，登录用户免费用
                return ['quota' => 0, 'allowed' => true, 'reason' => ''];
            }

            $ratio = $this->ratioFor($group, $model);

            return [
                'quota'   => (int) round($global * $ratio),
                'allowed' => true,
                'reason'  => '',
            ];
        }

        // 专属价格 + 分组倍率（保留倍率机制，便于临时调价）
        $ratio = $this->ratioFor($group, $model);

        return [
            'quota'   => (int) round($price * $ratio),
            'allowed' => true,
            'reason'  => '',
        ];
    }

    /**
     * 分组倍率。
     *
     * 查找顺序：精确匹配 (grp, model) → 通配 (grp, '*') → 1.0
     */
    public function ratioFor(string $group, string $model): float
    {
        $row = $this->db->first(
            'SELECT ratio FROM group_ratios WHERE grp = ? AND model = ? LIMIT 1',
            [$group, $model]
        );
        if ($row !== null) {
            return (float) $row['ratio'];
        }

        $row = $this->db->first(
            'SELECT ratio FROM group_ratios WHERE grp = ? AND model = \'*\' LIMIT 1',
            [$group]
        );
        return $row !== null ? (float) $row['ratio'] : 1.0;
    }

    /**
     * 写消费日志。
     *
     * 注意：user_id = 0 的记录是匿名调用。这些也记（用于统计免费池的压力），
     * 但经销商的日志接口只查自己的 user_id，看不到别人的。
     */
    public function writeLog(array $data): void
    {
        try {
            $this->db->execute('
                INSERT INTO logs (user_id, token_id, channel_id, model, prompt_tokens, completion_tokens,
                                  quota, elapsed_ms, first_byte_ms, is_stream, status_code, request_id, detail)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ', [
                (int) ($data['user_id'] ?? 0),
                (int) ($data['token_id'] ?? 0),
                (int) ($data['channel_id'] ?? 0),
                (string) ($data['model'] ?? ''),
                (int) ($data['prompt_tokens'] ?? 0),
                (int) ($data['completion_tokens'] ?? 0),
                (int) ($data['quota'] ?? 0),
                (int) ($data['elapsed_ms'] ?? 0),
                (int) ($data['first_byte_ms'] ?? 0),
                // ⚠️ 必须传 'true'/'false' 字符串，不能传 PHP bool。
                // PDO 在 ATTR_EMULATE_PREPARES=false 时把 bool 绑成空串，
                // PG 会报 invalid input syntax for type boolean: ""。
                ($data['is_stream'] ?? false) ? 'true' : 'false',
                (int) ($data['status_code'] ?? 200),
                (string) ($data['request_id'] ?? ''),
                (string) ($data['detail'] ?? ''),
            ]);
        } catch (\Throwable $e) {
            \Ltzy\Support\Logger::warn('billing.log_failed', [
                'request_id' => Ctx::requestId(),
                'error'      => \Ltzy\Support\Logger::mask($e->getMessage()),
            ]);
        }
    }

    private function refreshIfStale(): void
    {
        if ($this->priceCache !== [] && (microtime(true) - $this->cacheAt) < $this->cacheTtl) {
            return;
        }

        $rows = $this->db->query('SELECT model, per_request_price FROM model_prices');
        $cache = [];
        foreach ($rows as $r) {
            $cache[(string) $r['model']] = (int) $r['per_request_price'];
        }
        $this->priceCache = $cache;
        $this->cacheAt = microtime(true);
    }
}
