# 通用大模型 API 聚合中转站 —— 项目生成提示词（完整版）

> 使用方式：把本文件全文（不含本行说明）作为一条消息发给代码生成模型，它会一次性产出完整可运行项目。所有 `[占位符]` 均可直接替换，不需要再补充需求。

---

你是资深全栈架构师、API 网关专家与 Go/TypeScript 后端工程师。现在请**一次性、完整、不省略地**产出「通用大模型 API 聚合中转站（Unified LLM Gateway / API Relay）」这个项目的**全部可运行源码、配置、迁移、测试与文档**。禁止输出「略」「其余类似」「TODO」「篇幅所限」「自行补充」等任何省略标记；每个文件给出完整内容并用 ` ```语言:路径 ` 代码块标注；代码必须能直接 `go build` / `pnpm build` 通过并 `docker compose up -d` 启动。

## 0. 项目代号、一句话定义与最终形态

- 代号：`llm-hub`（可替换为 `[PROJECT_NAME]`）。
- 一句话定义：一个多租户的「众包渠道 + 统一出口」大模型 API 网关——**任何人**都可以把自己拥有的上游 `base_uri` 与 `api_key` 贡献为一个渠道；站点自动连接上游，**拉取该渠道真实可用的模型清单、每个模型的实际参数，并用一次真实调用采集模型的输入/输出样例**；站点把 grok(xAI)、Claude(Anthropic)、ChatGPT(OpenAI)、Gemini 以及任意 OpenAI 兼容上游的协议统一转换，聚合为一个全局模型池；**所有人**只用一个平台令牌 `sk-xxxx`、一个统一地址 `[DEPLOY_HOST]/v1`，即可调用池中的任意模型。
- 最终形态：管理前台（贡献渠道 / 看模型 / 建令牌 / 看用量）+ 管理后台（审核 / 编排 / 监控）+ OpenAI 兼容数据面网关 + Anthropic Messages 兼容入口 + 流式 SSE 反代理 + 计量计费。

## 1. 核心概念（数据字典，必须贯穿全文）

| 概念 | 定义 |
|---|---|
| Provider 供应商类型 | `openai` / `anthropic` / `xai`(=grok) / `gemini` / `openai_compatible`（自定义） |
| Channel 渠道 | 一条上游连接：贡献者填写 `base_uri` + `api_key` + `provider_type`（+ 可选自定义 header / 路径覆盖）。渠道有 `visibility=public\|private`：公开渠道的模型进入全局池供所有人调用，私有仅贡献者可用。 |
| Model 模型 | 从上游真实发现得到，不是手写死列表。含上下文长度、最大输出、能力位、价格、原始 JSON、以及**探测得到的输入样例/输出样例/延迟**。 |
| ModelAlias 统一模型名 | 全局池对外暴露的唯一名字；支持 `gpt-4o`、`claude-3-7-sonnet`、`grok-3` 等短名，也支持 `xai/grok-3` 带前缀精确定位某渠道。同一别名下可挂多个渠道做负载均衡。 |
| User 用户 | 注册用户，既是渠道贡献者也是 API 消费者。角色 `admin` / `user`。 |
| Token 平台令牌 | `sk-<32位随机>`，用户调用本聚合站 `/v1/*` 的凭证。绑定模型白名单、RPM/TPM 限流、额度余额、过期时间、IP 白名单。 |
| RequestLog 请求日志 | 每次调用记录**完整输入 messages、完整输出 content、prompt_tokens、completion_tokens、TTFT、总耗时、状态码、命中渠道、花费、request_id**。 |
| Quota 额度 | 用户余额（USD）或 token 额度；贡献公开渠道按被他人调用量返还奖励额度。 |

上游真实端点常量（写进代码常量表，贡献渠道时给出下拉提示）：

```
OPENAI_BASE_URI     = https://api.openai.com/v1
XAI_BASE_URI        = https://api.x.ai/v1                 # grok，OpenAI 兼容
ANTHROPIC_BASE_URI  = https://api.anthropic.com/v1       # Claude，/messages + /models
GEMINI_BASE_URI     = https://generativelanguage.googleapis.com/v1beta/openai  # OpenAI 兼容层
```

鉴权风格：`openai/xai/gemini/openai_compatible` → `Authorization: Bearer <key>`；`anthropic` → `x-api-key: <key>` + `anthropic-version: 2023-06-01`。

## 2. 功能需求（F1–F14，逐条实现，不许合并省略）

**F1 用户与鉴权**：注册（邮箱+密码，bcrypt/argon2 哈希）、登录（JWT access + refresh）、退出、改密、角色管理；首个注册用户自动成为 admin；管理面所有 `/api/*` 需 JWT；数据面 `/v1/*` 需 `Authorization: Bearer sk-xxx`。

**F2 渠道贡献与管理（核心入口）**：任意登录用户 `POST /api/channels` 提交 `{name, provider_type, base_uri, api_key, visibility, weight, priority, custom_headers, models_path, chat_path}`。保存前做三步校验：① `base_uri` 合法性（必须 http(s)://，禁止内网/回环/元数据地址，防 SSRF）；② 用该 key 请求上游模型列表验证可用；③ `api_key` 用 AES-256-GCM 加密落库，之后**永不明文回显**，接口只返回 `key_hint`（末 4 位）。支持列表、编辑、启停、删除、手动触发同步、手动触发探测。

**F3 真实模型参数发现（"获取实际模型参数"）**：新增/编辑渠道或定时/手动同步时，按供应商调用：
- OpenAI 系：`GET {base_uri}/models` → `data[].id, owned_by`；
- Anthropic：`GET {base_uri}/models` (带 `x-api-key` + `anthropic-version`) → `data[].id, display_name, created_at`；
- Gemini 兼容层：`GET {base_uri}/models`；
- 对 `openai_compatible` 允许 `models_path` 覆盖（如 `/v1/models`）。
再对每个模型尽量补齐参数：从上游返回原始 JSON 提取 `context_length / max_tokens / max_output_tokens / input_modalities / output_modalities / supports_vision / supports_tools / pricing`；上游不返回时，按 `provider_type + model id` 的内置能力表（在代码里给一张覆盖 grok-3/grok-3-mini、claude-3-7-sonnet/claude-3-5-haiku、gpt-4o/gpt-4o-mini/o1/o3、gemini-2.5-pro/flash 的表）补全，并标注 `param_source=upstream|builtin|probe`。结果 upsert 到 `models` 与 `channel_models`，生成全局 `model_aliases`。

**F4 真实模型输入/输出采集（"模型输入输出"）**：对每个新发现模型发一次极小探测请求（如 `messages=[{"role":"user","content":"Reply with the single word: OK"}]`，`max_tokens=16`，`stream=false`），记录：实际请求体（输入）、实际响应文本（输出）、`prompt_tokens/completion_tokens`、`latency_ms`、是否成功、错误信息。存到 `models.probe_*` 与 `model_probes` 表，并在前台「模型详情」页展示「真实输入样例 / 真实输出样例 / 实测延迟 / 上下文长度 / 能力位 / 价格」。探测失败只标记 `probe_status=failed`，不影响模型入池。提供 `POST /api/channels/:id/probe` 重新探测，`POST /api/models/:id/probe` 单模型重探。

**F5 统一数据面 API（OpenAI 兼容，供所有人调用）**：
- `GET  /v1/models` —— 返回全局聚合模型池（OpenAI 风格 `{object:"list", data:[{id,object:"model",owned_by,context_length,...}]}`）。
- `POST /v1/chat/completions` —— 主入口，支持 `stream=true/false`、`tools/tool_choice`、`response_format`、`n`、`stop`、`temperature/max_tokens` 等全部 OpenAI 参数透传。
- `POST /v1/completions`（legacy）、`POST /v1/embeddings`、`POST /v1/images/generations`、`POST /v1/audio/*` 按能力位路由。
- `POST /v1/messages` —— **Anthropic Messages 兼容入口**：原生接受 Claude 格式请求，既可转发到 Anthropic 渠道，也可把 Claude 格式转成 OpenAI 格式转发到 gpt/grok 渠道（这就是"反代理 Claude/ChatGPT/grok 让其聚合"）。
- `POST /v1/responses`（OpenAI 新格式，可选，给出适配层）。
- `GET /healthz`、`GET /metrics`（Prometheus）。

**F6 反代理与流式转发**：按 `model` 解析到候选渠道 → 选中渠道 → 重写 `Authorization`/`x-api-key`、剥离平台令牌、注入上游所需 header → 透传 body → 直连上游。流式场景必须：`Content-Type: text/event-stream`、`Cache-Control: no-cache`、`X-Accel-Buffering: no`，逐 `data:` 块 `Flush()` 不缓冲；正确处理 `[DONE]`、`: ping` 注释行、多行 SSE event（Anthropic）、上游中途报错帧；客户端断开时 `context` 取消并关闭上游连接；非流式直接转发 JSON 并统一错误包装。

**F7 协议适配器层（关键，必须给完整实现）**：定义接口
```go
type ProviderAdapter interface {
    ListModels(ctx, ch) ([]DiscoveredModel, error)
    BuildRequest(ch, *UnifiedRequest) (url string, headers http.Header, body []byte, error)
    ParseResponse(ch, *http.Response) (*UnifiedResponse, error)
    StreamIterator(ch, io.Reader) StreamIterator   // 产出统一 chunk
}
```
实现 4 个适配器：
- `OpenAICompatAdapter`：透传（覆盖 openai / xai / gemini-compat / 自定义），注意 grok 的 `reasoning_content`/`live_search` 差异字段保留透传。
- `AnthropicAdapter`：**双向转换**。OpenAI → Anthropic：`system` 抽出到顶层 `system`；`tools` 的 `function` 映射为 `input_schema`；`tool_calls` ↔ `tool_use` content block；`tool` role ↔ `tool_result`；`finish_reason` ↔ `stop_reason`（`stop/end_turn`、`length/max_tokens`、`tool_calls/tool_use`）；`usage.prompt_tokens/completion_tokens` ↔ `input_tokens/output_tokens`；`max_tokens` 必填。流式把 Anthropic 的 `message_start / content_block_start / content_block_delta(input_json_delta|text_delta) / content_block_stop / message_delta / message_stop / ping / error` 事件用状态机转成 OpenAI `chat.completion.chunk`（含 `delta.content`、`delta.tool_calls`、`finish_reason`、`usage`）。反向（`/v1/messages` 入口 → OpenAI 渠道）做对称转换。
- `GeminiAdapter`（走 OpenAI 兼容层，注明原生 `generateContent` 的可选分支）。
- `OpenAICompatibleAdapter` 的自定义路径/自定义 header 变体。
统一内部结构体 `UnifiedRequest/UnifiedResponse/UnifiedChunk`，所有适配器只与它打交道。

**F8 路由、负载均衡与故障转移**：`model_alias → []channel_models(active, healthy)` 候选集；选择器支持 `weighted_round_robin`（默认）、`least_in_flight`、`priority` 三种策略（渠道表可配）；在途计数用 Redis；上游返回 `429/5xx/超时/连接错误` 时自动标记渠道短时熔断（冷却 30s）并在 `MAX_FAILOVER=3` 次内切换下一候选；全部失败返回统一 502；定时健康检查（每 60s 用轻量 `/models` 请求刷新 `status`）。支持 `model` 带 `provider/` 前缀强制指定渠道类型。

**F9 令牌、配额与限流**：`POST /api/tokens` 生成 `sk-`，字段：`name, allowed_models[], rpm, tpm, max_concurrency, quota_usd, expires_at, ip_allowlist[]`。数据面中间件：校验令牌哈希（数据库只存 sha256）、过期、模型白名单、IP；Redis Lua 令牌桶实现 RPM/TPM/并发三道限流；请求结束按用量扣减余额；余额耗尽返回 `402 insufficient_quota`。贡献 `public` 渠道的用户，其渠道被他人成功调用时按 `contributor_reward_rate` 返还额度。

**F10 计量、日志与可观测**：异步队列写 `request_logs`（含完整输入输出，可配置脱敏开关）；用量看板按日/模型/渠道聚合 QPS、成功率、平均 TTFT、token 消耗、花费；结构化 JSON 日志 + `X-Request-Id` 全链路；`/metrics` 暴露 `llmhub_requests_total{model,channel,status}`、`llmhub_ttft_seconds`、`llmhub_tokens_total{type}`、`llmhub_channel_health`。

**F11 管理后台**：渠道 CRUD + 一键同步/探测 + 健康状态；模型池与别名映射编辑（合并/拆分/禁用/改名/定价覆盖）；用户与角色管理；令牌审计与吊销；仪表盘（QPS 曲线、成功率、Top 模型、渠道健康矩阵）；日志检索（按 request_id / 用户 / 模型 / 状态码 / 关键字）；系统设置（默认超时、failover 次数、同步 cron、奖励比例、注册开关、脱敏开关）。

**F12 用户前台**：注册/登录；「贡献渠道」表单（选供应商→自动填充 base_uri 占位提示→填 api_key→测试连接→提交）；「我的渠道」列表显示该渠道**被发现的实际模型、每个模型的参数卡片与真实输入输出样例**；「模型广场」展示全局聚合模型池（按供应商/能力/价格筛选）；「我的令牌」创建/复制/吊销，并自动给出可直接粘贴的 curl / Python(OpenAI SDK) / Anthropic SDK 调用示例（base_url 指向 `[DEPLOY_HOST]/v1`）；「用量」页看自己的调用明细与余额；贡献奖励流水。

**F13 安全**：上游 `api_key` AES-256-GCM 加密（`MASTER_KEY` 环境变量，32 字节 base64），日志与响应一律脱敏（只留 `****abcd`）；SSRF 防护：base_uri 白名单校验（拒绝 `127.0.0.0/8`、`10/8`、`172.16/12`、`192.168/16`、`169.254.169.254`、`::1`、非 80/443 的内网端口、file/gopher 协议），并解析 DNS 后二次校验；管理面 CSRF/JWT、密码强度、登录失败限流；CORS 可配；请求体大小上限；平台令牌只存哈希；不记录上游 key 到任何日志；提供 `SECURITY.md` 说明密钥轮换与备份。

**F14 部署**：`Dockerfile`（多阶段）、`docker-compose.yml`（app + postgres + redis + nginx）、Nginx 反代 SSE 专用配置、`Makefile`、`README.md`、`.env.example`、数据库迁移、种子数据（内置能力表与供应商常量）。

## 3. 技术栈

后端 **Go 1.22 + Gin + pgx/sqlx + go-redis + golang-migrate + tiktoken-go + Prometheus client_golang + zap**；前端 **Next.js 14(App Router) + TypeScript + Tailwind + shadcn/ui + Recharts + SWR**；存储 **PostgreSQL 16 + Redis 7**；测试 **testify + httptest + vitest**；部署 Docker Compose + Nginx。若改用 Node 20 + Fastify，需给出等价完整实现并说明理由。第三方依赖给出精确版本。

## 4. 数据库表（给出完整 DDL，含索引、外键、注释）

至少包含：
- `users(id, email UNIQUE, password_hash, role, balance_usd NUMERIC(18,6), status, created_at, updated_at)`
- `channels(id, owner_id→users, name, provider_type, base_uri, models_path, chat_path, auth_style, api_version, api_key_cipher BYTEA, api_key_iv BYTEA, api_key_tag BYTEA, key_hint, custom_headers JSONB, visibility, weight INT, priority INT, status, health_checked_at, last_error, fail_count, created_at, updated_at)`；索引 `(status, visibility)`、`(owner_id)`。
- `models(id, channel_id→channels, upstream_id, alias, display_name, provider_type, context_length INT, max_output_tokens INT, supports_stream BOOL, supports_vision BOOL, supports_tools BOOL, supports_reasoning BOOL, input_price_per_mtok NUMERIC, output_price_per_mtok NUMERIC, param_source, raw_json JSONB, probe_status, probe_input JSONB, probe_output TEXT, probe_prompt_tokens INT, probe_completion_tokens INT, probe_latency_ms INT, probed_at, enabled BOOL, UNIQUE(channel_id, upstream_id))`
- `model_aliases(alias PK, canonical_model_id, description, enabled, price_override JSONB, created_at)`
- `tokens(id, user_id→users, name, token_hash UNIQUE, key_hint, allowed_models TEXT[], rpm INT, tpm INT, max_concurrency INT, quota_usd NUMERIC, used_usd NUMERIC, ip_allowlist TEXT[], expires_at, status, last_used_at, created_at)`
- `request_logs(id BIGSERIAL, request_id UNIQUE, user_id, token_id, alias, model_id, channel_id, provider_type, stream BOOL, request_body JSONB, response_body TEXT, prompt_tokens INT, completion_tokens INT, ttft_ms INT, latency_ms INT, status_code INT, error_code, cost_usd NUMERIC, client_ip, created_at)`；索引 `(user_id, created_at DESC)`、`(alias, created_at DESC)`、`(request_id)`。
- `model_probes(id, model_id→models, input JSONB, output TEXT, latency_ms, ok, error, created_at)`
- `usage_daily(day DATE, user_id, alias, requests, prompt_tokens, completion_tokens, cost_usd, PRIMARY KEY(day,user_id,alias))`
- `quota_ledger(id, user_id, delta_usd, reason, ref_id, created_at)`（贡献奖励/充值/消费流水）
- `settings(key PK, value JSONB, updated_at)`
给出迁移文件 `migrations/0001_init.up.sql` 与 `.down.sql` 全文。

## 5. REST API 契约（每个端点列出 method / path / 鉴权 / 请求体 / 响应体 / 错误码）

管理面（JWT）：
```
POST   /api/auth/register            {email,password} -> {user,tokens}
POST   /api/auth/login               {email,password} -> {access_token,refresh_token}
POST   /api/auth/refresh             {refresh_token}
POST   /api/auth/logout
GET    /api/me
POST   /api/channels                 {name,provider_type,base_uri,api_key,visibility,weight,custom_headers,...} -> {id,key_hint,models_discovered}
GET    /api/channels                 ?page&status
GET    /api/channels/:id
PUT    /api/channels/:id
DELETE /api/channels/:id
POST   /api/channels/:id/test        -> {ok,latency_ms,error}
POST   /api/channels/:id/sync-models -> {added,updated,removed}
POST   /api/channels/:id/probe       -> {probed,failed}
GET    /api/models                   ?provider&capability&q -> 聚合池（含参数+输入输出样例）
GET    /api/models/:id
PUT    /api/models/:id               {enabled,alias,price_override,...}
POST   /api/models/:id/probe
GET    /api/aliases / POST /api/aliases / PUT /api/aliases/:alias
POST   /api/tokens                   {name,allowed_models,rpm,tpm,quota_usd,expires_at} -> {token:"sk-..."}（仅此一次明文返回）
GET    /api/tokens / DELETE /api/tokens/:id
GET    /api/usage                    ?from&to&group_by
GET    /api/logs                     ?request_id&alias&status&q&page
GET    /api/admin/dashboard
GET    /api/admin/users / PUT /api/admin/users/:id
GET/PUT /api/admin/settings
```
数据面（Bearer sk-）：`/v1/models`、`/v1/chat/completions`、`/v1/completions`、`/v1/embeddings`、`/v1/images/generations`、`/v1/messages`、`/v1/responses`、`/healthz`、`/metrics`。
统一错误体：`{"error":{"message":"...","type":"invalid_request_error|rate_limit_error|upstream_error|insufficient_quota","code":"...","request_id":"..."}}`，HTTP 状态 400/401/402/403/404/429/500/502/504 逐一对应。

## 6. 关键实现细节（要求给出真实完整代码，不是伪代码）

1. `internal/adapter/`：`adapter.go`(接口+统一结构体+注册工厂)、`openai_compat.go`、`anthropic.go`（**含完整 OpenAI↔Anthropic 请求/响应/流式的双向转换函数与状态机**）、`gemini.go`、`convert_test.go`（覆盖 system/tools/tool_calls/usage/stop_reason/流式事件序列的单元测试）。
2. `internal/relay/stream.go`：SSE 逐行扫描、`Flush`、`context` 取消、`[DONE]`、Anthropic 多行 event 合并、错误帧注入、TTFT 打点。
3. `internal/discovery/`：模型发现 + 参数补全能力表（把第 1 节的 grok/Claude/ChatGPT/Gemini 常用模型能力写成常量表）+ 探测任务 + cron。
4. `internal/router/selector.go`：加权轮询 / 最少在途 / 优先级 + 熔断 + failover 循环。
5. `internal/crypto/`：AES-256-GCM 加解密 + key_hint + 日志脱敏 middleware。
6. `internal/middleware/`：JWT、平台令牌校验、Redis Lua 令牌桶限流、request_id、CORS、body limit、SSRF 校验器（DNS 解析二次校验）。
7. `internal/metering/`：上游 usage 优先 + tiktoken 兜底 + 异步批量写日志 + 计费与贡献奖励返还。
8. `internal/config/` + `.env.example`：每个变量都有注释与默认值。
9. 前端 `app/` 下所有页面组件、API client、类型定义、Tailwind 配置、`Dockerfile`、`next.config.js`。

## 7. 目录结构（输出完整树）

```
llm-hub/
├── cmd/server/main.go
├── internal/{config,db,cache,auth,crypto,middleware,adapter,discovery,router,relay,metering,model,channel,token,log,admin,health}/
├── api/{openai.go,anthropic.go,admin.go,router.go,dto/}
├── migrations/0001_init.{up,down}.sql
├── web/{app/,components/,lib/,types/,public/,package.json,pnpm-lock说明,tailwind.config.ts,next.config.js,Dockerfile}
├── deploy/{nginx.conf,docker-compose.yml,Dockerfile,Makefile}
├── .env.example
├── go.mod / go.sum
├── README.md
├── SECURITY.md
└── tests/{relay_test.go,adapter_test.go,e2e.sh}
```

## 8. 环境变量 `.env.example`（全文 + 注释）

```
SERVER_PORT=8080
SERVER_BASE_URL=https://[DEPLOY_HOST]
DATABASE_URL=postgres://llmhub:llmhub@postgres:5432/llmhub?sslmode=disable
REDIS_URL=redis://redis:6379/0
JWT_SECRET=[JWT_SECRET]
JWT_TTL_MIN=1440
MASTER_KEY=[32字节base64_AES256密钥]      # 加密上游 api_key，丢失则无法解密
DEFAULT_TIMEOUT_SEC=180
MAX_FAILOVER=3
CIRCUIT_COOLDOWN_SEC=30
MODEL_SYNC_CRON=0 */6 * * *
HEALTH_CHECK_INTERVAL_SEC=60
PROBE_ENABLED=true
PROBE_MAX_TOKENS=16
LOG_FULL_BODY=true                          # 记录模型完整输入输出
LOG_MASK_KEYS=true
CONTRIBUTOR_REWARD_RATE=0.15
REGISTRATION_OPEN=true
CORS_ORIGINS=*
ADMIN_EMAIL=[ADMIN_EMAIL]
ADMIN_PASSWORD=[ADMIN_PASSWORD]
RATE_RPM_DEFAULT=60
RATE_TPM_DEFAULT=200000
BODY_LIMIT_MB=20
```

## 9. `docker-compose.yml` 与 Nginx SSE 配置要点（给全文）

Nginx 必须包含：
```
location /v1/ {
    proxy_pass http://app:8080;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 600s;
    proxy_send_timeout 600s;
    chunked_transfer_encoding on;
    add_header X-Accel-Buffering no;
}
```
compose 含 `app`(Go)、`web`(Next)、`postgres`(带 healthcheck + volume)、`redis`、`nginx`(80/443)，`depends_on` 用 `service_healthy`。

## 10. 前端页面清单

`/login`、`/register`、`/dashboard`(概览)、`/channels`(列表+新增表单+测试连接按钮)、`/channels/[id]`(渠道详情：已发现的模型表 + 每个模型的参数卡片 + **真实输入样例/真实输出样例/实测延迟** + 重新同步/重新探测按钮)、`/models`(全局模型广场，按供应商 grok/Claude/ChatGPT/Gemini 与能力筛选)、`/models/[id]`、`/tokens`(创建后弹窗一次性显示 sk- + 自动生成的 curl/Python/Anthropic SDK 示例)、`/usage`(图表+明细)、`/logs`、`/admin/{channels,models,users,settings,dashboard}`。给出每个页面的关键交互与所用组件，代码完整。

## 11. 验收标准与可执行测试用例（给出 `tests/e2e.sh` 全文 curl 脚本）

1. 注册 admin → 登录拿 JWT。
2. 贡献 grok 渠道：`POST /api/channels {"provider_type":"xai","base_uri":"https://api.x.ai/v1","api_key":"[XAI_KEY]","visibility":"public"}` → 响应 `models_discovered>0`，`GET /api/models?provider=xai` 能看到 `grok-3` 等真实模型及参数、且详情页有真实输入输出样例。
3. 贡献 Claude 渠道：`provider_type=anthropic, base_uri=https://api.anthropic.com/v1, api_key=[ANTHROPIC_KEY]` → 自动发现 `claude-*`；探测成功。
4. 贡献 ChatGPT 渠道：`provider_type=openai, base_uri=https://api.openai.com/v1, api_key=[OPENAI_KEY]` → 自动发现 `gpt-4o` 等。
5. 创建令牌 `sk-...` → `GET /v1/models -H "Authorization: Bearer sk-..."` 返回**三家模型合并在同一个列表**。
6. `curl -N [DEPLOY_HOST]/v1/chat/completions -d '{"model":"grok-3","messages":[{"role":"user","content":"hi"}],"stream":true}'` → 逐块 SSE 输出、末行 `data: [DONE]`、首字延迟记录进日志。
7. 用**同一个 sk-** 依次调用 `gpt-4o`、`claude-3-7-sonnet`、`grok-3`，三者都 200；`GET /api/logs` 能看到每次的完整输入/输出与 token 数。
8. `POST /v1/messages`（Anthropic 原生格式）指向 `model=claude-3-7-sonnet` 成功；再指向 `model=gpt-4o` 也成功（跨协议适配生效）。
9. 把某渠道 `status=disabled` 或填错 key → 请求自动 failover 到另一渠道仍 200；全部不可用返回统一 502 + `upstream_error`。
10. 直接查库：`SELECT api_key_cipher FROM channels` 为密文，`grep -r [OPENAI_KEY] logs/` 无结果。
11. RPM 超限返回 429；额度耗尽返回 402；无效令牌 401；越权访问他人渠道 403。
12. `go test ./...` 与 `pnpm test` 全绿；`docker compose up -d` 后 `/healthz` 返回 200。

## 12. 输出要求（最后一段，必须遵守）

- 按第 7 节目录树**逐个文件**输出完整内容，每个代码块首行标注 `// 文件: 路径` 或 ` ```go:cmd/server/main.go ` 形式；不许跳过任何文件、不许合并、不许写「同前」。
- 所有 SQL、Go、TypeScript、YAML、Nginx 配置、shell 脚本、README 均为可直接运行的完整版本。
- `README.md` 必须含：架构图（mermaid）、5 分钟快速启动（`cp .env.example .env && docker compose up -d && curl ...`）、环境变量说明、贡献渠道步骤、统一调用示例（curl / OpenAI SDK / Anthropic SDK）、常见问题（SSE 不流式、上游 429、密钥轮换、如何新增一个供应商适配器）。
- 最后附一段「新增供应商适配器的最小改动清单」，说明如何把任意新的 base_uri + api_key 类型接入。
- 全程使用中文注释与文档；代码标识符、命令、日志保持英文原样。
- 输出从第一个文件开始，到 README 最后一行的 curl 命令结束，中间不插入任何总结、不询问、不提示「如需继续」。
