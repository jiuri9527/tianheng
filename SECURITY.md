# 天衡（Tianheng）安全说明

> 本文描述天衡通用大模型 API 聚合中转站的密钥体系、威胁模型、攻防防火墙语义、数据保护与运维流程。所有结论均对应仓库中的真实实现文件与行号；未实现的项在 §9「已知限制」中如实列出。

## 1. 安全目标与信任边界

天衡的业务模型是「众包渠道 + 统一出口」：任何注册用户都可以贡献自己的上游 `base_uri` + `api_key` 成为渠道，也可以调用全局模型池。**所有用户都是超级用户，没有任何管理员角色**（`src/internal/api/server.go` 的 `requireAdmin` 恒通过；`src/internal/auth/auth.go` 注册即 `superuser`）。

因此信任边界不是「管理员 vs 普通用户」，而是：

| 边界 | 受保护对象 | 防护手段 |
|---|---|---|
| 公网 → 网关 | 全部端点 | 攻防防火墙（`src/internal/firewall/firewall.go`）、请求体上限、SSRF 校验器 |
| 网关 → 上游 | 用户贡献的 `api_key` | AES-256-GCM 字段级加密，仅回显 `key_hint` |
| 网关 → 本地状态 | `api_key` 密文、密码哈希、令牌哈希、请求日志 | 0600 文件、0700 目录、SHA-256 完整性链、WAL |
| 公开读 → 敏感读 | 监控面板 / 排行榜 / 健康检查 | 只返回聚合数字，绝不返回原文 |

## 2. 密钥体系

| 密钥 | 用途 | 存储形式 | 丢失后果 |
|---|---|---|---|
| `MASTER_KEY` | AES-256-GCM 加密上游 `api_key` | 环境变量（32 字节 base64） | 已有渠道密文不可解，需用户重新提交 `api_key` |
| `JWT_SECRET` | 签发/校验管理面 JWT（HS256） | 环境变量 | 全部会话失效，用户需重新登录 |
| 平台令牌 `sk-…` | 调用 `/v1/*` | 数据库只存 `sha256(token)`，明文仅在创建响应返回一次 | 无（可重新创建） |
| 用户密码 | 登录 | 迭代式 HMAC-SHA256 KDF（盐 + 轮数），非明文 | 无（可重置） |

**`MASTER_KEY` 缺失时服务拒绝启动**（`src/internal/crypto/crypto.go` 的 `NewCipher` 明确报错），绝不回退到源码内可推导的开发密钥 —— 这是补丁 `patches/002-crypto-master-key-and-keyhint.patch` 修复的原始缺陷（所有未配置部署共享同一把 AES 主密钥）。

### 2.1 密钥轮换步骤

1. 生成新主密钥：`python3 -c 'import os,base64;print(base64.b64encode(os.urandom(32)).decode())'`
2. 以「双密钥并存」方式滚动：先设置 `MASTER_KEY_PREVIOUS=<旧值>`、`MASTER_KEY=<新值>`，重启服务。
3. 逐条触发渠道重加密（调用 `PUT /api/channels/{id}` 提交同一 `api_key`，或以运维脚本遍历），确认 `channels.json` 内密文全部可被新密钥解开。
4. 清空 `MASTER_KEY_PREVIOUS`，再次重启，完成轮换。

`JWT_SECRET` 轮换直接替换环境变量并重启，所有既有会话立即失效（这是期望行为）。

## 3. 威胁模型

| 威胁 | 攻击面 | 防护 | 验证证据 |
|---|---|---|---|
| 存储型 XSS | 论坛标题/正文/回复 | 后端 `sanitizeText` 剥离标签 + `json.Encoder` HTML 转义 + 前端 React 纯文本渲染（全仓 `dangerouslySetInnerHTML` 仅用于主题脚本常量） | `verify/offsec_target/evidence/01-08-attack-round.txt` [B] 组 |
| SQL 注入 | 论坛检索、日志检索 | 无 SQL 后端（JSON 存储 + 内存索引）；检索用 `strings.Contains` 而非拼接查询 | 同上 [B2] |
| SSTI | 论坛内容 | 无模板求值路径，内容按字面存储 | 同上 [B3] |
| 上传攻击 | 头像 | 真实魔数（PNG/JPEG/WebP/GIF）+ Content-Type 双向校验 + 2 MiB 上限 + 服务端随机文件名 + 路径二次校验 + 0600 落盘 | 同上 [C] 组（400/413/400/400） |
| SSRF | `base_uri` | 协议白名单（仅 http/https）+ 内网/回环/元数据地址拒绝 + DNS 解析后二次校验 + 拨号时逐 IP 校验 + 重定向每跳重校验 | 同上 [D] 组（7 种编码全 400）；`patches/011-ssrf-dialpin-and-redirect.patch` |
| JWT 伪造 | 管理面 | 算法固定 HS256（拒绝 `alg=none`）+ 服务端密钥校验 | 同上 [E] 组（401/401） |
| 越权 | 论坛对象、渠道、令牌 | 对象级归属校验（作者/所有者）；「全员超级用户」不等于任意删改他人内容 | 同上 [F] 组（403/403） |
| 密钥泄露 | 日志、错误响应、状态文件 | `LOG_MASK_KEYS`、`crypto.Redact`、防火墙事件脱敏、错误体不含密钥 | 同上 [G] 组 `NO_PLAINTEXT_CREDENTIALS_IN_STATE` |
| 监控数据泄露 | `/api/telemetry/*` | 只返回聚合数字与分类键；`PLAINTEXT_LEAK_KEYS=NONE` | 同上 [H] 组 |
| 资源耗尽 | SSE 流、超大请求体 | SSE 并发上限 256（`TELEMETRY_STREAM_MAX_CONNS`）+ 同 tick 共享聚合 + `BODY_LIMIT_MB` | `patches/020`；300 并发实测 200=256/503=44 |
| 自动保护被武器化 | 防火墙解锁 | 解锁/封存仅限本机回环 | `patches/019`（V-36） |
| 封存期自锁 DoS | 认证端点 | 回环专用认证恢复通道 | `patches/021`（V-38） |
| 数据篡改 | 落盘状态 | SHA-256 完整性链 + 启动校验 + `*IntegrityError` 拒绝启动 | `docs/14-数据库加固.md`（18 单测） |

## 4. 攻防防火墙语义

`src/internal/firewall/firewall.go` 是**自适应攻击防护**，不是使用限制（天衡不设任何使用限制）。

- **检测规则**：SQL 注入、XSS、路径穿越、命令注入、SSTI、SSRF 元数据、敏感路径探测（`/.env`、`/.git/`）、扫描器 UA、认证暴力、404 路径扫描、速率突发。
- **不读请求体**：只检测 URL / 查询串 / 请求头，LLM prompt 与凭据不进入检测面。
- **分级**：`observe → throttle → block → lockdown`；连续失败达阈值自动封禁 IP（默认 5 分钟），多个 IP 被封或持续高危触发全局 `lockdown` + **数据封存**。
- **封存（sealed）语义**：全部业务端点返回 `423`，后端渠道密文、令牌哈希、`request_logs`、用户凭据完全不可达；`/healthz` 与**本机回环运维通道**例外。
- **运维端点**：`GET /api/firewall/status`（公开只读）、`GET /api/firewall/events`（脱敏事件）、`GET /api/firewall/verify`（自检）、`POST /api/firewall/unlock|seal`（**仅本机回环**）。
- **冷却**：`FIREWALL_LOCKDOWN_TTL_SEC` 到期后 `level` 自动降级，但 `sealed` 必须由管理员在本机显式解锁 —— 防止保护半途失效。
- **可调阈值**：`FIREWALL_BLOCK_SCORE`、`FIREWALL_LOCKDOWN_IP_THRESHOLD`、`FIREWALL_LOCKDOWN_TTL_SEC`、`FIREWALL_BURST_THRESHOLD`（默认 5000/10s，避免误伤正常高频调用）。

## 5. 数据保护

- **落盘权限**：状态文件 `0600`、目录与备份目录 `0700`（`src/internal/store/hardening.go` 实测断言）。
- **原子写**：`CreateTemp(0600) → Write → fsync → Close → rename → 目录 fsync`，断电不产生半截文件。
- **完整性链**：`state.integrity` 每次写入追加一环 `SHA256(prev || state || seq)`，含逐表摘要；改 1 字节、删文件、改清单、改链环均返回明确 `*IntegrityError` 并拒绝启动。
- **WAL 崩溃恢复**：写前追加含完整快照与校验和的 WAL 并 fsync；5 个崩溃点均可恢复。
- **备份轮转**：`snapshot-%020d.json` 保留最近 N 份（默认 5）。
- **审计事件**：`firewall/events.jsonl` 每行含 `prev_hash`/`hash`，前后链接可校验；内容全部经 `Redact`，绝不写入 `sk-`、`Bearer`、`api_key`。

## 6. 备份与恢复

```bash
# 备份（服务运行中亦可，快照是原子写）
cp -a "$DATA_DIR" "/backup/tianheng-$(date +%Y%m%d-%H%M%S)"

# 校验完整性
# 启动时自动校验；也可离线验证 state.integrity 的链与逐表摘要
grep -c '"seq"' "$DATA_DIR/state.integrity"

# 恢复
systemctl stop tianheng           # 或 kill 进程
rm -rf "$DATA_DIR" && cp -a /backup/tianheng-XXXX "$DATA_DIR"
chmod 700 "$DATA_DIR" && chmod 600 "$DATA_DIR"/*.json "$DATA_DIR"/state.integrity
systemctl start tianheng
```

若 `state.integrity` 与状态文件不一致，服务会拒绝启动并打印 `*IntegrityError` —— 此时应回滚到最近备份，而不是删除完整性文件。

## 7. 审计

- 每次调用的完整输入/输出、token 数、TTFT、状态码、命中渠道、花费写入 `request_logs`（`LOG_FULL_BODY` 控制是否记全文，`LOG_MASK_KEYS` 控制脱敏）。
- `X-Request-Id` 全链路透传，错误体必含 `request_id`。
- 防火墙事件与快照独立落盘，`GET /api/firewall/verify` 返回 `credential_leaks` 与 `hash_chain_intact` 自检结果。
- 全站公开监控 `/api/telemetry/*` 只暴露聚合数字。

## 8. 安全测试

| 脚本 | 覆盖 | 命令 |
|---|---|---|
| `verify/e2e.sh` | 鉴权、令牌、`/v1/models` 真实 460 条、无上游 502、无限制断言、SSRF 4 例 | `bash verify/e2e.sh` |
| `verify/rankings_firewall_e2e.sh` | 排行榜真实性、攻击触发 lockdown、数据封存、回环解锁恢复 | `bash verify/rankings_firewall_e2e.sh` |
| `verify/offsec_target/` | dsh-plugin-offsec 真实攻击证据（论坛注入/上传/SSRF/JWT/越权/明文/泄露/SSE 耗尽/扫描触发保护） | 见 `docs/16-安全审计与靶场.md` |
| `verify/check_real_data.py` | 全仓模型数据真实性（460/69，`suspicious=0`） | `python3 verify/check_real_data.py` |

## 9. 已知限制（如实记录）

1. **跨进程写并发未加文件锁**：单进程部署下 `writeMu` 已串行化全部落盘写；多进程共享同一 `DATA_DIR` 不受保护。
2. **完整性链随提交次数线性增长**：保留全链以换取逐环可校验，长期运行需归档旧链段。
3. **遗留数据 TOFU**：加固前产生的状态文件默认采纳并标记 `AdoptedLegacy`，`Options{Strict:true}` 可改为拒绝启动。
4. **`/metrics` 与 `/api/telemetry/*` 为公开读**：只含聚合数字，但仍暴露站点流量规模；如需收敛请在 Nginx 层加 IP 白名单。
5. **`MASTER_KEY_PREVIOUS` 双密钥并存期**：轮换窗口内旧密文仍可解，窗口应尽量短。
6. **上游响应内容未做内容安全过滤**：天衡是透明反代理，不对模型输出做审查；合规责任在调用方。
