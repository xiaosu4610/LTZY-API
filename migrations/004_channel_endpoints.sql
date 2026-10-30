-- 004_channel_endpoints.sql · 渠道支持的端点声明
--
-- 背景：要让「入站 Responses + 上游支持 Responses」走原生直通，
-- 网关必须知道每个上游到底支持哪些端点。
--
-- 为什么用数组而不是多个布尔字段：
--   以后可能还要接 /v1/embeddings、/v1/rerankings、/v1/audio/* 等各种组合，
--   加一个布尔字段就要改表结构，数组则直接追加。
--
-- 探测依据（2026-10-27 实测）：
--   上游 A   : /v1/chat/completions ✓  /v1/responses ✓  /v1/embeddings ✓
--   上游 B   : /v1/chat/completions ✓  /v1/responses ✓  /v1/embeddings ✓  /v1/rerankings ✓
--
-- 注意：探测时用的是「空 model 请求」—— 如果端点存在会返回 400 参数错误，
-- 不存在会返回 404。这个方法比看文档可靠，也适用于以后接的新上游。

ALTER TABLE channels
    ADD COLUMN IF NOT EXISTS endpoints TEXT[] NOT NULL
    DEFAULT ARRAY['chat'];

COMMENT ON COLUMN channels.endpoints IS
    '该渠道支持的上游端点：chat / responses / embeddings / rerankings / audio';

-- 按实测结果填写
UPDATE channels SET endpoints = ARRAY['chat', 'responses', 'embeddings']
WHERE id = 3;

UPDATE channels SET endpoints = ARRAY['chat', 'responses', 'embeddings', 'rerankings']
WHERE id = 14;

-- 其余渠道保持默认 ['chat']
