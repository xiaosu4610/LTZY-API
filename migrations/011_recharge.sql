-- 011_recharge.sql —— 充值订单与支付回调
--
-- 背景（2026-10-29 产品策略变更）：
--   取消「免登录白嫖免费池」，改为必须注册登录才能调用。
--   同时开通充值——免费池不够用的人可以付钱走付费线路。
--
-- 设计取舍：
--
-- 1. **金额存「分」而不是「元」（INTEGER amount_cents）**
--    支付网关给的是字符串 "1.00"，浮点比较会出现 1.00 != 1.0 这种事。
--    全程用整数分，只在展示时除 100。
--
-- 2. **回调幂等靠 status 字段 + 唯一索引**
--    易支付的重试机制是「没收到 success 就一直重发」，
--    同一个 trade_no 会撞进来好几次。用 UPDATE ... WHERE status = 0
--    让第二次回调影响 0 行，天然幂等。
--
-- 3. **保留 notify_raw 原文**
--    对账时最怕「网关说付了、我们说没付」。把回调原文整段存下来，
--    出了纠纷可以直接看原始报文，不用猜。

BEGIN;

CREATE TABLE IF NOT EXISTS recharge_orders (
    id           BIGSERIAL    PRIMARY KEY,

    -- 我方订单号：R + 年月日时分秒 + 6 位随机，22 字符，全局唯一
    order_no     VARCHAR(64)  NOT NULL UNIQUE,

    user_id      INTEGER      NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    amount_cents INTEGER      NOT NULL,                 -- 支付金额（分）
    quota        BIGINT       NOT NULL,                 -- 到账额度（1 元 = 1e6）

    pay_type     VARCHAR(16)  NOT NULL DEFAULT '',      -- alipay / wxpay
    status       SMALLINT     NOT NULL DEFAULT 0,       -- 0 待支付 / 1 已支付 / 2 已关闭

    trade_no     VARCHAR(64)  NOT NULL DEFAULT '',      -- 上游订单号
    notify_raw   TEXT         NOT NULL DEFAULT '',      -- 回调原文，对账用
    client_ip    VARCHAR(45)  NOT NULL DEFAULT '',      -- 下单 IP，防刷排查

    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    paid_at      TIMESTAMPTZ,
    expire_at    TIMESTAMPTZ  NOT NULL,

    CONSTRAINT recharge_status_check CHECK (status IN (0, 1, 2)),
    CONSTRAINT recharge_amount_check CHECK (amount_cents > 0),
    CONSTRAINT recharge_quota_check  CHECK (quota > 0)
);

-- 用户查自己的充值记录
CREATE INDEX IF NOT EXISTS idx_recharge_user
    ON recharge_orders (user_id, created_at DESC);

-- 定时关单扫这个索引
CREATE INDEX IF NOT EXISTS idx_recharge_expire
    ON recharge_orders (status, expire_at);

-- 上游订单号唯一（空串不参与，因为待支付时还没有 trade_no）
CREATE UNIQUE INDEX IF NOT EXISTS uq_recharge_trade
    ON recharge_orders (trade_no) WHERE trade_no <> '';

COMMENT ON TABLE  recharge_orders IS '充值订单。status: 0待支付 1已支付 2已关闭';
COMMENT ON COLUMN recharge_orders.notify_raw IS '支付网关回调原文，对账与纠纷取证用';

-- ── 开关 ──
-- options 有 PRIMARY KEY (key)，可以直接 upsert

INSERT INTO options (key, value) VALUES ('topup_enabled', 'true')
    ON CONFLICT (key) DO UPDATE SET value = 'true';

-- 关掉匿名：2026-10-29 起必须登录
INSERT INTO options (key, value) VALUES ('free_anonymous_enabled', 'false')
    ON CONFLICT (key) DO UPDATE SET value = 'false';

COMMIT;
