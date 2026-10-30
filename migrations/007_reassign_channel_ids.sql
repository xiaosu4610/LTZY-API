-- 007_reassign_channel_ids.sql
--
-- 把渠道 ID 重新编排为连续值。
--
-- 现状：渠道 ID 需要重排（历史原因 ID 不连续）
--   —— 这两个 ID 是从生产 Go 版原样导入的，中间缺了 1、2、4-13，
--      都是以前删掉的付费渠道。留着这种稀疏 ID 有两个坏处：
--        1. 运维看 ID 会疑惑"1-13 哪去了，是不是有数据丢了"
--        2. 日志和报错里出现 channel_id=14 时，看不出总共只有两个渠道
--
-- 目标：重排为从 1 开始的连续 ID，序列同步重置
--
-- ⚠️ 本文件**不要写 BEGIN/COMMIT**。
--    bin/migrate.php 已经把整个文件包在一个事务里了，
--    再写 BEGIN 会和外层事务嵌套，PG 报 "no active transaction"
--    然后回滚逻辑自己也崩了（这个坑踩过两次）。
--
-- 安全性：
--   · abilities / logs 对 channels 没有外键约束（建表时就没加）
--   · 目标 ID 1、2 当前不存在，不会和源 ID 3、14 冲突
--   · abilities 的 uq_abilities(model, channel_id) 在更新期间也不会撞
--     （3→1 与 14→2 是两组独立数据）

-- 1) 渠道本身
UPDATE channels SET id = 1  WHERE id = 3;
UPDATE channels SET id = 2  WHERE id = 14;

-- 2) 模型映射
UPDATE abilities SET channel_id = 1  WHERE channel_id = 3;
UPDATE abilities SET channel_id = 2  WHERE channel_id = 14;

-- 3) 历史日志
UPDATE logs SET channel_id = 1  WHERE channel_id = 3;
UPDATE logs SET channel_id = 2  WHERE channel_id = 14;

-- 4) 序列重置 —— 不重置的话下次新建渠道会从 15 开始
SELECT setval('channels_id_seq', (SELECT COALESCE(MAX(id), 1) FROM channels));
