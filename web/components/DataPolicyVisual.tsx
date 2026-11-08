/**
 * 数据策略可视化（对应真实首页 "Data policy visualization / Custom Data Policies" 板块）
 * 全部条目取自 LLM-API-Aggregator-Prompt.md F13 与 REST 契约常量，不编造运行时数据。
 */
const GATES = [
  {
    step: "01",
    title: "平台令牌只存哈希",
    detail: "sk-<32位随机> 数据库仅保存 sha256；数据面校验后剥离，不透传上游。",
    tag: "F9 / F13",
  },
  {
    step: "02",
    title: "上游密钥 AES-256-GCM 加密落库",
    detail: "api_key 用 MASTER_KEY（32 字节 base64）加密，接口只返回 key_hint（末 4 位），永不明文回显。",
    tag: "F2 / F13",
  },
  {
    step: "03",
    title: "SSRF 白名单校验",
    detail: "拒绝 127.0.0.0/8、10/8、172.16/12、192.168/16、169.254.169.254、::1 与 file/gopher 协议；DNS 解析后二次校验。",
    tag: "F2 / F13",
  },
  {
    step: "04",
    title: "日志脱敏",
    detail: "LOG_MASK_KEYS 开启时上游 key 仅保留末 4 位 key_hint；不写入任何日志；请求体大小受 BODY_LIMIT_MB 限制。",
    tag: "F10 / F13",
  },
];

export default function DataPolicyVisual() {
  return (
    <div className="grid gap-5 lg:grid-cols-[7fr_5fr]">
      <div className="card-surface p-6 md:p-8">
        <div className="flex items-center gap-2">
          <span className="flex size-7 items-center justify-center rounded-md bg-positive/12 text-positive-text">
            <svg viewBox="0 0 20 20" fill="none" aria-hidden="true" className="size-4">
              <path d="M10 2.5l6 2.5v4.5c0 4-2.6 6.6-6 8-3.4-1.4-6-4-6-8V5l6-2.5Z" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
              <path d="M7.5 10l1.7 1.7 3.4-3.4" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </span>
          <h3 className="font-brand text-base font-semibold text-heading">Custom Data Policies</h3>
        </div>
        <p className="mt-3 max-w-xl text-sm leading-relaxed text-muted-foreground">
          保护组织数据：细粒度数据策略贯穿「令牌 → 密钥 → 出站 → 日志」四道闸门，
          确保上游凭据与完整请求体只在受控边界内流转。
        </p>

        <ol className="mt-7 space-y-0">
          {GATES.map((g, i) => (
            <li key={g.step} className="relative flex gap-4 pb-6 last:pb-0">
              {i < GATES.length - 1 && (
                <span className="absolute left-[15px] top-8 h-full w-px bg-border" aria-hidden="true" />
              )}
              <span className="relative z-10 flex size-8 shrink-0 items-center justify-center rounded-full border border-border bg-card font-mono text-2xs text-primary">
                {g.step}
              </span>
              <div className="min-w-0 pt-0.5">
                <div className="flex flex-wrap items-center gap-2">
                  <h4 className="text-sm font-semibold text-heading">{g.title}</h4>
                  <span className="rounded-full border border-border bg-muted/60 px-2 py-0.5 font-mono text-2xs text-muted-foreground">
                    {g.tag}
                  </span>
                </div>
                <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">{g.detail}</p>
              </div>
            </li>
          ))}
        </ol>
      </div>

      <div className="space-y-5">
        <div className="card-surface p-6">
          <p className="text-overline uppercase tracking-widest text-muted-foreground">统一错误体</p>
          <pre className="mt-3 overflow-x-auto rounded-md border border-border bg-muted/40 p-3 font-mono text-2xs leading-relaxed text-foreground/90">
{`{
  "error": {
    "message": "...",
    "type": "invalid_request_error",
    "code": "...",
    "request_id": "..."
  }
}`}
          </pre>
          <p className="mt-3 text-2xs leading-relaxed text-muted-foreground">
            400 / 401 / 402 / 403 / 404 / 429 / 500 / 502 / 504 逐一对应。
          </p>
        </div>

        <div className="card-surface p-6">
          <p className="text-overline uppercase tracking-widest text-muted-foreground">硬数据保护规则（真实实现）</p>
          <div className="mt-4 space-y-3 font-mono text-xs">
            <div className="flex items-center justify-between gap-3 rounded-md border border-border bg-muted/40 px-3 py-2">
              <span className="text-muted-foreground">api_key</span>
              <span className="text-foreground">AES-256-GCM 密文落库 · 仅回显 key_hint 末 4 位</span>
            </div>
            <div className="flex items-center justify-between gap-3 rounded-md border border-border bg-muted/40 px-3 py-2">
              <span className="text-muted-foreground">token_hash</span>
              <span className="text-foreground">仅存 sha256(令牌) · 明文创建时返回一次</span>
            </div>
            <div className="flex items-center justify-between gap-3 rounded-md border border-border bg-muted/40 px-3 py-2">
              <span className="text-muted-foreground">X-Request-Id</span>
              <span className="text-foreground">全链路透传（不含凭据）</span>
            </div>
          </div>
          <p className="mt-4 text-2xs leading-relaxed text-muted-foreground">
            LOG_FULL_BODY 与 LOG_MASK_KEYS 均为系统设置项（F11 /admin/settings）。
          </p>
        </div>
      </div>
    </div>
  );
}
