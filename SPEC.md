# SPEC — 天衡 Tianheng 通用大模型 API 聚合中转站 · 八团队协同基线

> 站点品牌名：**天衡（Tianheng）**；`go.mod` 模块名 `llm-hub` 为冻结技术标识符，保持不变（见 §2 统一技术契约），确保后端零第三方依赖与 `go build ./...` 契约一致。

> 本文件是 8 个团队的**唯一共同基线**。所有团队开工前必须先读本文件与 `LLM-API-Aggregator-Prompt.md`。
> 工作目录：`/Users/jiuri/Desktop/Tianheng`。

## 0. 项目定义（不可简化）

一个多租户「众包渠道 + 统一出口」大模型 API 网关：任何人贡献自己的上游 `base_uri` + `api_key` 成为渠道；站点自动连接上游，**拉取该渠道真实可用的模型清单与每个模型的真实参数**，并用一次真实调用采集模型的**真实输入/输出样例**；把 grok(xAI)、Claude(Anthropic)、ChatGPT(OpenAI)、Gemini 及任意 OpenAI 兼容上游的协议做统一转换，聚合为一个全局模型池；所有人只用一个平台令牌 `sk-xxxx`、一个地址 `/v1`，即可调用池中任意模型。

完整需求清单见 `LLM-API-Aggregator-Prompt.md` 的 **F1–F14**，逐条实现，**禁止删减、禁止降级为概述、禁止用 TODO/略/伪代码占位**。

## 1. 硬约束（违反即返工）

### C-1 真实数据（最高优先级）
- 唯一静态真实数据源：`data/models-real.json`（HTTP 200 抓取自 `https://openrouter.ai/api/v1/models`），**460 个真实模型 / 69 家真实供应商**，SHA-256 `08b4635fd684689feda841f906d59e0dcd8ad9dd57a7c0b7f0667d46b740876a`。
- 派生可读摘要：`data/models-real-summary.json`、`data/models-real-summary.md`。
- **禁止伪造任何模型 id、名称、上下文长度、价格、供应商、参数、输入输出样例。** 文档/UI/代码/测试里出现的每一个模型必须能在 `data/models-real.json` 中逐字查到；引用时标注 `"source":"data/models-real.json"`。
- 运行时数据（用户贡献渠道的模型）必须来自对真实上游 `GET {base_uri}/models` 的实际请求结果；**未拉取成功时该渠道模型列表为空并标记 `probe_status=unavailable`，绝不填充占位/编造模型**。
- 演示与截图数据只允许使用 `data/models-real.json` 的真实条目；禁止 `gpt-4o` / `grok-3` 之类凭印象手写（除非该 id 真实存在于数据集中，须先 grep 校验）。
- 真实设计参考：`data/openrouter-home.html`（抓取自 https://openrouter.ai，214,265 字节，SHA-256 `389f9681bee42548fb55ce68c76a7483693f56bffc5613d2bd725ebc0f320770`）。

### C-2 功能不简化
F1 用户鉴权 / F2 渠道贡献与管理 / F3 真实模型参数发现 / F4 真实模型输入输出采集 / F5 统一数据面 API / F6 反代理与流式 SSE / F7 协议适配器（OpenAI↔Anthropic 双向转换）/ F8 路由负载均衡与 failover / F9 令牌配额限流 / F10 计量日志可观测 / F11 管理后台 / F12 用户前台 / F13 安全（AES-256-GCM + SSRF 防护）/ F14 部署 —— **全部实现**。

### C-3 UI 对标
按 https://openrouter.ai 的视觉语言设计，2026 年顶级审美：深色/浅色双主题、克制的品牌色、大字号 hero 标题（`font-brand`/`text-hero`）、`text-prose`/`text-muted-foreground` 层级、`max-w-7xl` 居中栅格、`rounded-sm` 微圆角、`outline-hidden`/`focus-visible:shadow-focus` 无障碍焦点、`duration-quick ease-smooth-out` 微动效、模型路由可视化、价格与性能对照、数据策略可视化。参考 `data/openrouter-home.html` 的真实 class 名与结构。

### C-4 自研超级并发内核
`kernel/` 必须是**自研**实现，不允许用 `gin`/`net/http` 默认调度顶替：自研有界优先级任务调度器、分片无锁环形队列、工作窃取（work-stealing）线程池、加权轮询 + 最少在途 + 熔断选择器、流式 SSE 背压与零拷贝转发、连接池与超时取消、令牌桶限流、原子计数指标。需给压测数据与并发基准。

## 2. 统一技术契约（团队间接口固定，按此并行）

- 后端：**Go 1.22**，`go.mod` 模块名 `llm-hub`。
- `kernel/` 包路径 `llm-hub/kernel`，对外导出：
  ```go
  type Scheduler interface { Submit(*Task) error; Start(); Stop(); Stats() Stats }
  type Selector interface { Pick(candidates []Channel) (Channel, error); Report(Channel, Result) }
  type StreamPipe interface { Forward(ctx context.Context, w http.ResponseWriter, up io.Reader) error }
  type Limiter interface { Allow(key string, cost int64) (bool, error) }
  ```
- `src/` 主体：`src/cmd/server/main.go`、`src/internal/{config,db,cache,auth,crypto,middleware,adapter,discovery,router,relay,metering,model,channel,token,log,admin,health}/`。通过 `import "llm-hub/kernel"` 使用内核，**不得修改 kernel/ 目录**（内核归团队 5）。
- 前端 `web/`：Next.js 14 App Router + TypeScript + Tailwind；`web/lib/models.ts` 从 `/api/models` 读取真实数据；静态展示数据取自 `data/models-real.json` 导出的 `web/data/models.json`（由团队 4 生成，逐字来自真实数据集）。
- 统一错误体：`{"error":{"message","type","code","request_id"}}`。
- 管理面 `/api/*` JWT；数据面 `/v1/*` Bearer `sk-`。

## 3. 八团队分工与写入范围（严格互斥，禁止越界写）

| # | 团队 | 唯一写入范围 | 主要产出 | 依赖 |
|---|---|---|---|---|
| 1 | 方案撰写 | `docs/01-方案初稿.md` | 完整项目方案初稿（架构、模块、数据流、F1–F14 展开、里程碑） | — |
| 2 | 方案完善 | `docs/02-方案完善版.md` | 在初稿基础上补齐边界、异常、容量、成本、灰度、回滚、可观测 | 读 01 |
| 3 | 方案审核 | `docs/03-方案审核报告.md` | 逐条核对 F1–F14、真实数据约束、C-1~C-4，给问题清单与结论 | 读 01/02 |
| 4 | UI 设计 | `docs/04-UI设计规范.md`、`web/**` | 设计系统 + 完整页面原型 + `web/data/models.json`（真实数据） | — |
| 5 | 并发内核 | `kernel/**` | 自研超级并发内核源码 + 基准测试 + `docs/05-内核设计.md` | — |
| 6 | 开发/审核/测试 | `src/**`、`tests/**`、`docs/06-代码审核与测试.md` | 主体代码（自研）、代码审核记录、单元/集成测试 | 依契约引用 kernel |
| 7 | 漏洞审查与修复 | `docs/07-漏洞审查与修复.md`、`patches/**` | 漏洞清单（含 CWE/行号）+ 修复补丁文件 | 读 src/kernel |
| 8 | 功能审查核实 | `docs/08-功能核实报告.md`、`verify/**` | F1–F14 逐项核实、e2e 脚本与真实执行结果、数据真实性校验 | 读全部 |

> 团队 7 只写**报告与补丁文件**，不直接改 `src/`/`kernel/`；修复以可应用 `.patch` 交付。团队 8 只写 `verify/` 与报告，不改他人文件。

## 4. 完成定义（每个团队）

1. 交付文件真实存在且非空（用 `wc -c` / `go test ./...` / `go build ./...` 验证）。
2. 代码可编译、测试可执行并给出**真实运行输出**；禁止声称通过却无日志。
3. 所有模型数据可回溯到 `data/models-real.json`（在报告里给 grep 证据）。
4. 报告结尾列「未完成项」，无未完成项才标记完成。
5. 通过 `send_message` 把完成摘要与文件路径发回 `lead`。

## 5. 验收总纲（团队 8 与 Lead 共同执行）

```bash
go build ./... && go test ./...
python3 verify/check_real_data.py     # 校验每个模型 id ∈ data/models-real.json
bash verify/e2e.sh                    # 起服务，跑真实 /v1/models 与 SSE 转发
test -s docs/01-方案初稿.md && test -s docs/02-方案完善版.md && test -s docs/03-方案审核报告.md
test -s docs/04-UI设计规范.md && test -s web/app/page.tsx
test -s kernel/scheduler.go && test -s src/cmd/server/main.go
test -s docs/06-代码审核与测试.md && test -s docs/07-漏洞审查与修复.md && test -s docs/08-功能核实报告.md
```
