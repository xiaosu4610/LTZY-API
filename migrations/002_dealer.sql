-- 002_dealer.sql · 经销商体系
--
-- 背景：这个项目面向大众，免费模型直接开放（不需要注册登录）。
-- 收费走「经销商」模式 —— 经销商由我们自己创建账号，他们拿到的
-- 模型清单和定价都不同，登录后只看自己账户的余额和调用日志。
--
-- 和 v1（001_init）的关系：
--   001 里的 users 表继续用，role=1 现在表示「经销商」，
--   role=100 是超管（我们自己）。不新增表，避免两套用户体系。

-- ── 经销商可用模型与专属定价 ──────────────────────────
-- 每个经销商看到的模型不一样、价格也不一样，所以不能用全局 model_prices，
-- 必须按 (经销商, 模型) 存一行。
--
-- price 单位与 model_prices.per_request_price 一致（quota，1 元 = 1,000,000）。
-- price = 0 表示这个模型对他是免费的（通常是赠送额度）。
CREATE TABLE IF NOT EXISTS dealer_models (
    id          SERIAL PRIMARY KEY,
    user_id     INT          NOT NULL,
    model       VARCHAR(128) NOT NULL,
    price       BIGINT       NOT NULL DEFAULT 0,
    enabled     SMALLINT     NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- 同一个经销商对同一个模型只能有一条定价
CREATE UNIQUE INDEX IF NOT EXISTS uq_dealer_models ON dealer_models (user_id, model);
CREATE INDEX IF NOT EXISTS idx_dealer_models_user ON dealer_models (user_id, enabled);

-- ── 日志表增加经销商维度 ────────────────────────────
-- user_id 已有，但查询时经常要「某经销商的日志按时间倒序」，
-- 单独建索引比复合索引更省空间（分区表上尤其明显）。
CREATE INDEX IF NOT EXISTS idx_logs_request_id ON logs (request_id);

-- ── 经销商相关配置 ─────────────────────────────────
INSERT INTO options ("key", value) VALUES
    ('dealer_login_enabled', 'true'),
    ('log_retention_days',   '180')
ON CONFLICT ("key") DO NOTHING;

-- ── 现有用户的角色修正 ──────────────────────────────
-- 从生产导入的用户 role 都是 1，语义上就是经销商，不用改。
-- 这里只确保 admin 是超管。
UPDATE users SET role = 100 WHERE username = 'admin';
