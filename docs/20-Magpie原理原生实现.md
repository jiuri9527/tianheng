# 20 · Magpie 原理的天衡原生实现

> 本文记录：把 AI 供应商聚合层 **Magpie（`com.yetone.magpie`）的运行原理**，用**零第三方依赖的 Go 原生代码**实现在天衡内部。
>
> **不是集成、不是调用、不是依赖 Magpie** —— 天衡不引用它的任何二进制、服务或数据。原理来自对它在本机留下的真实运行数据（`~/.config/magpie/`）的逆向分析，实现全部是 `src/internal/` 下的新包。

## 1. 原理是怎么被还原出来的

分析对象是本机真实存在的 Magpie 运行数据（只读、不执行）：

| 数据文件 | 体量 | 揭示了什么 |
|---|---|---|
| `affinity.json` | 27,647 B | 键为 `group/<分组id>\|<会话id>`，值为 `{rest, who, model, turn}` —— 会话级上游绑定 |
| `routing/2026-11-08.jsonl` | 7.2 MB，320 条 | 每条决策含 `order[]`/`tries[]`/`affinity{}`/`masking{}`/`prompt{}`/`group{}` |
| `routing/*.jsonl.gz` | 7 个历史分片 | 按日分片 + gzip 归档 |
| `providers.json` | 42,956 B | `groups[]` 含 `members[]`/`subs[]`/`rules[{use,effort,images}]` |
| `masking/*.jsonl` | 8 个日分片，最大 5.7 MB | 脱敏审计 |
| `settings.json` | — | `redact/redactShape/redactKinds/redactWordsWhole` 等策略 |
| `client-shapes.json` | — | 客户端指纹（`X-Stainless-*`、`Anthropic-Version`） |

一条真实决策记录（原文节选）：

```json
{"seq":0,"agent":"dsh","model":"group/auto-dfmodel","effort":"max",
 "provider":"commandcode",
 "order":[{"id":"trae-cn","who":"九日","plan":"Credits","model":"deepseek-v4.1-flash-max"},...],
 "tries":[{"id":"trae-cn","done":true,"status":200,"ms":4408,"ttft":4408}],
 "affinity":{"turn":82,"within":true,"last":"trae-cn","cacheWrite":515871,"kept":true,"why":"turn"},
 "masking":1523,
 "masked":{"n":707,"distinct":43,
           "kinds":{"API_KEY":2,"EMAIL":4,"PASSWORD":5,"PERSON":1,"SECRET":30,"USERNAME":1},
           "by":{"known":5,"model":14,"rule":24}},
 "prompt":{"window":1000000,"tokens":516345,"counted":true,"turns":82,
           "parts":[{"kind":"system","tokens":26354},{"kind":"tools"},{"kind":"messages"}]}}
```

从中提炼出 **5 条可移植的原理**，逐条用 Go 原生实现。

## 2. 五条原理与天衡实现

### 原理① 会话亲和：多轮会话钉住同一上游以复用 prompt 缓存

- **来源证据**：`affinity.turn=82`、`affinity.cacheWrite=515871`、`affinity.why="turn"`。
- **为什么必需**：上游对前缀相同的多轮对话有 prompt caching，命中部分的计费常是未命中的 1/10。若每轮都重新做加权轮询，缓存全 miss，长对话成本放大一个量级。
- **天衡实现**：`src/internal/affinity/`
  - `Decide()` 判定是否继续粘住，返回与原理一一对应的 `Decision{mode,turn,within,last,last_turn,at,cache_write,cache_read,kept,why}`
  - `why` 取值：`first|turn|cache|ttl|gone|cooling|disabled|limit`（如实记录放弃原因）
  - `Bind()` 成功后推进轮数；上游变了则重置计数（不把两个上游的轮数累加）
  - `ObserveCache()` 只记上游真实回报的缓存用量
  - TTL 30 分钟、轮数上限 96（主动轮换，避免流量长期偏向单一上游）
  - 原子落盘 `affinity.json`（0600 / 目录 0700）
- **关键区分**：亲和是**渠道维度的路由决策**，不是用户维度的配额 —— 它不限制任何用户能发多少请求/token/并发。

### 原理② 分组抽象 + 条件规则：逻辑名与物理成员解耦

- **来源证据**：`groups[{id:"cheap-first", rules:[{use:"stepfun-cn/step-5-preview", effort:"high"},{use:..., images:true}], members:[...]}]`。
- **天衡实现**：`src/internal/grouping/`
  - 成员表达式支持通配：`*/deepseek-*`、`openai/gpt-*`、`chan_ab12/model`、裸模型名
  - 规则匹配特征：`effort / images / tools / stream / min_input_tokens / max_input_tokens`
  - **规则只把成员提到最前，其余候选保留** —— 保住失败切换能力
  - 子分组可嵌套，带环检测
  - `AutoDerive()` 按真实模型 id 自动成组（**零配置可用**）；`ResolveOrAuto()` 让裸模型名透明命中自动分组
  - 原子落盘 `groups.json`

### 原理③ 三级出站脱敏：known / model / rule × 11 类

- **来源证据**：`masked.by={known:5, model:14, rule:24}`、`masked.kinds={API_KEY,EMAIL,PASSWORD,PERSON,SECRET,USERNAME}`。
- **天衡实现**：`src/internal/redact/`
  - 三级来源：`known`（系统已知值精确匹配，优先级最高）→ `model`（可插拔识别器）→ `rule`（确定性正则）
  - 11 类：`API_KEY/SECRET/PASSWORD/EMAIL/PERSON/USERNAME/PHONE/IP/HOME/BUCKET/USERHOST`
  - **出站遮蔽 + 入站还原**：模型若在回答里引用占位符，入站时还原为原值（否则用户看到的是自己的密钥变成乱码）
  - 同一原值占位符稳定（模型可引用）；重叠区间只替换一次
  - 审计日志 `masking/YYYY-MM-DD.jsonl` 只写计数，绝不写原文
  - `tier=model` 如实标注为 **本地结构识别器**（确定性代码，不调用外部模型），接口可注入真正的 NER

### 原理④ 上下文构成分解：把请求拆成有语义的部分

- **来源证据**：`prompt.parts=[{kind:"system",tokens:26354,items:[...]},{kind:"tools"},{kind:"messages"}]`、`counted:true`。
- **天衡实现**：`src/internal/ctxbreakdown/`
  - 兼容 OpenAI 与 Anthropic 两种形状（`system` 字符串/分块数组、`content` 字符串/分块）
  - 分解为 `system / tools / messages / other`，各自带 items 明细
  - **`counted=false` 如实标注**：天衡不捆绑 BPE 词表，估算口径固定可复现（ASCII ÷4、CJK ÷2.2）；注入真实 tokenizer 后 `counted=true`
  - `window` 与真实上下文长度比对，超限标记 `overflow`
  - 各部分之和 == 总数（面板可交叉核对）

### 原理⑤ 路由决策全量留痕：order / tries / 三类统计串联

- **来源证据**：每条决策同时含 `order[]`、`tries[]`、`affinity{}`、`masking{}`、`prompt{}`，按日分片 + gzip 归档。
- **天衡实现**：`src/internal/routingjournal/`
  - 记录 `order[]`（候选排序 + 命中依据）、`tries[]`（每次尝试的状态/耗时/TTFT/错误）
  - 同一条记录里串联 `affinity` / `masking` / `prompt` 三类统计（用同一个 key）
  - 按日分片 `routing/YYYY-MM-DD.jsonl`，`Archive()` 把历史压成 `.gz`
  - `Summarize()` 输出真实统计：状态分布、渠道分布、**失败切换次数**、亲和命中数、脱敏总数、P50/P95、TTFT
  - 目录 0700、文件 0600；单行损坏跳过不影响整批

## 3. 接线：五条原理如何进入真实转发路径

`src/internal/api/dataplane.go` 的 `doRelay`（每次 `/v1/*` 调用的唯一入口）：

```
请求进入
  ↓ ① 会话标识：X-Tianheng-Conv 头 或 (用户,逻辑模型) 稳定派生
  ↓ ② 分组解析：ResolveOrAuto（裸模型名透明命中自动分组）+ 条件规则
  ↓ ③ 亲和判定：Decide() → 命中则钉住该上游（order[0] 标 affinity-pinned）
  ↓ ④ 上下文分解：Analyze() → prompt 统计
  ↓ ⑤ 出站脱敏：Mask() → 替换后的 body 才发给上游
  ↓ 真实转发（失败按 MAX_FAILOVER 切换，每次尝试写入 tries[]）
  ↓ 成功：Bind() 推进轮数 + ObserveCache() 记录真实缓存 + Append() 写决策日志
响应返回
```

运行时视图（只读、真实数据、无数据即空态）：

| 端点 | 内容 |
|---|---|
| `GET /api/routing/affinity` | 亲和绑定 + 真实统计（含缓存命中率） |
| `GET /api/routing/journal?day=&limit=&summary=1` | 决策记录 + 真实汇总 |
| `GET /api/routing/groups` | 分组与展开成员数 |
| `GET /api/routing/resolve?model=&effort=&images=` | 规则预演（不产生真实调用） |
| `GET /api/routing/redact` | 脱敏策略与三级/11 类 |
| `POST /api/routing/redact/preview` | 脱敏预演（遮蔽 + 还原） |

## 4. 真实端到端证据

环境：`SERVER_PORT=18900`，`ALLOW_PRIVATE_UPSTREAM=1`（开发/靶场专用开关，默认关闭），上游为真实 HTTP mock（`/tmp/mock-upstream.py`）。

**出站脱敏（上游看到的是占位符）**：

```
第1轮 200 → MOCK_UPSTREAM_SAW: 第 1 轮：用 «API_KEY#1» 连 «IP#1»，家目录 «HOME#1»
第2轮 200 → MOCK_UPSTREAM_SAW: 第 2 轮：用 «API_KEY#1» 连 «IP#1»，家目录 «HOME#1»
第3轮 200 → MOCK_UPSTREAM_SAW: 第 3 轮：用 «API_KEY#1» 连 «IP#1»，家目录 «HOME#1»
```

原文 `sk-live-abcdef1234567890`、`10.20.30.40`、`/Users/jiuri/app` 一个都没有到达上游。

**会话亲和 + 缓存观测**（上游真实回报 `cached_tokens=9`）：

```
stats: {"entries":1,"groups":1,"channels":1,"max_turn":3,"total_turns":3,
        "cache_write_tokens":0,"cache_read_tokens":27,"cache_hit_percent":100}
conv-cache  turn=3  cache_read=27
```

**决策日志**（同一条记录串联五条原理）：

```
group:    {"id":"auto-liquid-lfm-3-1-7b","ref":"auto/model/liquid/lfm-3-1.7b",
           "name":"liquid/lfm-3-1.7b","members":["chn_.../liquid/lfm-3-1.7b"],"why":"default"}
order:    [(1,'mock','affinity-pinned')]
tries:    [('chn_a481ae',200,0)]
affinity: {"mode":"","turn":3,"within":true,"last":"chn_...","last_turn":2,
           "cache_write":0,"cache_read":18,"kept":true,"why":"cache"}
masking:  {"n":3,"distinct":3,"kinds":{"API_KEY":1,"HOME":1,"IP":1},"by":{"rule":3}}
prompt:   {"window":262144,"tokens":35,"counted":false,
           "method":"estimate: ascii≈bytes/4, cjk≈bytes/2.2 (no tokenizer bundled)","turns":1}
summary:  {"total":3,"by_status":{"200":3},"affinity_kept":2,"masked_total":9,
           "failover_runs":0,"total_tries":3,"avg_ms":1,"p95_ms":1}
```

**落盘权限**：`affinity.json` 0600、`routing/` 0700、`routing/2026-11-08.jsonl` 0600。

## 5. 实现过程中发现并修复的 4 个真实缺陷

| # | 缺陷 | 症状 | 修复 |
|---|---|---|---|
| 1 | 中文环境整词边界失效 | `isWordByte` 把 `>=0x80` 都当单词字符，IP 后的「，」是 3 字节 UTF-8 → **中文技术文档里几乎所有敏感串都漏脱敏** | 边界判定按「被匹配值自身书写系统」决定：ASCII 值只要两侧非 ASCII 单词字符即可；含 CJK 值额外要求两侧非汉字 |
| 2 | 朴素中文人名识别过度贪婪 | 「路径」「后缀」「家目录」被判成人名 → **把用户正常请求内容替换成占位符** | 放弃全篇猜名，改为**上下文锚定**（我叫/姓名:/Mr./Dr.），只遮蔽锚点后那一段 |
| 3 | EMAIL 被误分类为 USERHOST | `jiuri@example.com` → `USERHOST` | 调整规则顺序：EMAIL 优先于更宽松的 USERHOST |
| 4 | `MkdirAll` 不收紧已存在目录权限 | 0755 目录复制过来后仍是 0755 | `fsutil` 显式 `os.Chmod(dir, 0o700)` |

## 6. 与需求的一致性

| 需求 | 本实现是否冲突 |
|---|---|
| 所有用户都是超级用户，无管理员 | ✅ 不冲突。五条原理都不引入角色 |
| 不准设置 token/并发/设备限制 | ✅ 不冲突。亲和是渠道维度路由决策，**不是**用户配额；分组是逻辑命名；脱敏是内容保护；日志是观测 |
| 只保留真实数据 | ✅ 全部真实：分组来自真实渠道模型、缓存来自上游真实 usage、统计来自真实决策、脱敏计数来自真实匹配。无数据即真实空态（`available:false`） |
| 不准简化功能 | ✅ 五条原理全量实现，字段与原理来源一一对应 |

## 7. 测试

```bash
go test ./src/internal/affinity/ ./src/internal/grouping/ ./src/internal/redact/ \
        ./src/internal/ctxbreakdown/ ./src/internal/routingjournal/ -count=1
```

| 包 | 测试数 | 覆盖 |
|---|---|---|
| `affinity` | 12 | 首轮无亲和、多轮粘住、80 轮连续、上游失效、TTL、轮数上限、mode=off、换上游重置、持久化 0600/0700、格式校验、统计与清理、快照排序 |
| `grouping` | 12 | 成员通配、子分组展开与环检测、规则提前、token 区间、空分组、未知分组、自动派生、往返持久化、格式校验、稳定排序、零配置透明 |
| `redact` | 15 | 三级计数、禁用零统计、往返还原、占位符稳定、整词边界、短值忽略、重叠消解、逐类开关、识别器注入、审计无明文、CJK 边界、人名锚定、邮箱分类 |
| `ctxbreakdown` | 9 | OpenAI/Anthropic 形状、超窗口、未知窗口、真实 tokenizer、CJK 口径、非法 JSON、图片分块、部分顺序 |
| `routingjournal` | 11 | 追加读回、Seq 单调、按日分片、权限、缺分片空态、limit 取最近、归档与透明读回、损坏行跳过、真实汇总、空汇总、RawJSON 往返 |

全仓 `go build ./... / go vet ./... / go test ./...` 均通过。
