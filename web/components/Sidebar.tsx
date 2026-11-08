"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const GROUPS: {
  title: string;
  items: { href: string; label: string; hint: string; desc?: string }[];
}[] = [
  {
    title: "概览",
    items: [
      { href: "/dashboard", label: "仪表盘", hint: "F10" },
      { href: "/usage", label: "用量", hint: "F10" },
      { href: "/logs", label: "请求日志", hint: "F10" },
    ],
  },
  {
    title: "模型与渠道",
    items: [
      { href: "/models", label: "模型广场", hint: "F12" },
      { href: "/rankings", label: "模型排行榜", hint: "真实", desc: "中文说明：基于 web/data/models.json 460 条真实模型的多维排行榜" },
      { href: "/channels", label: "我的渠道", hint: "F2" },
      { href: "/tokens", label: "平台令牌", hint: "F9" },
    ],
  },
  {
    title: "天衡社区",
    items: [
      { href: "/community", label: "社区论坛", hint: "论坛", desc: "中文说明：渠道贡献与模型交流社区" },
      { href: "/profile", label: "头像资料", hint: "资料", desc: "中文说明：个人头像与资料设置" },
      { href: "/monitor", label: "实时监控", hint: "监控", desc: "中文说明：站点与上游渠道实时运行状态" },
    ],
  },
  {
    title: "管理",
    items: [
      { href: "/admin", label: "后台总览", hint: "F11" },
      { href: "/admin/channels", label: "渠道管理", hint: "F11" },
      { href: "/admin/models", label: "模型与别名", hint: "F11" },
      { href: "/admin/users", label: "用户与角色", hint: "F1" },
      { href: "/admin/settings", label: "系统设置", hint: "F11" },
    ],
  },
];

export default function Sidebar() {
  const pathname = usePathname();
  return (
    <aside className="w-full shrink-0 lg:w-60">
      <div className="lg:sticky lg:top-20">
        <nav aria-label="控制台导航" className="space-y-6">
          {GROUPS.map((group) => (
            <div key={group.title}>
              <p className="px-3 text-overline uppercase tracking-widest text-muted-foreground/70">
                {group.title}
              </p>
              <ul className="mt-2 space-y-0.5">
                {group.items.map((item) => {
                  const active =
                    pathname === item.href ||
                    (item.href !== "/admin" && pathname.startsWith(item.href + "/"));
                  return (
                    <li key={item.href}>
                      <Link
                        href={item.href}
                        title={item.desc ?? item.label}
                        aria-current={active ? "page" : undefined}
                        className={`focus-ring group flex items-center justify-between rounded-md px-3 py-2 text-button transition-colors duration-quick ease-smooth-out ${
                          active
                            ? "bg-accent text-accent-foreground"
                            : "text-muted-foreground hover:bg-card-hover hover:text-foreground"
                        }`}
                      >
                        <span>{item.label}</span>
                        <span className="font-mono text-2xs text-muted-foreground/60">
                          {item.hint}
                        </span>
                      </Link>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </nav>

        <div className="mt-8 hidden rounded-lg border border-border bg-card p-4 lg:block">
          <p className="text-overline uppercase tracking-widest text-muted-foreground">
            统一出口
          </p>
          <code className="mt-2 block break-all font-mono text-xs text-foreground">
            POST /v1/chat/completions
          </code>
          <p className="mt-2 text-2xs leading-relaxed text-muted-foreground">
            Bearer sk- … 一个令牌调用池中任意真实模型。
          </p>
          <Link
            href="/tokens"
            className="focus-ring mt-3 inline-flex rounded-sm text-2xs font-semibold text-primary hover:underline"
          >
            创建令牌 →
          </Link>
        </div>
      </div>
    </aside>
  );
}
