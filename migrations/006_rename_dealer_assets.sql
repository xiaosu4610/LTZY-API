-- 006_rename_dealer_assets.sql
--
-- 把 dealer_models 改名为 account_models。
--
-- ── 为什么改 ──
--
-- 表名本身会出现在错误信息、日志、数据库备份里：
--   ERROR: relation "dealer_models" does not exist
--   pg_dump 的注释
--   ORM 的 SQL 日志
--
-- 一旦这类信息泄漏（比如报错页没关 debug，或者备份文件流出），
-- 对方就知道"这个系统有经销商这个概念"。
--
-- 这和前面把 /api/dealer/* 改成 /api/account/*、
-- 把 /admin 路径删掉是同一个原则：**内部概念不该出现在任何可能外泄的地方**。
--
-- 顺带把外键名、索引名也改掉，保持一致。

-- 1) 表名
ALTER TABLE IF EXISTS dealer_models RENAME TO account_models;

-- 2) 索引名（PG 里索引名是全局唯一的，改名要单独做）
ALTER INDEX IF EXISTS uq_dealer_models RENAME TO uq_account_models;
ALTER INDEX IF EXISTS idx_dealer_models_user RENAME TO idx_account_models_user;

-- 3) 相关注释
COMMENT ON TABLE account_models IS '账户可用的模型与专属价格（每个账户一份清单）';
COMMENT ON COLUMN account_models.user_id IS '账户 ID';
COMMENT ON COLUMN account_models.price IS '该账户使用此模型的按次价格（quota，1 元 = 1,000,000）；0 表示免费';
