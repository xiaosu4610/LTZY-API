<?php
declare(strict_types=1);

/**
 * 应用配置。
 *
 * 这里的值可以被数据库 options 表覆盖（运行期可改），
 * 但数据库连不上时必须能靠这些默认值启动 —— 否则一个配置错误
 * 会导致服务完全起不来，连排障的接口都没有。
 */

return [
    // HTTP 监听
    //
    // 默认 0.0.0.0 而不是 127.0.0.1：本地开发时经常需要从手机、平板、
    // 或者另一台机器上测试页面。只监听回环地址会让这些访问全部失败，
    // 而且报错是"连接被拒绝"，很容易被误判成服务没起来。
    //
    // 生产环境用 nginx 反代，后端只监听 127.0.0.1：
    //   APP_HOST=127.0.0.1 php bin/server.php
    'host' => getenv('APP_HOST') ?: '0.0.0.0',
    'port' => (int) (getenv('APP_PORT') ?: 3000),

    // 上游中继超时（秒）。硬上限，到点即中断，即使上游还在排队。
    //
    // 2026-10-30 定为 600 秒。依据（首次上线阶段的实际要求）：
    //   · 首次请求常触发上游冷启动 / 模型加载，几十秒无响应属正常现象，
    //     超时设太短会让"第一次用"直接失败，用户第一印象就是不能用。
    //   · 统一 600 秒封顶 —— 到点必返回错误，不会无限挂着。
    //   · 重试共享这一个预算（见 RelayService 的 $deadline），
    //     所以 3 次重试也不会变成 1800 秒。
    'relay_timeout' => (int) (getenv('RELAY_TIMEOUT') ?: 600),

    // 失败重试次数（含首次）。
    //
    // 重试次数与上面的 600 秒是「共享预算」关系，不是相乘：
    // 第一次跑了 550 秒，第二次就只剩 50 秒。
    'retry_times' => (int) (getenv('RETRY_TIMES') ?: 3),

    // 连接池大小。PG 连接开销 53ms vs 查询 0.27ms，
    // 池太小会排队，太大会超过 PG 的 max_connections(100)。
    'db_pool_size' => (int) (getenv('DB_POOL_SIZE') ?: 16),

    // 日志级别：debug / info / warn / error
    'log_level' => getenv('LOG_LEVEL') ?: 'info',

    // 是否输出到 stdout（开发用）；生产建议写文件
    'log_stdout' => filter_var(getenv('LOG_STDOUT') ?: 'true', FILTER_VALIDATE_BOOL),

    // 对外站点地址（生成登录链接、支付回调地址用）。
    // 开源版本不写死任何域名：装完由安装向导写进 .env，或部署时用环境变量设。
    'site_url' => rtrim((string) (getenv('SITE_URL') ?: ''), '/'),

    // 免登录通道：允许无密钥调用免费模型。
    //
    // 已关闭。数据依据（实测）：
    //   匿名调用量远超登录用户（爬虫吃光免费池）；
    //   上游密钥池大面积超时，根子是这批匿名流量把密钥打到冷却；
    //   充值上线后必须能定位「谁充的钱」，匿名请求没有 user_id，计费闭环不了。
    'free_anonymous_enabled' => filter_var(
        getenv('FREE_ANONYMOUS_ENABLED') ?: 'false',
        FILTER_VALIDATE_BOOL
    ),

    // 密钥冷却时长（秒）
    'cooldown_auth_failed' => 30 * 60,   // 401/403：密钥失效，短期内不会恢复
    'cooldown_rate_limited' => 60,        // 429：限流窗口通常按分钟计

    // 经销商日志保留天数。
    // 180 天是产品约定：够看半年趋势，又不会让分区表无限膨胀。
    'log_retention_days' => (int) (getenv('LOG_RETENTION_DAYS') ?: 180),

    // ── 网关级破甲包装（红队测试用）────────────────────
    //
    // 在请求转发前自动把用户问题包进破甲模板，用户侧无感知。
    //
    // 硬边界：只对计费渠道的模型开，免费池
    // 的模型永远不进这个名单——免费渠道的账号安全是命根子。
    //
    // 上游按次计费，模板注入不增加成本（边际成本为零）。
    //
    // 测试期先只开一个模型，验证效果后再用 JB_MODELS 环境变量扩名单。
    'jailbreak' => [
        'enabled' => filter_var(getenv('JB_ENABLED') ?: 'true', FILTER_VALIDATE_BOOL),
        'models' => array_values(array_filter(explode(
            ',',
            getenv('JB_MODELS') ?: 'glm-5.3'
        ))),
    ],

    // ── 充值 / 支付（易支付 epay 协议）────────────────────
    //
    // 走标准易支付：下单到 mapi.php 拿 payurl，用户扫码付，
    // 网关异步回调 notify_url，验签通过后入账。
    //
    // 密钥只从环境变量读，绝不写进代码库 —— 配置文件会进 git。
    'payment' => [
        'enabled' => filter_var(getenv('PAYMENT_ENABLED') ?: 'true', FILTER_VALIDATE_BOOL),
        'gateway' => rtrim(getenv('PAYMENT_GATEWAY') ?: 'https://xnoo.cn', '/'),
        'pid'     => getenv('PAYMENT_PID') ?: '',
        'key'     => getenv('PAYMENT_KEY') ?: '',

        // 回调地址留空则用 site_url 自动拼，生产必须能被外网访问到
        'notify_url' => getenv('PAYMENT_NOTIFY_URL') ?: '',
        'return_url' => getenv('PAYMENT_RETURN_URL') ?: '',

        // 充值档位（元）。前端展示这些快捷按钮，自定义框支持到分。
        'amounts' => [1, 10, 30, 50, 100, 300, 500],

        // 最低 0.01 元（1 分）。允许小额是为了让人先试一单 ——
        // 支付链路没跑通过的人不愿意一上来就充 10 块。
        // 最高 5000 元，防手滑也防刷。
        'min_amount' => 0.01,
        'max_amount' => 5000,

        // 1 元 = 1,000,000 quota，与全站计费单位一致
        'rate' => 1_000_000,

        // 允许的支付方式
        'pay_types' => ['alipay', 'wxpay'],

        // 订单自动关单时长（分钟）：超过未支付就标为已关闭
        'expire_minutes' => 30,
    ],
];
