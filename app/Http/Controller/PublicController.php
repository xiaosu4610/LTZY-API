<?php
declare(strict_types=1);

namespace Ltzy\Http\Controller;

use Ltzy\Billing\Billing;
use Ltzy\Db\Connection;
use Ltzy\Domain\Channel\ChannelSourceInterface;
use Ltzy\Http\Request;
use Ltzy\Http\Response;
use Ltzy\Relay\KeyPoolManager;
use Ltzy\Support\Ctx;
use Ltzy\Support\SiteConfig;

/**
 * 公开接口：状态、模型列表、模型广场。
 *
 * /api/status、/api/site、/api/model/plaza 是完全公开的营销面，
 * 匿名可访问，只展示免费池 —— 那是给人的第一眼，不该要密钥。
 *
 * /v1/models 是例外：按 OpenAI 规范要求 Bearer 密钥。
 * 登录/带 key 才返回「免费池 + 该用户可用的计费模型」，
 * 无密钥一律 401 invalid_api_key，与官方行为一致。详见 models()。
 */
final class PublicController
{
    /**
     * 免费模型清单缓存（**按数组缓存，不再缓存整串响应**）。
     *
     * /v1/models 已改为「登录后按用户返回」：
     * 免费部分对所有人一样，收费部分一人一份，所以整串 JSON 已经没法共用了。
     * 现在只缓存免费那一段，收费部分按用户实时拼。
     *
     * 压测数据：500 并发时全量遍历渠道 p50 是 233ms，缓存后接近 0。
     *
     * @var array<int,array{id:string,object:string,created:int,owned_by:string}>|null
     */
    private static ?array $freeCache = null;
    private static float $freeCacheAt = 0.0;

    /** 模型对象的 created 时间戳。固定值，保证多次请求的列表逐字节可比。 */
    private const CREATED_AT = 1793000000;

    /** @var array<string,string>|null 模型 → 类别缓存（来自 abilities） */
    private static ?array $categoryCache = null;
    private static float $categoryCacheAt = 0.0;

    public function __construct(
        private readonly ChannelSourceInterface $channels,
        private readonly KeyPoolManager $pools,
        private readonly Billing $billing,
        private readonly Connection $db,
        private readonly SiteConfig $site,
    ) {
    }

    /**
     * GET /api/status
     *
     * ⚠️ 这是**完全公开**的接口，任何人裸访问都能拿到。
     * 所以只返回客户端和前端真正需要的东西，其余一律不给：
     *
     *   已删除：php_version / swoole_version  → 暴露技术栈，等于告诉攻击者查哪个 CVE
     *   已删除：channels_total / channels_enabled → 暴露上游规模，商业信息
     *   已删除：version → 暴露迭代进度
     *   已删除：register_enabled / topup_enabled / 账号体系开关
     *          → 暴露业务形态（"原来这个站有经销商体系"）
     *
     * 内部监控需要的详细信息在 /api/admin/status（需鉴权）。
     */
    public function status(Request $req, Response $resp): void
    {
        $resp->ok([
            'server_address' => $req->origin(),
            'free_models'    => $this->freeModelCount(),
            'status'         => 'ok',
        ]);
    }

    /**
     * GET /v1/models
     *
     * 按 OpenAI 规范实现：**必须带可用的 Bearer 密钥**，没有就直接 401。
     *
     * 返回该用户此刻真正能调的全部模型：
     *   - 免费池（所有人一样，命中 60 秒缓存）
     *   - 计费模型（全局 model_prices 铺底 + account_models 专属价覆盖）
     *
     * 响应体严格按 OpenAI 的 List models 规范：
     *   {"object":"list","data":[{"id","object","created","owned_by"}]}
     *
     * 额外多给 per_request_price / price_yuan / tier 三个字段 ——
     * 官方规范里没有价格，但我们的收费模型是按次计价的商业产品，
     * 用户必须能在客户端下拉框里看到成本才知道该不该选。
     * 多出来的字段对标准客户端无害（JSON 按需取用）。
     */
    public function models(Request $req, Response $resp): void
    {
        $userId = Ctx::int('user_id');

        // ── 鉴权门 ──
        // Authenticator 已经把 sk- 令牌和网页会话都解析过了，
        // 走到这里 user_id 还是 0，说明密钥缺失 / 无效 / 用户被禁用 / 额度耗尽。
        if ($userId <= 0) {
            $this->rejectUnauthorized($resp, Ctx::str('auth_error'));
            return;
        }

        $paid = $this->billing->paidCatalogFor($userId);

        // 免费池：所有人共享同一份清单，命中缓存
        $items = [];
        foreach ($this->freeModels() as $m) {
            // 兜底：某个模型既被判成免费又有专属价时，以收费为准，
            // 绝不能两个区各出现一次 —— 客户端会渲染成重复项
            if (isset($paid[$m['id']])) {
                continue;
            }
            $m['tier'] = 'free';
            $items[$m['id']] = $m;
        }

        // 计费模型：按用户拼，价格来自专属价或全局价
        foreach ($paid as $model => $price) {
            $items[$model] = [
                'id'                => $model,
                'object'            => 'model',
                'created'           => self::CREATED_AT,
                'owned_by'          => self::vendorOf($model),
                'category'          => $this->categoryOfModel($model),
                'per_request_price' => $price,
                'price_yuan'        => round($price / 1000000, 6),
                'tier'              => 'paid',
            ];
        }

        ksort($items);

        $resp->jsonRaw(\Ltzy\Support\Json::encode([
            'object' => 'list',
            'data'   => array_values($items),
        ]));
    }

    /**
     * 401 —— 按 OpenAI 的错误信封返回。
     *
     * 官方客户端（openai-python / openai-node / LangChain / Cherry Studio …）
     * 都认这个结构，拿到 invalid_api_key 会直接抛 AuthenticationError，
     * 用户一眼能看出是密钥问题，而不是以为我们服务挂了。
     */
    private function rejectUnauthorized(Response $resp, string $authError): void
    {
        $message = match ($authError) {
            'user_disabled'         => 'This API key is associated with a disabled account.',
            'token_quota_exhausted' => 'This API key has exhausted its quota.',
            'invalid_token',
            'invalid_session'       => 'Incorrect API key provided. You can find your API key in the console.',
            default                 => "You didn't provide an API key. You need to provide your API key "
                                     . 'in an Authorization header using Bearer auth '
                                     . '(i.e. Authorization: Bearer YOUR_KEY).',
        };

        $resp->openaiError($message, 401, 'invalid_request_error', 'invalid_api_key');
    }

    /**
     * 免费模型清单（带 60 秒进程内缓存）。
     *
     * @return array<int,array{id:string,object:string,created:int,owned_by:string}>
     */
    private function freeModels(): array
    {
        $now = microtime(true);
        if (self::$freeCache === null || ($now - self::$freeCacheAt) >= 60) {
            // chatOnly = false：与 OpenAI 的 List models 口径一致。
            //
            // 官方 /v1/models 是把 chat、embedding、whisper、tts、dall-e
            // 全放在同一个列表里的，客户端按 category 自己挑端点。
            // 之前这里传 true（只要 chat），结果是 /v1/models 报 36 个、
            // 控制台「我的模型」报 79 个，同一件事两个数字，
            // 用户截图来问「到底有几个」的时候没法解释。
            self::$freeCache = $this->collectFreeModels(false);
            self::$freeCacheAt = $now;
        }
        return self::$freeCache;
    }

    /**
     * GET /api/model/plaza
     *
     * 模型广场数据。只含免费模型。
     *
     * ⚠️ 字段白名单：只给"用户需要知道的"。
     * 曾经返回过 channel_name（内部渠道名）和
     * keys_total（500），那等于把上游供应商和密钥规模白送给任何人 ——
     * 这是商业机密，不是产品信息。已删除。
     */
    public function plaza(Request $req, Response $resp): void
    {
        $items = [];
        $seen = [];

        foreach ($this->channels->all() as $ch) {
            if (!$ch->isEnabled()) {
                continue;
            }
            foreach ($ch->models as $m) {
                if (isset($seen[$m])) {
                    continue;
                }
                // 只展示免费模型 —— 但**不再按能否对话过滤**。
                //
                // 之前这里还要求 isChatModel()，结果 embedding / rerank / 语音
                // 这些白白进了库却不在广场露面，用户根本不知道我们有这些能力。
                // 现在全部展示，用 category 字段在页面上分区，用户能一眼看到
                // 「这个模型该用哪个端点」，比藏起来更有用。
                if (!$this->billing->isFree($m)) {
                    continue;
                }
                $seen[$m] = true;

                // 注意：不要在这里加渠道相关字段（渠道 ID / 密钥池 / 路由信息），
                // 那属于内部概念，泄漏了等于把后端拓扑告诉攻击者。
                $items[] = [
                    'model'    => $m,
                    'vendor'   => self::vendorOf($m),
                    'category' => $ch->categoryOf($m),
                ];
            }
        }

        usort($items, static fn(array $a, array $b) => strcmp($a['model'], $b['model']));

        // 厂商维度聚合，门户首页要展示"覆盖多少个厂商"
        $vendors = [];
        foreach ($items as $it) {
            $vendors[$it['vendor']] = ($vendors[$it['vendor']] ?? 0) + 1;
        }
        arsort($vendors);

        // 类别维度聚合：前端按这个分区渲染
        $cats = [];
        foreach ($items as $it) {
            $c = $it['category'] !== '' ? $it['category'] : 'other';
            $cats[$c] = ($cats[$c] ?? 0) + 1;
        }

        $resp->ok([
            'items'    => $items,
            'total'    => count($items),
            'vendors'  => array_map(
                static fn(string $name, int $n) => ['name' => $name, 'count' => $n],
                array_keys($vendors),
                array_values($vendors)
            ),
            'categories' => array_map(
                static fn(string $name, int $n) => ['name' => $name, 'count' => $n],
                array_keys($cats),
                array_values($cats)
            ),
        ]);
    }

    /**
     * GET /api/site
     *
     * 门户首页需要的站点信息。
     *
     * 注意边界：只给"访客需要知道的"。
     * 厂商分布可以给（这是产品卖点），但不上技术栈、不上下游数量、
     * 不提任何账号体系的存在。
     */
    public function site(Request $req, Response $resp): void
    {
        $models = $this->collectFreeModels(false);   // 全部免费模型（含向量化/重排）
        $vendors = [];
        foreach ($models as $m) {
            $v = $m['owned_by'];
            $vendors[$v] = ($vendors[$v] ?? 0) + 1;
        }
        arsort($vendors);

        $host = $req->header('host') ?? '127.0.0.1:3000';

        // 品牌信息来自后台配置（可在「站点设置」里改），
        // 分发给不同用户部署时不用改代码。
        $brandCfg = $this->site->brand();
        // Logo 有两层：管理员上传的（var/）优先，内置默认（web/）兜底
        $customLogo = dirname(__DIR__, 3) . '/var/favicon.ico';
        $defaultLogo = dirname(__DIR__, 3) . '/web/favicon.ico';
        $logoFile = is_file($customLogo) ? $customLogo : (is_file($defaultLogo) ? $defaultLogo : '');
        $hasLogo = $logoFile !== '';

        $resp->ok([
            'brand' => [
                'name'     => $brandCfg['name'],
                'slogan'   => $brandCfg['desc'],
                // 这段文案同时出现在首页 hero、meta description 和这里，
                // 三处必须一致，否则用户看到的是自相矛盾的说明。
                'intro'    => '汇聚多个主流大模型，注册即全量解锁。免费模型不限次、不扣费；'
                    . '计费模型按次扣费，充值即用，接口与 OpenAI 完全一致。',
                'footer'   => $brandCfg['footer'],
            ],
            // 前端据此决定显示自定义图片还是默认字母方块
            'has_logo'     => $hasLogo,
            'logo_url'     => '/favicon.ico?v=' . ($hasLogo ? filemtime($logoFile) : 0),
            'base_url'     => $req->origin() . '/v1',
            'free_models'  => count($models),
            'vendor_count' => count($vendors),
            'vendors'      => array_slice(
                array_map(
                    static fn(string $name, int $n) => ['name' => $name, 'count' => $n],
                    array_keys($vendors),
                    array_values($vendors)
                ),
                0,
                12
            ),
            'endpoints' => [
                [
                    'method'  => 'POST',
                    'path'    => '/v1/responses',
                    'desc'    => 'Responses API（推荐，OpenAI 新一代标准）',
                    'example' => 'curl ' . $req->origin() . '/v1/responses \\' . "\n"
                        . '  -H "Content-Type: application/json" \\' . "\n"
                        . '  -H "Authorization: Bearer YOUR_KEY" \\' . "\n"
                        . '  -d \'{"model":"LLM_MODEL","input":"你好"}\'',
                ],
                [
                    'method'  => 'POST',
                    'path'    => '/v1/chat/completions',
                    'desc'    => 'Chat Completions（兼容旧客户端）',
                    'example' => 'curl ' . $req->origin() . '/v1/chat/completions \\' . "\n"
                        . '  -H "Content-Type: application/json" \\' . "\n"
                        . '  -H "Authorization: Bearer YOUR_KEY" \\' . "\n"
                        . '  -d \'{"model":"LLM_MODEL","messages":[{"role":"user","content":"你好"}]}\'',
                ],
                [
                    'method'  => 'GET',
                    'path'    => '/v1/models',
                    'desc'    => '获取可用模型列表（需密钥）',
                    'example' => 'curl ' . $req->origin() . '/v1/models \\' . "\n"
                        . '  -H "Authorization: Bearer YOUR_KEY"',
                ],
            ],
            'notes' => [
                '注册登录后自动获得免费池 + 计费模型的完整模型清单',
                'Authorization: Bearer 你的密钥，与 OpenAI 完全一致',
                '模型 ID 从「模型广场」或 /v1/models 复制',
                '支持 OpenAI 兼容的全部客户端',
            ],
        ]);
    }

    /**
     * 模型 → 类别映射（来自 abilities 表，带 60 秒缓存）。
     *
     * 为什么不能只靠 Channel::categoryOf()：
     *   那个读的是渠道自己 models 字段里的类别，而部分计费渠道
     *   的 channels.models 是空的 —— 模型映射只存在于 abilities 表。
     *   只靠渠道会把这 13 个收费模型全标成 chat，
     *   gpt-image-2.5 会被当成对话模型推给用户，选了必然报错。
     *
     * @return array<string,string>
     */
    private function allCategories(): array
    {
        $now = microtime(true);
        if (self::$categoryCache !== null && ($now - self::$categoryCacheAt) < 60) {
            return self::$categoryCache;
        }

        $map = [];
        $rows = $this->db->query(
            "SELECT DISTINCT ON (model) model, COALESCE(category, 'chat') AS category
               FROM abilities
              ORDER BY model,
                       CASE COALESCE(category, 'chat')
                            WHEN 'chat'      THEN 0
                            WHEN 'vision'    THEN 1
                            WHEN 'embedding' THEN 2
                            WHEN 'rerank'    THEN 3
                            WHEN 'audio'     THEN 4
                            WHEN 'image'     THEN 5
                            ELSE 9
                       END"
        );
        foreach ($rows as $r) {
            $map[(string) $r['model']] = (string) $r['category'];
        }

        self::$categoryCache = $map;
        self::$categoryCacheAt = $now;

        return $map;
    }

    /**
     * 单个模型的类别。查不到按 chat 兜底（新模型默认是对话模型）。
     */
    private function categoryOfModel(string $model): string
    {
        return $this->allCategories()[$model] ?? 'chat';
    }

    /**
     * 收集免费模型（稳定排序 + 去重）。
     *
     * ⚠️ 用 abilities.category 过滤，不靠模型名猜。
     *
     * 之前用关键词黑名单（embed/rerank/asr...），结果部分上游的视觉模型
     * （bevformer / body-pose / sparsedrive）一个都没匹配上，全漏到 chat 广场里，
     * 用户点了必然 404。现在以数据库里的 category 为准。
     *
     * ── $chatOnly 是干什么的 ──
     *
     * 这个函数被两处用，需求完全不同：
     *   /v1/models   要「能直接对话的」——客户端拿它填模型下拉框，
     *                混进 embedding 模型，用户选了必然 400。
     *   广场 / 首页   要「全部免费的」——用户得知道我们有向量化、重排序能力。
     *
     * 以前只有一个行为，导致广场漏掉了 43 个模型。现在用参数区分。
     *
     * @return array<int,array{id:string,object:string,created:int,owned_by:string}>
     */
    private function collectFreeModels(bool $chatOnly = true): array
    {
        $models = [];
        $seen = [];

        foreach ($this->channels->all() as $ch) {
            if (!$ch->isEnabled()) {
                continue;
            }
            foreach ($ch->models as $m) {
                if (isset($seen[$m]) || !$this->billing->isFree($m)) {
                    continue;
                }
                // $chatOnly 时才做对话能力过滤
                if ($chatOnly && !$ch->isChatModel($m)) {
                    continue;
                }
                $seen[$m] = true;
                $models[] = [
                    'id'       => $m,
                    'object'   => 'model',
                    'created'  => self::CREATED_AT,
                    'owned_by' => self::vendorOf($m),
                    // category 让客户端知道这个模型该走哪个端点：
                    // embedding 走 /v1/embeddings，audio 走 /v1/audio/*，
                    // 混进 /v1/chat/completions 必然 400。
                    'category' => $ch->categoryOf($m),
                ];
            }
        }

        // 稳定排序：前端做缓存时，顺序抖动会造成无谓的 diff
        usort($models, static fn(array $a, array $b) => strcmp($a['id'], $b['id']));

        return $models;
    }

    private function freeModelCount(): int
    {
        return count($this->collectFreeModels(false));   // 与广场口径一致
    }

    /**
     * 从模型名推断厂商。
     */
    public static function vendorOf(string $model): string
    {
        $prefix = strtolower(explode('/', $model)[0]);
        return match (true) {
            str_starts_with($prefix, 'nvidia')    => 'NVIDIA',
            str_starts_with($prefix, 'deepseek')  => '深度求索',
            str_starts_with($prefix, 'qwen')      => '阿里巴巴',
            str_starts_with($prefix, 'glm')       => '智谱',
            str_starts_with($prefix, 'z-ai')      => '智谱',
            str_starts_with($prefix, 'meta')      => 'Meta',
            str_starts_with($prefix, 'google')    => 'Google',
            str_starts_with($prefix, 'minimax')   => 'MiniMax',
            str_starts_with($prefix, 'stepfun')   => '阶跃星辰',
            str_starts_with($prefix, 'moonshot')  => '月之暗面',
            str_starts_with($prefix, 'mistral')   => 'Mistral',
            str_starts_with($prefix, 'microsoft') => 'Microsoft',
            str_starts_with($prefix, 'internlm')  => '上海AI Lab',
            str_starts_with($prefix, 'openai')    => 'OpenAI',
            str_starts_with($prefix, 'baai')      => '智源',
            str_starts_with($prefix, 'aisingapore') => 'AI Singapore',
            str_starts_with($prefix, 'upstage')   => 'Upstage',
            str_starts_with($prefix, 'ibm')       => 'IBM',
            str_starts_with($prefix, 'writer')    => 'Writer',
            str_starts_with($prefix, 'abacusai')  => 'Abacus.AI',

            // ── 向量化 / 重排序 / 语音 / 垂类模型 ──
            // 这些模型的 ID 没有「厂商/模型名」结构，只能按特征前缀认。
            // 不认的话广场上会堆一大片「其他」，用户分不清谁是谁。
            str_starts_with($prefix, 'bge-')        => '智源 BAAI',
            str_starts_with($prefix, 'bce-')        => '网易有道',
            str_starts_with($prefix, 'all-mpnet')   => 'Microsoft',
            str_starts_with($prefix, 'jina')        => 'Jina AI',
            str_starts_with($prefix, 'sensevoice')  => '阿里巴巴',
            str_starts_with($prefix, 'spark-tts')   => '科大讯飞',
            str_starts_with($prefix, 'healthgpt')   => '医疗垂类',
            str_starts_with($prefix, 'huatuogpt')   => '医疗垂类',
            str_starts_with($prefix, 'lingshu')     => '医疗垂类',
            str_starts_with($prefix, 'ip-location') => '工具类',
            str_starts_with($prefix, 'poolside')    => 'Poolside',

            default => '其他',
        };
    }
}
