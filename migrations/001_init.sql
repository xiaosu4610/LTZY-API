-- 001_init.sql · v2 初始表结构
--
-- 从现网 30 张表精简到 8 张：只保留这个项目真正用到的。
-- 去掉的：approval / subaccount / tenant / redemption / pay / midjourney /
--        plan_wallet / alert / audit / ip_location / permission …
--
-- 设计原则见 docs/07-数据库设计.md。

-- ── 渠道 ──────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS channels (
    id              SERIAL PRIMARY KEY,
    name            VARCHAR(128) NOT NULL,
    type            VARCHAR(32)  NOT NULL DEFAULT 'openai_compat',
    status          SMALLINT     NOT NULL DEFAULT 1,      -- 1=启用 2=禁用
    base_url        TEXT         NOT NULL,
    "key"           TEXT         NOT NULL DEFAULT '',     -- 多把密钥，换行分隔
    models          TEXT         NOT NULL DEFAULT '',     -- 逗号分隔（冗余，供展示）
    model_mapping   TEXT         NOT NULL DEFAULT '{}',   -- JSON：用户名 → 上游名
    grp             VARCHAR(32)  NOT NULL DEFAULT 'default',
    priority        INT          NOT NULL DEFAULT 0,
    weight          INT          NOT NULL DEFAULT 1,
    config          JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_channels_status ON channels (status);
CREATE INDEX IF NOT EXISTS idx_channels_type   ON channels (type);

-- ── 模型到渠道的映射 ────────────────────────────────────
CREATE TABLE IF NOT EXISTS abilities (
    id          SERIAL PRIMARY KEY,
    model       VARCHAR(128) NOT NULL,
    channel_id  INT          NOT NULL,
    enabled     SMALLINT     NOT NULL DEFAULT 1,
    grp         VARCHAR(32)  NOT NULL DEFAULT 'default',
    priority    INT          NOT NULL DEFAULT 0,
    weight      INT          NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_abilities_model ON abilities (model, enabled);
CREATE UNIQUE INDEX IF NOT EXISTS uq_abilities ON abilities (model, channel_id);

-- ── 模型价格 ──────────────────────────────────────────
-- per_request_price 非 0 表示按次计价；为 0 则看 input/output 两个按 token 价。
CREATE TABLE IF NOT EXISTS model_prices (
    id                  SERIAL PRIMARY KEY,
    model               VARCHAR(128) NOT NULL UNIQUE,
    input_price         BIGINT       NOT NULL DEFAULT 0,
    output_price        BIGINT       NOT NULL DEFAULT 0,
    cache_price         BIGINT       NOT NULL DEFAULT 0,
    per_request_price   BIGINT       NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- ── 分组倍率（分组 × 模型 → 倍率）───────────────────────
-- model = '*' 表示该分组的通配倍率。
CREATE TABLE IF NOT EXISTS group_ratios (
    id          SERIAL PRIMARY KEY,
    grp         VARCHAR(32)   NOT NULL,
    model       VARCHAR(128)  NOT NULL DEFAULT '*',
    ratio       NUMERIC(10,4) NOT NULL DEFAULT 1.0,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_group_ratios ON group_ratios (grp, model);

-- ── 账户 ──────────────────────────────────────────────
-- quota 单位是 quota（1 元 = 1,000,000）。
-- status: 1=正常 2=禁用 3=已清退（软删除，保留数据可对账）
CREATE TABLE IF NOT EXISTS users (
    id              SERIAL PRIMARY KEY,
    username        VARCHAR(64)  NOT NULL UNIQUE,
    password_hash   VARCHAR(255) NOT NULL,
    display_name    VARCHAR(64)  NOT NULL DEFAULT '',
    email           VARCHAR(128) NOT NULL DEFAULT '',
    role            SMALLINT     NOT NULL DEFAULT 1,     -- 1=普通 10=管理 100=超管
    status          SMALLINT     NOT NULL DEFAULT 1,
    grp             VARCHAR(32)  NOT NULL DEFAULT 'default',
    quota           BIGINT       NOT NULL DEFAULT 0,
    used_quota      BIGINT       NOT NULL DEFAULT 0,
    request_count   BIGINT       NOT NULL DEFAULT 0,
    access_token    VARCHAR(64)  NOT NULL DEFAULT '',
    remark          TEXT         NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_login_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_users_status ON users (status);
CREATE INDEX IF NOT EXISTS idx_users_grp    ON users (grp);
CREATE UNIQUE INDEX IF NOT EXISTS uq_users_token ON users (access_token) WHERE access_token <> '';

-- ── 令牌 ──────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS tokens (
    id              SERIAL PRIMARY KEY,
    user_id         INT          NOT NULL,
    name            VARCHAR(128) NOT NULL,
    "key"           VARCHAR(64)  NOT NULL UNIQUE,        -- sk-xxx
    status          SMALLINT     NOT NULL DEFAULT 1,
    remain_quota    BIGINT       NOT NULL DEFAULT 0,     -- 0 = 不限制
    used_quota      BIGINT       NOT NULL DEFAULT 0,
    models          TEXT         NOT NULL DEFAULT '',    -- 白名单，空=不限
    rpm             INT          NOT NULL DEFAULT 0,
    tpm             INT          NOT NULL DEFAULT 0,
    concurrency     INT          NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expired_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_tokens_user ON tokens (user_id);

-- ── 消费日志（分区表）────────────────────────────────────
-- 分区键是 created_at，所以主键必须包含它（PG 分区表的硬性要求）。
CREATE TABLE IF NOT EXISTS logs (
    id                 BIGSERIAL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id            INT          NOT NULL DEFAULT 0,   -- 0 = 免登录
    token_id           INT          NOT NULL DEFAULT 0,
    channel_id         INT          NOT NULL DEFAULT 0,
    model              VARCHAR(128) NOT NULL DEFAULT '',
    prompt_tokens      INT          NOT NULL DEFAULT 0,
    completion_tokens  INT          NOT NULL DEFAULT 0,
    quota              BIGINT       NOT NULL DEFAULT 0,
    elapsed_ms         INT          NOT NULL DEFAULT 0,
    first_byte_ms      INT          NOT NULL DEFAULT 0,
    is_stream          BOOLEAN      NOT NULL DEFAULT false,
    status_code        INT          NOT NULL DEFAULT 200,
    request_id         VARCHAR(64)  NOT NULL DEFAULT '',
    detail             TEXT         NOT NULL DEFAULT '',
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX IF NOT EXISTS idx_logs_user_created    ON logs (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_logs_model_created   ON logs (model, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_logs_channel_created ON logs (channel_id, created_at DESC);

-- ── 运行期配置 ─────────────────────────────────────────
CREATE TABLE IF NOT EXISTS options (
    "key"  VARCHAR(64) PRIMARY KEY,
    value  TEXT NOT NULL DEFAULT ''
);

-- ── 迁移版本记录 ───────────────────────────────────────
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     VARCHAR(64) PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
