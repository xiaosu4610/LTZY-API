-- 005_responses_mode.sql · 上游 Responses 端点的兼容模式
--
-- 实测发现两家的 /v1/responses 行为完全不同：
--
--   上游 A   : 端点存在，但绝大多数模型返回 404
--              （模型本身不支持这个端点，不是路径问题）
--
--   上游 B   : 端点存在且返回标准 Responses 结构
--              但请求体必须**同时**带 messages 和 input，
--              只给 input 会报 "必传参数: messages"
--
-- 所以不能简单地"支持就直通"，要按渠道声明模式：
--
--   native  = 严格按 OpenAI 官方格式（input），可直接透传
--   compat  = 需要补齐字段才能用（部分上游：补 messages）
--   none    = 不支持，一律走 chat 翻译

ALTER TABLE channels
    ADD COLUMN IF NOT EXISTS responses_mode VARCHAR(16) NOT NULL DEFAULT 'none';

COMMENT ON COLUMN channels.responses_mode IS
    '上游 /v1/responses 的兼容模式：native（标准）/ compat（需补字段）/ none（不支持）';

-- 按实测结果配置
UPDATE channels SET responses_mode = 'none' WHERE id = 3;    -- 上游 A：该端点对免费模型不可用
UPDATE channels SET responses_mode = 'compat' WHERE id = 14; -- 上游 B：需补 messages 字段

-- 其余渠道默认 none（走 chat 翻译，最稳）
