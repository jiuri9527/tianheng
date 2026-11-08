import Link from "next/link";

const COLUMNS: { title: string; links: { label: string; href: string }[] }[] = [
  {
    title: "Product",
    links: [
      { label: "模型广场", href: "/models" },
      { label: "价格对照", href: "/models?sort=price-asc" },
      { label: "渠道贡献", href: "/channels" },
      { label: "控制台", href: "/dashboard" },
      { label: "API 令牌", href: "/tokens" },
    ],
  },
  {
    title: "Developer",
    links: [
      { label: "用量看板", href: "/usage" },
      { label: "请求日志", href: "/logs" },
      { label: "管理后台", href: "/admin" },
      { label: "登录", href: "/login" },
      { label: "注册", href: "/register" },
    ],
  },
  {
    title: "Company",
    links: [
      { label: "路由可视化", href: "/#routing" },
      { label: "数据策略", href: "/#data-policy" },
      { label: "工作原理", href: "/#how-it-works" },
      { label: "Featured Models", href: "/#featured" },
    ],
  },
];

export default function SiteFooter() {
  return (
    <footer className="mt-24 border-t border-border bg-background">
      <div className="mx-auto w-full max-w-7xl px-4 py-14 md:px-6">
        <div className="grid grid-cols-2 gap-10 md:grid-cols-[2fr_1fr_1fr_1fr]">
          <div>
            <div className="flex items-center gap-2.5">
              <span className="flex size-7 items-center justify-center rounded-md bg-foreground text-background">
                <svg viewBox="0 0 24 24" fill="none" aria-hidden="true" className="size-4">
                  <path d="M4 7h6.5M4 7v10M4 17h6.5M20 7h-6.5M20 7v10M20 17h-6.5M10.5 12h3" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
                  <circle cx="12" cy="12" r="1.6" fill="currentColor" />
                </svg>
              </span>
              <span className="font-brand text-base font-bold tracking-tight">
                天衡 <span className="text-primary">Tianheng</span>
              </span>
            </div>
            <p className="mt-4 max-w-xs text-sm leading-relaxed text-muted-foreground">
              天衡（Tianheng）：众包渠道 + 统一出口。一个平台令牌、一个 /v1 地址，调用全网真实模型。
            </p>
            <p className="mt-4 font-mono text-2xs text-muted-foreground/60">
              data source: data/models-real.json · 460 models / 69 providers
            </p>
          </div>
          {COLUMNS.map((col) => (
            <div key={col.title}>
              <h3 className="text-overline uppercase tracking-widest text-muted-foreground">
                {col.title}
              </h3>
              <ul className="mt-4 space-y-2.5">
                {col.links.map((l) => (
                  <li key={l.href + l.label}>
                    <Link
                      href={l.href}
                      className="focus-ring rounded-sm text-sm text-muted-foreground transition-colors duration-quick hover:text-foreground"
                    >
                      {l.label}
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
        <div className="mt-12 flex flex-col gap-2 border-t border-border pt-6 text-xs text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
          <span>© 2026 天衡 Tianheng · Unified LLM Gateway</span>
          <span className="font-mono text-2xs">OpenAI / Anthropic / Gemini / xAI 协议统一转换</span>
        </div>
      </div>
    </footer>
  );
}
