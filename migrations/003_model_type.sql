-- 003_model_type.sql · 模型类型与端点声明
--
-- 背景：abilities 表只有「模型 → 渠道」的映射，没有「这个模型走哪个端点」的信息。
-- 结果 embedding 模型也被塞进 chat 入口，调用必然 404。
--
-- 这次加上：
--   endpoint  该模型对应的上游端点（chat / embeddings / rerankings / ...）
--   category  模型类别（chat / embedding / rerank / vision / audio / other）
--
-- 两者分开是有原因的：
--   endpoint 决定"发到哪个 URL"
--   category 决定"在界面上归到哪一类"
-- 大多数情况一一对应，但 vision 模型走的还是 chat 端点。

ALTER TABLE abilities
    ADD COLUMN IF NOT EXISTS endpoint VARCHAR(64) NOT NULL DEFAULT 'chat';

ALTER TABLE abilities
    ADD COLUMN IF NOT EXISTS category VARCHAR(32) NOT NULL DEFAULT 'chat';

-- 索引：按端点筛选"这个端点上能用哪些模型"
CREATE INDEX IF NOT EXISTS idx_abilities_endpoint ON abilities (endpoint, enabled);

-- ── 已知模型的修正 ──────────────────────────────────
-- Embedding 模型走 /v1/embeddings，不能出现在 chat 广场
UPDATE abilities SET endpoint = 'embeddings', category = 'embedding'
WHERE model LIKE '%embed%' OR model LIKE '%bge%' OR model LIKE '%m3e%' OR model LIKE '%jina%';

-- Rerank 模型走 /v1/rerankings
UPDATE abilities SET endpoint = 'rerankings', category = 'rerank'
WHERE model LIKE '%rerank%';

-- 语音识别 / 合成
UPDATE abilities SET endpoint = 'audio', category = 'audio'
WHERE model LIKE '%asr%' OR model LIKE '%tts%' OR model LIKE '%voice%' OR model LIKE '%sense%';

-- 视觉理解模型：虽然叫 vision，但走的是 chat 端点（传图片进去）
-- 所以 endpoint 保持 chat，只把 category 标出来
UPDATE abilities SET category = 'vision'
WHERE (model LIKE '%vision%' OR model LIKE '%vl-%' OR model LIKE '%paligemma%')
  AND category = 'chat';

-- 知识库/表格/结构化
UPDATE abilities SET category = 'other'
WHERE model LIKE '%kumo%' OR model LIKE '%tabular%' OR model LIKE '%relational%';
