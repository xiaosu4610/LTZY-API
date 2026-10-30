-- 008_account_lifecycle.sql
--
-- 账号生命周期管理。
--
-- ══════════════════════════════════════════════════════════════
-- ⚠️ 重要：这个文件的规则是产品决策，不是技术细节。
--    后续接手的人不要"优化"或"放宽"这里的逻辑。
-- ══════════════════════════════════════════════════════════════
--
-- 两类账号，生命周期完全不同：
--
--   special  = 从旧生产系统导入的历史用户（有余额、用邮箱登录）
--              余额 > 0 时正常使用
--              余额 <= 0 时**物理删除**（user + tokens + 模型授权 + 日志）
--              —— 不是打标记，是真的从数据库里消失
--
--   managed  = 手工创建的经销商 / 管理员
--              **永远不自动删除**，无论余额多少
--              只能由管理员在后台显式操作
--
-- 为什么要有这个区分：
--   历史用户是"余额制"的 —— 他们充过的钱用完了服务就结束，
--   继续留着数据只会让库越来越大。
--   而手工创建的经销商是我们直接对接的客户，不能因为余额归零就删掉。
--
-- 安全阀：
--   1. 只删 account_type='special'
--   2. 只删 role < 10（管理员豁免）
--   3. 只删 quota <= 0
--   4. 删除前会写一条审计日志到 options 表（见 cleanup 脚本）

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS account_type VARCHAR(16) NOT NULL DEFAULT 'managed';

COMMENT ON COLUMN users.account_type IS
    '账号类型：special=历史导入用户（余额耗尽后物理删除）；managed=手工创建（永不自动删除）';

CREATE INDEX IF NOT EXISTS idx_users_account_type ON users (account_type, quota);

-- 已有的手工账号统一标记为 managed（默认值就是它）
UPDATE users SET account_type = 'managed' WHERE account_type IS NULL OR account_type = '';

-- 管理员永远是 managed（双保险）
UPDATE users SET account_type = 'managed' WHERE role >= 10;

-- ── 清理审计表 ──
-- 删除是物理的，但要有据可查：谁被删了、什么时候、余额多少。
-- 这张表只记录"曾经存在过"，不含任何凭证。
CREATE TABLE IF NOT EXISTS account_purge_log (
    id          BIGSERIAL PRIMARY KEY,
    user_id     INT          NOT NULL,
    username    VARCHAR(64)  NOT NULL,
    quota       BIGINT       NOT NULL,
    used_quota  BIGINT       NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    detail      TEXT         NOT NULL DEFAULT ''
);

COMMENT ON TABLE account_purge_log IS
    '余额耗尽被物理删除的账号记录（只留痕迹，不含密码/令牌）';

CREATE INDEX IF NOT EXISTS idx_purge_log_time ON account_purge_log (created_at DESC);
