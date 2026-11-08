"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState } from "react";
import ThemeToggle from "@/components/ThemeToggle";

/** 全站主导航：label 为中文，desc 为中文说明（title / 无障碍提示）。 */
const NAV: { href: string; label: string; desc: string }[] = [
  { href: "/models", label: "模型广场", desc: "中文说明：浏览 460 条真实模型与参数" },
  { href: "/models?sort=price-asc", label: "价格对照", desc: "中文说明：按真实价格排序对照" },
  { href: "/#routing", label: "路由", desc: "中文说明：模型路由可视化" },
  { href: "/#data-policy", label: "数据策略", desc: "中文说明：数据策略可视化" },
  { href: "/channels", label: "渠道", desc: "中文说明：贡献与管理上游渠道" },
  { href: "/dashboard", label: "控制台", desc: "中文说明：用量、日志与仪表盘" },
  { href: "/rankings", label: "真实模型排行榜", desc: "中文说明：基于 web/data/models.json 真实数据的多维榜单" },
  { href: "/community", label: "社区论坛", desc: "中文说明：渠道贡献与模型交流社区" },
  { href: "/profile", label: "头像资料", desc: "中文说明：个人头像与资料设置" },
  { href: "/monitor", label: "实时监控", desc: "中文说明：站点与上游渠道实时运行状态" },
];

export default function SiteHeader() {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);

  return (
    <header className="sticky top-0 z-50 w-full border-b border-border bg-background/85 backdrop-blur-xl">
      <div className="mx-auto flex h-16 w-full max-w-7xl items-center gap-4 px-4 md:px-6">
        <Link
          href="/"
          className="focus-ring group flex shrink-0 items-center gap-2.5 rounded-sm"
          aria-label="天衡 首页"
        >
          <span className="relative flex size-7 items-center justify-center rounded-md bg-foreground text-background">
            <svg viewBox="0 0 24 24" fill="none" aria-hidden="true" className="size-4">
              <path
                d="M4 7h6.5M4 7v10M4 17h6.5M20 7h-6.5M20 7v10M20 17h-6.5M10.5 12h3"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
              />
              <circle cx="12" cy="12" r="1.6" fill="currentColor" />
            </svg>
          </span>
          <span className="font-brand text-base font-bold tracking-tight text-foreground">
            天衡<span className="ml-1 font-mono text-2xs font-medium uppercase tracking-widest text-primary">Tianheng</span>
          </span>
        </Link>

        <nav aria-label="主导航" className="hidden min-w-0 flex-1 items-center gap-0.5 overflow-x-auto lg:flex">
          {NAV.map((item) => {
            const base = item.href.split("?")[0].split("#")[0];
            const active = pathname === base && base !== "/";
            return (
              <Link
                key={item.href}
                href={item.href}
                title={item.desc}
                className={`focus-ring whitespace-nowrap rounded-md px-3 py-2 text-button transition-colors duration-quick ease-smooth-out ${
                  active
                    ? "bg-muted text-foreground"
                    : "text-muted-foreground hover:bg-card-hover hover:text-foreground"
                }`}
              >
                {item.label}
              </Link>
            );
          })}
        </nav>

        <div className="ml-auto flex items-center gap-2">
          <ThemeToggle compact />
          <Link
            href="/tokens"
            className="focus-ring hidden items-center justify-center rounded-md bg-primary px-3.5 py-2 text-button font-medium text-primary-foreground no-underline transition-[filter] duration-quick ease-smooth-out hover:brightness-110 sm:inline-flex"
          >
            获取 API Key
          </Link>
          <button
            type="button"
            aria-expanded={open}
            aria-controls="mobile-nav"
            aria-label="展开导航菜单"
            onClick={() => setOpen((v) => !v)}
            className="focus-ring inline-flex size-9 items-center justify-center rounded-md border border-border text-foreground lg:hidden"
          >
            <svg viewBox="0 0 20 20" fill="none" aria-hidden="true" className="size-4">
              <path
                d={open ? "M5 5l10 10M15 5L5 15" : "M3 6h14M3 10h14M3 14h14"}
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>
      </div>

      {open && (
        <nav
          id="mobile-nav"
          aria-label="移动端导航"
          className="border-t border-border bg-background px-4 py-3 lg:hidden"
        >
          <div className="grid grid-cols-2 gap-1">
            {NAV.map((item) => (
              <Link
                key={item.href}
                href={item.href}
                title={item.desc}
                onClick={() => setOpen(false)}
                className="focus-ring rounded-md px-3 py-2 text-button text-muted-foreground hover:bg-card-hover hover:text-foreground"
              >
                {item.label}
              </Link>
            ))}
            <Link
              href="/tokens"
              onClick={() => setOpen(false)}
              className="focus-ring rounded-md bg-primary px-3 py-2 text-button font-medium text-primary-foreground"
            >
              获取 API Key
            </Link>
          </div>
        </nav>
      )}
    </header>
  );
}
