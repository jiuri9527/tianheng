-- =============================================================================
-- 天衡（Tianheng）0001_init.down.sql
-- 回滚 0001_init.up.sql：按外键依赖逆序 DROP。
-- =============================================================================

BEGIN;

-- 论坛（依赖 users / forum_boards / forum_posts）
DROP TABLE IF EXISTS forum_replies;
DROP TABLE IF EXISTS forum_posts;
DROP TABLE IF EXISTS forum_boards;

-- 资料与头像
DROP TABLE IF EXISTS profiles;

-- 计费与用量
DROP TABLE IF EXISTS quota_ledger;
DROP TABLE IF EXISTS usage_daily;
DROP TABLE IF EXISTS model_probes;

-- 请求日志
DROP TABLE IF EXISTS request_logs;

-- 令牌
DROP TABLE IF EXISTS tokens;

-- 模型与别名（models 依赖 channels）
DROP TABLE IF EXISTS model_aliases;
DROP TABLE IF EXISTS models;

-- 渠道（依赖 users）
DROP TABLE IF EXISTS channels;

-- 设置
DROP TABLE IF EXISTS settings;

-- 用户
DROP TABLE IF EXISTS users;

COMMIT;
