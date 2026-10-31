-- 013_users_username_lower_index.sql —— 用户名大小写不敏感索引
--
-- 背景（开源后用户反馈的第 1 个 Issue）：
--   「网页登录提示用户名或密码错误，无法登录」
--
--   根因是 PostgreSQL 的 = 区分大小写，而注册 / 安装向导都保留用户输入的
--   原始大小写。数据库存 `ustinian`、用户输入 `Ustinian` 就查不到，
--   统一报「用户名或密码错误」——用户完全无从自行排查。
--
--   代码侧已改为 `lower(username) = lower(?)`，这里配套加函数索引，
--   否则每次登录都会全表扫描。用户量上千后差异明显。
--
--   lower() 对中文是 no-op，不影响中文用户名。
--
-- 幂等：IF NOT EXISTS，重复执行安全。

CREATE INDEX IF NOT EXISTS idx_users_username_lower
    ON users (lower(username));

-- 邮箱同样做大小写不敏感查找（登录支持用邮箱作为账号）
CREATE INDEX IF NOT EXISTS idx_users_email_lower
    ON users (lower(email))
    WHERE email <> '';
