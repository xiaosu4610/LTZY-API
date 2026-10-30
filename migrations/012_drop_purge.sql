-- 012_drop_purge.sql —— 废弃「余额耗尽自动删除账号」
--
-- 背景：
--   早期（迁移旧生产系统那阵）定的规则是：
--     account_type = 'special' 的账号，余额 <= 0 时**物理删除**。
--   目的是清走不活跃的历史用户，省资源。
--
--   2026-10-29 开始正常运营，这条规则必须废掉：
--
--     1. 用户现在会充值。余额为 0 只是正常的中间状态
--        （刚用完、正要再充），删号等于把人赶走；
--     2. 账号背后连着 tokens / logs / recharge_orders，
--        物理删除会对账数据断链，出了纠纷查无可查；
--     3. 免费模型不扣余额，留一个账号的存储成本约等于零，
--        「省资源」这个理由已经不成立。
--
-- 影响：
--   · 删除审计表 account_purge_log
--   · 删除 bin/purge-expired-accounts.php 与 bin/verify-purge.php
--   · account_type 保留为**纯来源标记**，不再影响任何生命周期
--
--   没有定时任务在跑这个清理（已核查 crontab 与 systemd timer），
--   所以删掉不会有任务报错。

BEGIN;

DROP TABLE IF EXISTS account_purge_log;

COMMENT ON COLUMN users.account_type IS
  '账号来源标记：special=旧系统导入，managed=后台创建，normal=自助注册。仅用于统计与展示，不参与任何自动删除逻辑';

COMMIT;
