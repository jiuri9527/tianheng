-- =============================================================================
-- 天衡（Tianheng）0001_init.up.sql
-- 关系型镜像 DDL：与 src/internal/store/store.go 的 JSON 结构体逐字段一致。
-- 说明：天衡默认使用「JSON 文件 + WAL + 完整性链」零依赖存储；
--       本迁移用于需要 PostgreSQL 镜像/数据仓库/BI 接入的部署形态，
--       字段名严格取自 Go struct 的 json tag，不新增臆测字段。
-- =============================================================================

BEGIN;

-- --------------------------- F1 用户 ---------------------------
-- 天衡：所有用户都是超级用户，没有任何管理员角色。
-- role 列保留但恒为 'superuser'（CHECK 约束固化该语义）。
CREATE TABLE IF NOT EXISTS users (
    id            TEXT        PRIMARY KEY,
    email         TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL DEFAULT 'superuser'
                  CHECK (role IN ('superuser')),
    balance_usd   NUMERIC(18,6) NOT NULL DEFAULT 0,
    status        TEXT        NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active','disabled')),
    created_at    BIGINT      NOT NULL,
    updated_at    BIGINT      NOT NULL
);
COMMENT ON TABLE  users            IS '天衡用户；无管理员角色，role 恒为 superuser';
COMMENT ON COLUMN users.role       IS '天衡无角色区分，仅 superuser';
COMMENT ON COLUMN users.password_hash IS '迭代式 HMAC-SHA256 KDF，非明文';

-- --------------------------- F2 渠道（众包 base_uri + api_key）---------------------------
CREATE TABLE IF NOT EXISTS channels (
    id                TEXT    PRIMARY KEY,
    owner_id          TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name              TEXT    NOT NULL,
    provider_type     TEXT    NOT NULL
                      CHECK (provider_type IN ('openai','anthropic','xai','gemini','openai_compatible')),
    base_uri          TEXT    NOT NULL,
    models_path       TEXT    NOT NULL DEFAULT '',
    chat_path         TEXT    NOT NULL DEFAULT '',
    auth_style        TEXT    NOT NULL DEFAULT 'bearer',
    api_version       TEXT    NOT NULL DEFAULT '',
    -- AES-256-GCM 三段密文：绝不存明文 api_key
    api_key_cipher    BYTEA   NOT NULL,
    api_key_iv        BYTEA   NOT NULL,
    api_key_tag       BYTEA   NOT NULL,
    key_hint          TEXT    NOT NULL DEFAULT '',
    custom_headers    JSONB   NOT NULL DEFAULT '{}'::jsonb,
    visibility        TEXT    NOT NULL DEFAULT 'public'
                      CHECK (visibility IN ('public','private')),
    weight            INTEGER NOT NULL DEFAULT 1,
    priority          INTEGER NOT NULL DEFAULT 0,
    status            TEXT    NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active','disabled')),
    probe_status      TEXT    NOT NULL DEFAULT 'unavailable'
                      CHECK (probe_status IN ('unavailable','ok','failed')),
    health_checked_at BIGINT  NOT NULL DEFAULT 0,
    last_error        TEXT    NOT NULL DEFAULT '',
    fail_count        INTEGER NOT NULL DEFAULT 0,
    cooldown_until    BIGINT  NOT NULL DEFAULT 0,
    created_at        BIGINT  NOT NULL,
    updated_at        BIGINT  NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_channels_owner      ON channels (owner_id);
CREATE INDEX IF NOT EXISTS idx_channels_status_vis ON channels (status, visibility);
COMMENT ON TABLE  channels             IS '用户贡献的上游渠道；api_key 仅密文落库';
COMMENT ON COLUMN channels.key_hint    IS '仅末 4 位提示，永不回显完整密钥';
COMMENT ON COLUMN channels.base_uri    IS '经 SSRF 校验：仅 http/https，拒绝内网/回环/元数据';

-- --------------------------- F3/F4 真实模型（发现 + 真实探测样例）---------------------------
CREATE TABLE IF NOT EXISTS models (
    id                      TEXT    PRIMARY KEY,
    channel_id              TEXT    NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    upstream_id             TEXT    NOT NULL,
    alias                   TEXT    NOT NULL,
    display_name            TEXT    NOT NULL DEFAULT '',
    provider_type           TEXT    NOT NULL,
    context_length          INTEGER NOT NULL DEFAULT 0,
    max_output_tokens       INTEGER NOT NULL DEFAULT 0,
    supports_stream         BOOLEAN NOT NULL DEFAULT FALSE,
    supports_vision         BOOLEAN NOT NULL DEFAULT FALSE,
    supports_tools          BOOLEAN NOT NULL DEFAULT FALSE,
    supports_reasoning      BOOLEAN NOT NULL DEFAULT FALSE,
    input_price_per_mtok    DOUBLE PRECISION NOT NULL DEFAULT 0,
    output_price_per_mtok   DOUBLE PRECISION NOT NULL DEFAULT 0,
    param_source            TEXT    NOT NULL DEFAULT 'upstream',
    raw_json                JSONB,
    probe_status            TEXT    NOT NULL DEFAULT 'unavailable'
                            CHECK (probe_status IN ('unavailable','ok','failed')),
    probe_input             JSONB,
    probe_output            TEXT    NOT NULL DEFAULT '',
    probe_prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    probe_completion_tokens INTEGER NOT NULL DEFAULT 0,
    probe_latency_ms        INTEGER NOT NULL DEFAULT 0,
    probed_at               BIGINT  NOT NULL DEFAULT 0,
    enabled                 BOOLEAN NOT NULL DEFAULT TRUE,
    UNIQUE (channel_id, upstream_id)
);
CREATE INDEX IF NOT EXISTS idx_models_alias     ON models (alias);
CREATE INDEX IF NOT EXISTS idx_models_provider  ON models (provider_type);
COMMENT ON TABLE  models               IS '真实上游 GET {base_uri}/models 的逐字投影；拉取失败则 probe_status=unavailable，绝不编造';
COMMENT ON COLUMN models.raw_json      IS '上游原始对象逐字保留';
COMMENT ON COLUMN models.probe_input   IS '真实探测请求体（模型输入样例）';
COMMENT ON COLUMN models.probe_output  IS '真实探测响应文本（模型输出样例）';

-- --------------------------- 统一模型别名 ---------------------------
CREATE TABLE IF NOT EXISTS model_aliases (
    alias              TEXT    PRIMARY KEY,
    canonical_model_id TEXT    NOT NULL DEFAULT '',
    description        TEXT    NOT NULL DEFAULT '',
    enabled            BOOLEAN NOT NULL DEFAULT TRUE,
    price_override     JSONB,
    created_at         BIGINT  NOT NULL
);
COMMENT ON TABLE model_aliases IS '全局统一模型名 → 真实模型映射；同名可挂多渠道做负载均衡';

-- --------------------------- F9 平台令牌（天衡：无任何限制）---------------------------
CREATE TABLE IF NOT EXISTS tokens (
    id              TEXT    PRIMARY KEY,
    user_id         TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT    NOT NULL DEFAULT '',
    token_hash      TEXT    NOT NULL UNIQUE,
    key_hint        TEXT    NOT NULL DEFAULT '',
    allowed_models  JSONB   NOT NULL DEFAULT '[]'::jsonb,
    -- 天衡不设任何使用限制：以下字段恒为 0 / 空，CHECK 约束固化语义
    rpm             INTEGER NOT NULL DEFAULT 0 CHECK (rpm = 0),
    tpm             INTEGER NOT NULL DEFAULT 0 CHECK (tpm = 0),
    max_concurrency INTEGER NOT NULL DEFAULT 0 CHECK (max_concurrency = 0),
    quota_usd       NUMERIC(18,6) NOT NULL DEFAULT 0 CHECK (quota_usd = 0),
    used_usd        NUMERIC(18,6) NOT NULL DEFAULT 0,
    ip_allowlist    JSONB   NOT NULL DEFAULT '[]'::jsonb,
    expires_at      BIGINT  NOT NULL DEFAULT 0 CHECK (expires_at = 0),
    status          TEXT    NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','revoked')),
    last_used_at    BIGINT  NOT NULL DEFAULT 0,
    created_at      BIGINT  NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tokens_user   ON tokens (user_id);
CREATE INDEX IF NOT EXISTS idx_tokens_status ON tokens (status);
COMMENT ON TABLE  tokens                IS '平台令牌 sk-；只存 sha256，明文仅创建响应返回一次';
COMMENT ON COLUMN tokens.rpm            IS '天衡不设限，恒 0';
COMMENT ON COLUMN tokens.ip_allowlist   IS '天衡不设 IP 白名单，恒空';

-- --------------------------- F10 请求日志（完整输入/输出）---------------------------
CREATE TABLE IF NOT EXISTS request_logs (
    id                TEXT    PRIMARY KEY,
    request_id        TEXT    NOT NULL,
    user_id           TEXT    NOT NULL DEFAULT '',
    token_id          TEXT    NOT NULL DEFAULT '',
    alias             TEXT    NOT NULL DEFAULT '',
    model_id          TEXT    NOT NULL DEFAULT '',
    channel_id        TEXT    NOT NULL DEFAULT '',
    provider_type     TEXT    NOT NULL DEFAULT '',
    stream            BOOLEAN NOT NULL DEFAULT FALSE,
    request_body      JSONB,
    response_body     TEXT    NOT NULL DEFAULT '',
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    ttft_ms           INTEGER NOT NULL DEFAULT 0,
    latency_ms        INTEGER NOT NULL DEFAULT 0,
    status_code       INTEGER NOT NULL DEFAULT 0,
    error_code        TEXT    NOT NULL DEFAULT '',
    cost_usd          NUMERIC(18,6) NOT NULL DEFAULT 0,
    client_ip         TEXT    NOT NULL DEFAULT '',
    created_at        BIGINT  NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_logs_user_created  ON request_logs (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_logs_alias_created ON request_logs (alias, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_logs_request_id    ON request_logs (request_id);
COMMENT ON TABLE request_logs IS '每次调用的完整输入/输出与计量；驱动实时监控与用量看板';

-- --------------------------- 真实探测历史 ---------------------------
CREATE TABLE IF NOT EXISTS model_probes (
    id         TEXT    PRIMARY KEY,
    model_id   TEXT    NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    input      JSONB,
    output     TEXT    NOT NULL DEFAULT '',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    ok         BOOLEAN NOT NULL DEFAULT FALSE,
    error      TEXT    NOT NULL DEFAULT '',
    created_at BIGINT  NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_probes_model ON model_probes (model_id, created_at DESC);

-- --------------------------- 日用量聚合 ---------------------------
CREATE TABLE IF NOT EXISTS usage_daily (
    day               TEXT    NOT NULL,
    user_id           TEXT    NOT NULL DEFAULT '',
    alias             TEXT    NOT NULL DEFAULT '',
    requests          INTEGER NOT NULL DEFAULT 0,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd          NUMERIC(18,6) NOT NULL DEFAULT 0,
    PRIMARY KEY (day, user_id, alias)
);

-- --------------------------- 额度流水（仅记账，不构成限制）---------------------------
CREATE TABLE IF NOT EXISTS quota_ledger (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL DEFAULT '',
    delta_usd  NUMERIC(18,6) NOT NULL DEFAULT 0,
    reason     TEXT    NOT NULL DEFAULT '',
    ref_id     TEXT    NOT NULL DEFAULT '',
    created_at BIGINT  NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ledger_user ON quota_ledger (user_id, created_at DESC);

-- --------------------------- 系统设置 ---------------------------
CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at BIGINT NOT NULL DEFAULT 0
);

-- --------------------------- 社区论坛（F15 新增）---------------------------
CREATE TABLE IF NOT EXISTS forum_boards (
    id          TEXT   PRIMARY KEY,
    slug        TEXT   NOT NULL UNIQUE,
    name        TEXT   NOT NULL,
    description TEXT   NOT NULL DEFAULT '',
    created_at  BIGINT NOT NULL
);
COMMENT ON TABLE forum_boards IS '论坛版块；由首次真实发帖自动创建，无预置假数据';

CREATE TABLE IF NOT EXISTS forum_posts (
    id          TEXT    PRIMARY KEY,
    board_id    TEXT    NOT NULL REFERENCES forum_boards(id) ON DELETE CASCADE,
    author_id   TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    author_name TEXT    NOT NULL DEFAULT '',
    title       TEXT    NOT NULL,
    content     TEXT    NOT NULL,
    pinned      BOOLEAN NOT NULL DEFAULT FALSE,
    like_count  INTEGER NOT NULL DEFAULT 0,
    reply_count INTEGER NOT NULL DEFAULT 0,
    created_at  BIGINT  NOT NULL,
    updated_at  BIGINT  NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_posts_board_created ON forum_posts (board_id, pinned DESC, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_posts_author        ON forum_posts (author_id);
COMMENT ON COLUMN forum_posts.title IS '经 sanitizeText 剥离标签 + JSON HTML 转义，防存储型 XSS';

CREATE TABLE IF NOT EXISTS forum_replies (
    id          TEXT   PRIMARY KEY,
    post_id     TEXT   NOT NULL REFERENCES forum_posts(id) ON DELETE CASCADE,
    author_id   TEXT   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    author_name TEXT   NOT NULL DEFAULT '',
    content     TEXT   NOT NULL,
    created_at  BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_replies_post ON forum_replies (post_id, created_at ASC);

-- --------------------------- 个人资料与头像（F16 新增）---------------------------
CREATE TABLE IF NOT EXISTS profiles (
    user_id             TEXT   PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    nickname            TEXT   NOT NULL DEFAULT '',
    bio                 TEXT   NOT NULL DEFAULT '',
    avatar_id           TEXT   NOT NULL DEFAULT '',
    avatar_ext          TEXT   NOT NULL DEFAULT '',
    avatar_content_type TEXT   NOT NULL DEFAULT '',
    avatar_bytes        INTEGER NOT NULL DEFAULT 0,
    avatar_decoded      BOOLEAN NOT NULL DEFAULT FALSE,
    has_thumbnail       BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at          BIGINT NOT NULL DEFAULT 0
);
COMMENT ON TABLE  profiles             IS '用户自上传头像与资料；头像字节落 0600 文件系统，本表只存元数据';
COMMENT ON COLUMN profiles.avatar_id   IS '服务端随机 id，绝不采信用户文件名（防路径穿越/XSS）';

COMMIT;
