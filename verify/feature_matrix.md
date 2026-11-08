# verify/feature_matrix.md —— F1–F14 逐项功能核实矩阵（终查定稿）

> 团队 8 · 功能审查核实 · 唯一写入范围 `verify/**`、`docs/08-功能核实报告.md`
> 终查时间：2026-11-07 22:0x +0800
> 硬证据全文：`docs/08-功能核实报告.md`（§2 逐字输出、§3 C-1、§4 本表展开、§9 报告生成后复核）
> 判定口径（SPEC.md C-2 / 团队 8 硬约束）：
> - **已实现**：有真实代码/文件证据 **且** 有真实命令执行输出支撑。
> - **部分**：有代码/文件证据，但对应端到端成功路径缺少真实运行验证，或存在明确验证缺口。
> - **未实现**：无对应文件/代码，或运行时验证真实失败。
> - **禁止在无证据时标记「已实现」**；所有输出逐字来自真实执行。

## 0. 终查状态

| 项 | 值 |
|---|---|
| 需求来源 | `LLM-API-Aggregator-Prompt.md` §2 F1–F14、`SPEC.md` §1 C-2 |
| 核实脚本 | `verify/check_real_data.py`（v5）、`verify/e2e.sh`（双令牌 + 双键名设计） |
| 数据面基线 | `data/models-real.json`（460 模型 / 69 供应商 / SHA-256 `08b4635f…876a`） |
| 复查次数 | 11 次真实 `glob`/`find` 观测，直至 `src/cmd/server/main.go`、kernel、tests、docs/01~08、patches 全部齐全 |
| 任务状态 | task-1~7 全部 completed；task-8 blockedBy 解除 |
| 终查结果 | **已实现 10 / 部分 4 / 未实现 0** |
| 最终结论 | **有未完成项**（3 类受限验证缺口，非功能缺失；详见 `docs/08 §8`） |

复查记录（真实观测，逐次追加）：

| # | 时间 | 观测到的新增文件/目录 | task-6 / task-7 |
|---|---|---|---|
| 1 | 20:58 | 仅 `SPEC.md`、`LLM-API-Aggregator-Prompt.md`、`data/*`、`go.mod`、`kernel/contract.go` | pending / pending |
| 2 | 21:0x | `README.md`、`docs/01`、`src/internal/{config,crypto,models,ssrf}`、数据集副本 | pending / pending |
| 3 | 21:08 | `docs/03`、`docs/07`、`src/internal/{adapter,auth,rt,store}`、`web/**`、`Makefile`、`deploy/**` | pending / pending |
| 4 | 21:19 | `src/internal/{discovery,channel}`、更多 `web/components` | pending / pending |
| 5 | 21:26 | `src/internal/api/server.go`（完整路由表）出现，瞬时 build 失败（handler 未齐） | pending / pending |
| 6 | 21:30 | `src/cmd/server/main.go` 出现，`go build ./...` exit 0 | pending / pending |
| 7 | 21:37 | `kernel/**`(15)、`patches/**`、`tests/**`、`docs/05` 齐全；`channel.go` 补齐 13 个 json 标签 | pending / pending |
| 8 | 21:41 | `docs/04`、`web/app/**` 页面齐备；`go test ./...` 一度因 crypto_test 失败 | pending / pending |
| 9 | 21:51 | `docs/06-代码审核与测试.md` 出现；docs/01~07 全部非空 | pending / pending |
| 10 | 21:53 | 团队 6 修 `MASTER_KEY` 空值测试；`go test ./...` exit=0、41 个 `--- PASS` | completed / completed |
| 11 | 22:0x | Lead 真实应用 17 个补丁并全量复验通过；`docs/08` 落盘 | completed / completed |

---

## 1. F1–F14 终查汇总表

| F | 功能 | 判定 | 证据路径 | 核实命令与真实输出 |
|---|---|---|---|---|
| F1 | 用户与鉴权 | **已实现** | `src/internal/auth/auth.go:31/44/86/141`、`src/internal/crypto/crypto.go:125/131/140/166`、`src/internal/api/auth_api.go`、`src/internal/api/server.go:152-154,168-175` | `bash verify/e2e.sh` S2 `/api/me` 无效令牌=401、S3 register/login=200 `JWT_ACQUIRED True`；`TestAPIAuthAndManagement` PASS |
| F2 | 渠道贡献与管理 | **已实现** | `src/internal/channel/channel.go:22-33`（13 字段 json 标签）、`src/internal/api/channels_api.go`、`server.go:177-190`、`src/internal/crypto/crypto.go:37/102/114`、`src/internal/ssrf/ssrf.go` | e2e S8 四条 `POST /api/channels`=400 且真实 SSRF 拒绝消息；Lead [A-fix]/[C]；`TestAPIChannelFieldContractAndSSRF` PASS |
| F3 | 真实模型参数发现 | **已实现** | `src/internal/models/models.go:91/137/146/170`、`src/internal/adapter/adapters.go:15/78`、`src/internal/discovery/discovery.go`、`src/internal/models/models-real.json`（SHA 与真值一致） | `python3 verify/check_real_data.py` [3] 460/460 id + 4131 次字段比对 0 不一致；e2e S4 `/v1/models`=200 `RETURNED_MODELS=460 / NOT_IN_REAL=0 / PASS` |
| F4 | 真实模型输入/输出采集 | **部分** | `src/internal/discovery/discovery.go:162-209`、`src/internal/api/server.go:184,190`、`src/internal/store/store.go`、`web/components/ProbeSampleCard.tsx` | Lead [C] 失败上游 → `probe_status:"unavailable"`；`tests/discovery_test.go` PASS。成功探测真实输入/输出样例无上游凭据不可端到端验证 |
| F5 | 统一数据面 API | **已实现** | `src/internal/api/server.go:150-210`、`dataplane.go`、`metrics.go`、`src/cmd/server/main.go` | e2e：`/healthz`=200、`/metrics`=200、`/v1/models`=200、chat=502 `upstream_error`、messages=502 `upstream_error`；统一错误体含 `request_id` |
| F6 | 反代理与流式 SSE | **部分** | `src/internal/adapter/stream.go:20/27/96/285/366`、`src/internal/rt/streampipe.go:20`、`kernel/streampipe.go`、`src/internal/relay/relay.go` | `go test -bench . -benchmem ./kernel/...` → `BenchmarkStreamPipe-8 … 303.67 MB/s`；`TestAdapterAnthropicStreamStateMachine` / `TestAdapterOpenAIStreamAndAnthropicWriter` PASS。真实上游 SSE 端到端无凭据不可验证 |
| F7 | OpenAI↔Anthropic 双向适配 | **已实现** | `src/internal/adapter/convert.go:12/29/43/50/79/127/372/523/585/644`、`adapter.go:23/39/144`、`tests/adapter_test.go` | `go test ./tests/... -count=1 -v` 9 个 `TestAdapter*` 全 PASS（41 个 PASS 内）；`go test ./... -count=1` exit=0 |
| F8 | 路由负载均衡与 failover | **部分** | `src/internal/rt/selector.go:41`、`kernel/selector.go`、`src/internal/router/router.go`、`kernel/scheduler.go`、`server.go:53-56` | `go test ./...` kernel PASS、`BenchmarkScheduler-8`；`TestAPIQuota402AndRateLimit429` PASS。多上游真实 failover 无凭据不可端到端验证 |
| F9 | 令牌配额限流 | **已实现** | `src/internal/api/tokens_api.go:28-58`、`src/internal/middleware/middleware.go:211-340`、`src/internal/rt/limiter.go` | e2e S3 建令牌=200（`key_hint:"****3866"` 脱敏）；S7 `RPM_REQ_1..2=200 / 3..8=429`、`HTTP_429_COUNT=6 / PASS`；早期真实观察到 402 `insufficient_quota` |
| F10 | 计量日志可观测 | **已实现** | `src/internal/metering/metering.go`、`src/internal/store/store.go:833`、`src/internal/api/metrics.go`、`src/internal/middleware/middleware.go`（RequestID）、`src/internal/health/health.go` | e2e S2 `/metrics`=200 含 `llmhub_requests_total` / `llmhub_ttft_seconds` / `llmhub_tokens_total`；`/healthz` JSON；错误体含 `request_id`；`TestAPIHealthzAndMetrics` PASS |
| F11 | 管理后台 | **已实现** | `web/app/admin/**`、`web/components/AdminModelTable.tsx`、`AdminRuntime.tsx`、`docs/04-UI设计规范.md:408-475`（§10.8）、`web/.next/BUILD_ID`、`src/internal/api/admin_api.go`、`server.go:203-210` | 团队 4 真实 `npm run build` → `✓ Compiled successfully` / `✓ Generating static pages (17/17)` / `BUILD_EXIT=0`；`./node_modules/.bin/tsc --noEmit -p tsconfig.json` → `TSC_EXIT=0 / TSC_ERRORS=0`；`node scripts/check-syntax.mjs` → `files=50 errors=0 / PASS`；`node scripts/verify-models.mjs` → `PASS: 0 errors`；`web/.next/BUILD_ID=g5Jzf4R_JMpb037dvfaSk` 真实存在；完整性检查 `web/` 10107 文件；`TestAPIAuthAndManagement` PASS |
| F12 | 用户前台 | **已实现** | `web/app/{page,login,register,dashboard,channels,models,tokens,usage,logs}/**`、`web/components/*`、`web/lib/models.ts`、`web/data/models.json`、`docs/04-UI设计规范.md §10` | checker [3] `web/data/models.json` 460/460 真实、字段 0 不一致；checker [5] `web/lib/models.ts:34/36/37/45/49/50` 全部 REAL；e2e S4；团队 4 前端构建真实通过（`npm run build` ✓ / 17/17 / `BUILD_EXIT=0`；`tsc` `TSC_EXIT=0`；`check-syntax` `files=50 errors=0`；`verify-models` `PASS: 0 errors`）；`web/.next/BUILD_ID=g5Jzf4R_JMpb037dvfaSk` |
| F13 | 安全（AES-256-GCM + SSRF） | **已实现** | `src/internal/crypto/crypto.go:31/37/102/111/114/125/131`、`src/internal/ssrf/ssrf.go:19/22/78`、`src/internal/middleware/middleware.go`、`docs/07-漏洞审查与修复.md`、`patches/`(18) | e2e S2 401；S8 4/4 400 且真实 SSRF 拒绝消息（loopback / link-local / `file` 协议 / RFC1918）；S3 `key_hint` 脱敏；Lead [A-fix]；`go test ./tests/...` 全 PASS |
| F14 | 部署 | **部分** | `Makefile`、`deploy/{Dockerfile,docker-compose.yml,nginx.conf,.env.example}`、`README.md`、`src/cmd/server/main.go`、`docs/06-代码审核与测试.md` | 完整性检查各文件非空；`go build ./...` exit=0；e2e 真实 `go build -o` + 后台启动 + `kill` 清理成功。未执行 `docker compose up -d` |

---

## 2. F1–F14 逐项核实明细（终查）

每项均给出真实文件:行号与真实命令输出，全文逐字输出见 `docs/08-功能核实报告.md §2`。

**F1 用户与鉴权（已实现）** — `auth.go` `SignToken`(L31)/`VerifyToken`(L44)/`NewService`(L86)，`crypto.go` `HashPassword`(L140)/`VerifyPassword`(L166)/`TokenHash`(L125)/`ConstantTimeEqual`(L131)；`server.go` 注册 `POST /api/auth/{register,login,refresh}` 与 `/api/me`。真实输出：`/api/me` 无效令牌 → 401 `{"error":{"message":"JWT 格式错误","type":"invalid_request_error","code":"invalid_token","request_id":"..."}}`；register/login 200、`JWT_ACQUIRED True (len=217)`。单元 `TestAPIAuthAndManagement` PASS。

**F2 渠道贡献与管理（已实现）** — `channel.Input` 13 字段（L22-33）全带 json 标签（团队 6 于 21:37:15 修复，修复前 snake_case 被静默忽略）；`channels_api.go` 提供 CRUD/test/sync/probe/status；`crypto.NewCipher`/`KeyHint`/`Redact` 保证 key 只密文落库、只回 `key_hint`。真实输出：e2e S8 四条内网 `base_uri` 全 400 且响应体为 `base_uri 校验失败: 拒绝内网/回环/元数据地址 …`；Lead [A-fix] 与 [C] 交叉确证。

**F3 真实模型参数发现（已实现）** — `models.go` `Load`(L91)/`IDSet`(L137)/`OpenAIList`(L146)/`FindRealModelsFile`(L170)，`adapters.go` `discoverParams`(L15)/`parseModelsList`(L78)，`discovery.go`；`src/internal/models/models-real.json` 与真值 SHA-256 逐字节一致。真实输出：checker [3] 460/460 id、4131 次字段比对 0 不一致；e2e S4 `/v1/models`=200 `TRUTH_MODELS=460 / RETURNED_MODELS=460 / FIRST_3_IDS=['liquid/lfm-3-1.7b', 'liquid/lfm-3-1.7b:free', 'google/gemini-omni-1.2-flash'] / NOT_IN_REAL=0 / VERDICT PASS`；Lead [C] 上游失败时 `models_discovered:0` + `probe_status:"unavailable"`，未编造模型。

**F4 真实模型输入/输出采集（部分）** — `discovery.go:162-209` 有 `ProbeResult`/`ProbeModel`/`recordFailedProbe`，失败标 `probe_status=failed/unavailable` 且不写编造输出；`server.go:184,190` 提供重探端点；`ProbeSampleCard.tsx` 展示真实样例。真实输出：Lead [C] 失败路径 `probe_status:"unavailable"`；`tests/discovery_test.go` PASS。**缺口**：无真实上游凭据，成功探测的真实输入/输出样例无端到端输出。

**F5 统一数据面 API（已实现）** — `server.go:150-210` 完整路由表（`/v1/models`、`/v1/chat/completions`、`/v1/completions`、`/v1/embeddings`、`/v1/images/generations`、`/v1/audio/*`、`/v1/messages`、`/v1/responses`、`/healthz`、`/metrics`）。真实输出：e2e S2/S4/S5/S6 状态码 200/200/200/401/200/502/502；统一错误体 `{"error":{"message","type","code","request_id"}}`。

**F6 反代理与流式 SSE（部分）** — `stream.go` SSE 扫描/`[DONE]`/Anthropic 多行 event/Flush 状态机、`streampipe.go` 零拷贝背压、`kernel/streampipe.go`。真实输出：`BenchmarkStreamPipe-8 … 303.67 MB/s`、`TestAdapterAnthropicStreamStateMachine` / `TestAdapterOpenAIStreamAndAnthropicWriter` / `tests/relay_test.go` PASS。**缺口**：真实上游 SSE 逐块转发无凭据不可验证。

**F7 协议适配器（已实现）** — `convert.go`（20,002 B）含 `OpenAIToAnthropicRequest`(L127)、`AnthropicToOpenAIRequest`(L372)、`AnthropicToOpenAIResponse`(L523)、`OpenAIToAnthropicResponse`(L585)、tools/usage/stop_reason/finish_reason 双向映射、流式状态机。真实输出：`go test ./tests/... -count=1 -v` 9 个 `TestAdapter*` 全部 `--- PASS`（41 PASS 内）。

**F8 路由负载均衡与 failover（部分）** — `rt/selector.go:41` `NewLoadBalancer`、`router/router.go`、`kernel/{scheduler,selector}.go`，`server.go:53-56` 注入 4 个 kernel 接口。真实输出：`go test ./...` kernel PASS、`BenchmarkScheduler-8`、`TestAPIQuota402AndRateLimit429` PASS。**缺口**：多上游 429/5xx 自动 failover 无凭据不可端到端验证。

**F9 令牌配额限流（已实现）** — `tokens_api.go:28-58` 生成 `sk-`（`token_hash` 落库、`key_hint` 脱敏、仅一次明文返回）；`middleware.go:211-340` RPM/TPM/并发三道 + `402 insufficient_quota`。真实输出：e2e S7 `RPM_REQ_1 : HTTP 200`、`RPM_REQ_2 : HTTP 200`、`RPM_REQ_3..8 : HTTP 429`、`HTTP_429_COUNT : 6`、`VERDICT : PASS(观察到 429)`。

**F10 计量日志可观测（已实现）** — `metering.go`、`store.go:833` `usageKey`、`metrics.go`、`middleware.go` `RequestID`、`health.go`。真实输出：`GET /metrics`=200，响应体含 `# HELP llmhub_requests_total`、`# TYPE llmhub_ttft_seconds summary`、`llmhub_tokens_total`；`/healthz` JSON `{"channels":0,"discovered_models":0,"real_models":460,"real_models_source":"data/models-real.json","status":"ok",...}`。**限制**：`request_logs` 完整落库内容未逐条导出核对（§4 未完成项 3）。

**F11 管理后台（已实现）** — `web/app/admin/**`（channels/models/users/settings/dashboard）、`AdminModelTable.tsx`、`AdminRuntime.tsx`、`docs/04-UI设计规范.md §10.8`（L408-475）、`web/.next/BUILD_ID`、`admin_api.go`、`server.go:203-210`。真实输出（团队 4 逐字贴于 `docs/04 §10`）：`npm run build` → `✓ Compiled successfully` / `✓ Generating static pages (17/17)` / `BUILD_EXIT=0`；`./node_modules/.bin/tsc --noEmit -p tsconfig.json` → `TSC_EXIT=0` / `TSC_ERRORS=0`；`node scripts/check-syntax.mjs` → `[syntax] files=50 errors=0 / PASS`；`node scripts/verify-models.mjs` → `[verify] PASS: 0 errors`；本人 `cat web/.next/BUILD_ID` → `g5Jzf4R_JMpb037dvfaSk`。完整性检查 `web/` 10107 文件；`TestAPIAuthAndManagement` PASS。前端构建已真实通过，不再列为缺口。

**F12 用户前台（已实现）** — `web/app/**` 全页面、`web/components/*`、`web/lib/models.ts`（从 `web/data/models.json` 读取真实数据）、`web/data/models.json`（460/460 真实）、`docs/04 §10`。真实输出：checker [3] 字段 0 不一致、[5] `web/lib/models.ts` 6 个 featured/routing/price id 全部 REAL；e2e S4；团队 4 前端构建真实通过（同上 `npm run build` ✓ / 17/17 / `BUILD_EXIT=0`、`tsc` 0 错误、`check-syntax` 50 文件 0 错误、`verify-models` 0 错误）；`web/.next/BUILD_ID=g5Jzf4R_JMpb037dvfaSk`。前端构建已真实通过，不再列为缺口。

**F13 安全（已实现）** — `crypto.go` AES-256-GCM `NewCipher`(L37 拒绝空 MASTER_KEY)/`KeyHint`(L102)/`Redact`(L114)/`TokenHash`(L125)；`ssrf.go` `Validate`(L19)/`ValidateWithResolver`(L22)/`isBlocked`(L78)；`middleware.go` JWT/sk-/BodyLimit/CORS/IP allowlist；`docs/07`(60666 B) + `patches/`(18 个补丁，Lead 已真实应用)。真实输出：e2e S8 四条拒绝消息分别覆盖 127/8 loopback、169.254/16 link-local、`file` 协议、RFC1918 10/8；S2 无效令牌 401；S3 `key_hint` 脱敏。

**F14 部署（部分）** — `Makefile`、`deploy/{Dockerfile,docker-compose.yml,nginx.conf,.env.example}`、`README.md`、`main.go`、`docs/06`。真实输出：各文件非空、`go build ./...` exit=0、e2e 真实 `go build -o` + 后台启动 + `kill` 清理。**缺口**：未执行 `docker compose up -d`。

---

## 3. 真实数据合规专项（C-1，终查结论）

由 `python3 verify/check_real_data.py` v5 真实执行（最终 154 个交付文本文件，含本报告自身）。

| 检查项 | 真实结论 |
|---|---|
| `data/models-real.json` | 460 模型 / 69 供应商 / SHA-256 `08b4635fd684689feda841f906d59e0dcd8ad9dd57a7c0b7f0667d46b740876a`，与 SPEC.md C-1 三项声明一致 `True` |
| `data/models-real-summary.json` / `.md` | 460 条 / 30 条模型语境 id，`不在真值集合: 0`；路径字面量 `data/models-real.json` 已排除 |
| `src/internal/models/models-real.json` | 460/460 id ∈ 真值；SHA-256 与真值逐字节一致 `True`；4131 次字段比对 0 不一致 |
| `web/data/models.json` | 460/460 id ∈ 真值；4131 次字段比对 0 不一致；SHA 不同系导出字段子集 |
| 模型语境候选 | 36~40 种真实命中，全部 ∈ 真值；上游元数据 slug 归 KNOWN_METADATA |
| **可疑编造项** | **`suspicious=0` / `fabricated_in_deliverables=0`；[6] 节 `(无 —— 可疑编造项 = 0)`** |
| 负例/夹具 | 最终 `negative_fixtures=0`（早期 v4 的 1 个负例探针随 docs/03 重写消失，已保留当时文件:行号证据） |
| 零对应短名 | 4 个（`claude-3-5-haiku`、`claude-3-7-sonnet`、`grok-3`、`grok-3-mini`），均在「禁止写入清单」/grep 负例/校验脚本语境，非静态真实模型数据 |
| 不编造输出 | e2e S5/S6 = 502 `upstream_error`，`FABRICATED_OUTPUT=False`；Lead [C] 上游失败 `models_discovered:0` + `probe_status:"unavailable"` |

**C-1 结论：通过，可疑编造项 = 0。**

---

## 4. 未完成项清单（终查，逐条列出）

1. **F4 / F6 / F8 端到端成功路径未真实验证**：本机无真实上游 `base_uri` + `api_key`，故成功探测采集真实输入/输出（F4）、真实上游流式 SSE 逐块转发（F6）、多上游自动 failover（F8）只有代码/单测/基准与失败路径证据，标「部分」。
2. **【已消解，保留记录】F11 / F12 前端构建**：团队 4 已真实执行 `npm run build`（`✓ Compiled successfully` / `✓ Generating static pages (17/17)` / `BUILD_EXIT=0`）、`tsc --noEmit`（`TSC_EXIT=0 / TSC_ERRORS=0`）、`node scripts/check-syntax.mjs`（`files=50 errors=0 / PASS`）、`node scripts/verify-models.mjs`（`PASS: 0 errors`），逐字输出见 `docs/04-UI设计规范.md §10`（L408-475）；工作区 `web/.next/BUILD_ID=g5Jzf4R_JMpb037dvfaSk` 真实存在。Lead 复核后 F11/F12 由「部分」升级为「已实现」，本项不再计为未完成。
3. **F14 未执行 `docker compose up -d`**：部署文件齐备、二进制真实启动成功，但容器编排未本机运行验证，标「部分」。
4. **F10 `request_logs` 完整落库内容未逐条导出核对**：`/metrics`、`X-Request-Id`、计量代码与单测均已核实。

> 以上 3 类为验证覆盖缺口（第 2 类已消解），非功能缺失、非伪造数据、非编译/测试失败。补齐真实上游凭据与容器构建环境后，可按本表命令直接复验升级为「已实现」。

---

## 5. 已修复/已消解问题（保留真实证据）

1. checker v1 369 个路径/元数据误报 → v2/v3/v5 收紧规则，最终 `suspicious=0`（Lead 复核意见全部落实）。
2. `data/models-real-summary.md` 路径字面量误判 → 仅保留真实供应商前缀 token，最终 `不在真值集合: 0`。
3. 早期负例探针 token → R6 归类 information-only 并给行号；docs/03 重写后 `negative_fixtures=0`。
4. 新用户 `balance_usd:0` 导致 `/v1/*` 全 402 → 团队 6 修为 `defaultBalanceUSD=50.0`。
5. `channel.Input` 缺 json 标签 → 团队 6 补齐 13 个标签；S8 由 CHECK 改判 PASS（修复前后输出见 `docs/08 §5`）。
6. S6 被前序请求限流 429 → e2e.sh 拆双令牌；修复后 502 `upstream_error` PASS。
7. `go test ./...` 9 用例失败 + nil `*Cipher` panic → 团队 6 注入固定 32 字节 base64 `testMasterKey()`；当前 exit=0、41 PASS。
8. 服务启动需显式 `MASTER_KEY` → `verify/e2e.sh:41` 计算、`:116` 注入（本人所有 e2e 运行均成功启动）；`docs/06 §4.7` 已给部署提示。
9. **F11 / F12 由「部分」升级为「已实现」**：本人曾误判「未执行 next build」，经 Lead 指出后复核 `docs/04-UI设计规范.md §10.8` 的真实 `npm run build`/`tsc --noEmit` 输出并 `cat web/.next/BUILD_ID`（= `g5Jzf4R_JMpb037dvfaSk`）确证前端构建真实通过；`docs/08 §4/§8` 与本矩阵 §1/§2/§4 已同步修正，统计由「已实现 8 / 部分 6」改为「已实现 10 / 部分 4」，未完成项由 4 类减为 3 类。
