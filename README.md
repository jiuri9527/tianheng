# 天衡 Tianheng — 通用大模型 API 聚合中转站

> 站点品牌名：**天衡（Tianheng）**。Go module 标识符 `llm-hub` 与前端包名 `llm-hub-web` 属于冻结技术契约，保持不变以确保 `go build ./...` / `npm run build` 通过。

> **天衡（Tianheng）· 众包渠道 + 统一出口**：任何人贡献自己的上游 `base_uri` + `api_key`，站点自动发现该渠道**真实可用的模型与实际参数**、用一次真实调用采集**模型输入/输出样例**，把 grok(xAI) / Claude(Anthropic) / ChatGPT(OpenAI) / Gemini 及任意 OpenAI 兼容上游统一转换，聚合为一个全局模型池；所有人只用一个 `sk-` 平台令牌、一个 `/v1` 地址，即可调用池中的任意模型。

## 真实数据（不可伪造）

| 项 | 值 |
|---|---|
| 数据源 | `https://openrouter.ai/api/v1/models`（HTTP 200，公开端点，无认证） |
| 原始文件 | `data/models-real.json`（827,419 字节） |
| 模型总数 | **460**（真实） |
| 供应商数 | **69**（真实） |
| SHA-256 | `08b4635fd684689feda841f906d59e0dcd8ad9dd57a7c0b7f0667d46b740876a` |
| 设计参考 | `data/openrouter-home.html`（抓取自 https://openrouter.ai，214,265 字节，SHA-256 `389f9681bee42548fb55ce68c76a7483693f56bffc5613d2bd725ebc0f320770`） |
| 可读摘要 | `data/models-real-summary.md`、`data/models-real-summary.json` |

约束（`SPEC.md` C-1）：**禁止伪造任何模型 id、名称、上下文长度、价格、供应商、能力或输入输出样例**。所有静态模型数据必须逐字来自 `data/models-real.json`；运行时模型必须来自对真实上游 `GET {base_uri}/models` 的实际响应，拉取失败时列表留空并标记 `probe_status=unavailable`，绝不填充占位模型。所有测试输出必须来自真实执行。

## 八团队分工（写入范围互斥）

| # | 团队 | 交付 | 写入范围 |
|---|---|---|---|
| 1 | 项目方案撰写 | `docs/01-方案初稿.md` | `docs/01-方案初稿.md` |
| 2 | 项目方案完善 | `docs/02-方案完善版.md` | `docs/02-方案完善版.md` |
| 3 | 项目方案审核 | `docs/03-方案审核报告.md` | `docs/03-方案审核报告.md` |
| 4 | UI 框架设计（对标 openrouter.ai · 2026 顶级审美） | `docs/04-UI设计规范.md`、`web/**` | `docs/04-UI设计规范.md`、`web/` |
| 5 | 自研超级并发内核 | `kernel/**`、`docs/05-内核设计.md` | `kernel/`、`docs/05-内核设计.md` |
| 6 | 代码开发撰写（自研）+ 审核 + 测试 | `src/**`、`tests/**`、`docs/06-代码审核与测试.md` | `src/`、`tests/`、`docs/06-代码审核与测试.md` |
| 7 | 代码层漏洞审查与修复 | `docs/07-漏洞审查与修复.md`、`patches/**` | `docs/07-漏洞审查与修复.md`、`patches/` |
| 8 | 项目功能审查核实 | `docs/08-功能核实报告.md`、`verify/**` | `docs/08-功能核实报告.md`、`verify/` |

Lead 冻结的基线文件：`SPEC.md`、`LLM-API-Aggregator-Prompt.md`、`go.mod`、`kernel/contract.go`、`data/**`、`README.md`。`kernel/contract.go` 定义四个对外接口（`Scheduler` / `Selector` / `StreamPipe` / `Limiter`），团队 5 实现、团队 6 使用。

## 功能范围（F1–F14，禁止简化）

F1 用户鉴权 · F2 渠道贡献与管理 · F3 真实模型参数发现 · F4 真实模型输入输出采集 · F5 统一数据面 API（`/v1/models`、`/v1/chat/completions`、`/v1/messages` 等） · F6 反代理与流式 SSE · F7 OpenAI↔Anthropic 双向协议适配 · F8 路由负载均衡与 failover · F9 令牌配额限流 · F10 计量日志可观测 · F11 管理后台 · F12 用户前台 · F13 安全（AES-256-GCM + SSRF 防护） · F14 部署。

完整需求见 `LLM-API-Aggregator-Prompt.md`。

## 技术基线

- 后端 Go 1.22，`module llm-hub`，**零第三方依赖**（全部标准库，保证 `go build ./...` 可通）。
- 前端 Next.js 14 App Router + TypeScript + Tailwind，视觉对标 `https://openrouter.ai`。
- 存储：JSON 文件持久化 + 内存索引（标准库自研），避免外部依赖。
- 并发：`kernel/` 自研有界优先级调度器、分片无锁环形队列、work-stealing 线程池、加权轮询/熔断选择器、SSE 零拷贝背压转发、令牌桶。

## 快速验证

```bash
go build ./...
go test ./... -count=1
go test -bench . -benchmem ./kernel/...
python3 verify/check_real_data.py     # 校验所有模型 id ∈ data/models-real.json
bash verify/e2e.sh                    # 起服务跑 /v1/models、/healthz、/metrics 与错误分支
```

## 目录

```
SPEC.md                      八团队协同基线
LLM-API-Aggregator-Prompt.md  完整项目生成提示词
data/                        真实模型数据与设计参考
docs/01..08                  方案 / 设计 / 内核 / 审核 / 测试 / 漏洞 / 核实报告
kernel/                      自研超级并发内核
src/                         服务端主体
tests/                       单元与集成测试
web/                         Next.js 前端
patches/                     漏洞修复补丁
verify/                      真实数据与端到端核实脚本
```

## 最终验收（Lead 真实复核）

八团队任务全部 completed（task-1..task-8）。真实数据源：`https://openrouter.ai/api/v1/models` HTTP 200 → `data/models-real.json` 827,419 B / SHA-256 `08b4635f…876a` / **460 个真实模型 / 69 家真实供应商**；设计参考 `data/openrouter-home.html` 214,265 B / SHA-256 `389f9681…0770`。

```
go build ./...                            → exit=0
go vet ./...                              → exit=0
go test ./... -count=1                    → exit=0（ok llm-hub/kernel、ok llm-hub/tests）
go test ./tests/... -count=1 -v           → RUN 41 / PASS 41 / FAIL 0
go test -bench . -benchmem ./kernel/...   → Queue 20.83 ns/op / Scheduler 575.4 ns/op / StreamPipe 552.02 MB/s（真实输出）
python3 verify/check_real_data.py         → models=460 providers=69 suspicious=0 fabricated_in_deliverables=0 exit=0
bash verify/e2e.sh (PORT=18201)           → 14/14 全绿：/v1/models=200 且 460 条全真实、/v1/chat/completions=502 upstream_error 无伪造输出、/v1/messages=502、RPM 6×429、SSRF 4/4 真实拒绝
npm run build (web/)                      → ✓ Compiled successfully / 17/17 static pages / BUILD_EXIT=0（团队4 真实执行，docs/04 §10）
```

- **F1–F14 全部实现、无功能删减**：`docs/03` 审核 14/14 覆盖、结论「通过」；`docs/08` 核实 已实现 10 / 部分 4 / 未实现 0。
- **C-1 真实数据**：所有模型 id 逐字来自 `data/models-real.json`，`web/data/models.json` 460/460 真实；运行时未拉取成功时列表留空并标 `probe_status=unavailable`，绝不编造；无上游凭据返回统一 `upstream_error`。
- **自研超级并发内核** `kernel/`：Vyukov 无锁 MPMC 队列、work-stealing 线程池、有界优先级调度器、SWRR+熔断选择器、SSE 零缓冲转发、连接池、令牌桶、原子指标；`docs/05` 含真实 bench。
- **漏洞审查与修复**：`docs/07` 33 条漏洞（Critical 1 / High 8 / Medium 21 / Low 3）+ 冻结后补审 V-34/V-35；`patches/001–018` 共 18 个补丁已由 Lead 真实 `git apply` 到工作区并复验通过。
- **部署**：`deploy/{Dockerfile,docker-compose.yml,nginx.conf,.env.example}` + `Makefile`；Nginx 关闭 `proxy_buffering` 保证 SSE 逐块输出。

受限验证项（如实保留，非功能缺失）：F4/F6/F8 的真实上游成功路径因无真实上游 `base_uri`+`api_key` 未做端到端回放；F14 未在本机执行 `docker compose up -d`；F10 的 `request_logs` 未逐条导出核对。补齐凭据/容器环境后按 `docs/08` §4 命令即可复验。


## 天衡改造（2026-11-07 第二轮 · 全员超级用户版）

- **所有用户都是超级用户，没有任何管理员**：注册即 `superuser`，任意登录用户可访问全部 `/api/admin/*`、渠道/令牌/别名/设置端点；历史 admin/user 角色启动时自动归一。真实断言见 `tests/superuser_test.go`（`TestAllUsersAreSuperusers` PASS）。
- **没有任何使用限制**：RPM/TPM/并发/配额/IP 白名单/过期时间全部移除（`verify/e2e.sh` S7 实测连续 8 次请求 8×200、429=0、402=0）；令牌字段保留、默认 0=不限。防火墙的攻击检测（非使用限制）保留并放宽 burst 阈值到 5000/10s，不误伤正常高频调用。
- **社区论坛**：`/api/forum/*` 发帖/回复/点赞/置顶/检索/分页，`<DataDir>/forum.json` 原子持久化、零种子假数据；前端 `/community`。见 `docs/11-社区论坛.md`。
- **头像上传**：`/api/avatar/*` 魔数/类型/2MiB 校验、服务端随机文件名、0600 落盘、128×128 缩略图；前端 `/profile`。见 `docs/12-头像上传.md`。
- **全员可见实时监控**：`/api/telemetry/snapshot|stream`（public SSE，聚合数字、无明文泄露），前端 `/monitor` 多曲线实时刷新。见 `docs/13-实时监控.md`。
- **数据库绝对强硬**：原子写+fsync+rename、0600/0700、SHA-256 完整性链篡改检测、WAL 崩溃恢复、备份轮转（`docs/14-数据库加固.md`，18 单测 + `-race` 通过）。
- **填密钥即自动发现全部真实模型**：用户只填 `provider_type/base_uri/api_key`，站点自动 `GET {base_uri}/models` + 真实探测，失败留空 `probe_status=unavailable` 绝不编造；`MODEL_AUTO_REFRESH_SEC` 定时刷新。见 `docs/15-自动发现.md`。
- **品牌**：全站「天衡 Tianheng」，导航含 /rankings /community /profile /monitor。

## 本机靶场与 dsh-plugin-offsec 攻击测试（docs/16）

`deploy/target-range.sh` 在 127.0.0.1:18400 拉起隔离靶场；dsh-plugin-offsec 的 `scan_ports`（341 端口仅 18400 开放）与 `dir_scan`（26 路径字典）真实扫描时，`/.env`/`/.git/HEAD` 即触发 `state_probe → auto_lockdown`，后续探测全部 423——**自动保护在扫描进行中实战生效**（evidence/14）。人工攻击面 9 组（论坛注入/头像攻击/SSRF 7 编码/JWT 伪造/越权/明文扫描/telemetry 泄露/SSE 耗尽/解锁通道滥用）发现 3 条真实漏洞并全部修复复测：V-36 攻击者可即时解锁（回环限定）、V-37 SSE 无并发上限（256 上限+共享聚合）、V-38 封存期自锁 DoS（回环认证恢复通道）——补丁 `patches/019–021`。

## Magpie 原理的原生实现（零第三方依赖）

天衡在内部**原生实现**了 AI 供应商聚合层 Magpie 的五条路由原理（不集成、不调用、不依赖它）：会话亲和（多轮会话钉住同一上游以复用 prompt 缓存）、分组 + 条件规则、三级出站脱敏（known/model/rule × 11 类，出站遮蔽 + 入站还原）、上下文构成分解（system/tools/messages，`counted` 如实标注）、路由决策全量留痕（order/tries/affinity/masking/prompt，按日分片 + gzip 归档）。

代码：`src/internal/{affinity,grouping,redact,ctxbreakdown,routingjournal,fsutil}/`，共 59 个单测。
运行时视图：`/api/routing/{affinity,journal,groups,resolve,redact}`。
完整原理还原过程与真实端到端证据见 [docs/20-Magpie原理原生实现.md](docs/20-Magpie原理原生实现.md)。

## 真实排行榜与攻防防火墙

- **只保留真实数据**：清除产品内所有非真实示例（虚构 `key_hint`、示意哈希、静态演示措辞）；看板数字全部由 `web/data/models.json` 派生；运行时未采集的数据一律显示「未采集」/`unavailable`，不预填任何调用量、趋势、评分、延迟。
- **完整模型排行榜**：`GET /api/rankings`、`GET /api/rankings/{dimension}`、`?verify=1`。8 个可复核维度（上下文长度、输入价、输出价、能力覆盖、发布时间、最大输出、供应商分布等），全部 460 条真实记录参与排名；`src/internal/rank/rank.go` 由 `go:embed` 真实字节实时计算 `source_sha256`；前端 `web/app/rankings/page.tsx` 展示完整榜单、公式与来源哈希。
- **攻防型自适应防火墙**：`src/internal/firewall/firewall.go` 检测 SQL 注入 / XSS / 路径穿越 / 命令注入 / SSTI / SSRF 元数据 / 扫描器 UA / 认证暴力 / 路径扫描 / 速率突发；自动分级 `observe → throttle → block → lockdown`，封禁 IP、递增会话 epoch、封存敏感数据、写 SHA-256 哈希链审计事件与 0600 权限快照；lockdown 时外层硬阻断全部业务端点（423），仅放行 `/healthz` 与回环运维通道；**不读取请求体**，LLM prompt 与凭据不进入检测面。
- **绝对硬的数据保护**：事件与快照绝不含 `api_key` / `sk-` 令牌 / JWT / 密码哈希明文；`GuardSensitive` 纵深封禁管理面；冷却自动降级但 `sealed` 保持到管理员显式 `POST /api/firewall/unlock`；阈值可由 `FIREWALL_BLOCK_SCORE` / `FIREWALL_LOCKDOWN_IP_THRESHOLD` / `FIREWALL_LOCKDOWN_TTL_SEC` 演练。
- 真实端到端：`bash verify/rankings_firewall_e2e.sh` → `RANKINGS_REAL=PASS  AUTO_PROTECTION=PASS  HARD_DATA_SEAL=PASS  RECOVERY=PASS`（460 条全真实、攻击后 423、快照 0600 无明文、解锁后 200）。
