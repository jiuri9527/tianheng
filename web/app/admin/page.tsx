import Link from "next/link";
import type { Metadata } from "next";
import { datasetSummary, providerDistribution } from "@/lib/stats";
import ConsoleLayout from "@/components/ConsoleLayout";
import RuntimeMetrics from "@/components/RuntimeMetrics";
import { BarChart } from "@/components/Chart";

export const metadata: Metadata = { title: "管理后台" };

const SECTIONS = [
  { href: "/admin/channels", label: "渠道管理", hint: "CRUD · 一键同步/探测 · 健康状态" },
  { href: "/admin/models", label: "模型池与别名", hint: "合并/拆分/禁用/改名/定价覆盖" },
  { href: "/admin/users", label: "用户与角色", hint: "admin / user 角色管理" },
  { href: "/admin/settings", label: "系统设置", hint: "超时 · failover · cron · 奖励比例 · 脱敏" },
  { href: "/logs", label: "日志检索", hint: "request_id / 用户 / 模型 / 状态码" },
  { href: "/usage", label: "用量看板", hint: "QPS / 成功率 / Top 模型 / 渠道健康" },
];

export default function AdminPage() {
  const summary = datasetSummary();
  return (
    <ConsoleLayout
      eyebrow="Admin · F11"
      title="管理后台"
      description="渠道 CRUD + 一键同步/探测 + 健康状态；模型池与别名映射编辑；用户与角色；令牌审计与吊销；仪表盘；日志检索；系统设置。"
    >
      <RuntimeMetrics />

      <section className="mt-12">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">管理分区</h2>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {SECTIONS.map((s) => (
            <Link key={s.href} href={s.href} className="focus-ring card-surface group p-4 transition-[box-shadow,transform] duration-fast ease-smooth-out hover:shadow-card-hover motion-safe:hover:-translate-y-0.5">
              <p className="text-sm font-medium text-heading group-hover:text-primary">{s.label}</p>
              <p className="mt-1.5 text-2xs leading-relaxed text-muted-foreground">{s.hint}</p>
            </Link>
          ))}
        </div>
      </section>

      <section className="mt-12">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">全局模型池（真实）</h2>
        <div className="grid gap-5 lg:grid-cols-[7fr_5fr]">
          <div className="card-surface p-5">
            <p className="text-sm font-semibold text-heading">供应商模型数 Top 12</p>
            <p className="mt-1 text-2xs text-muted-foreground">{summary.models} 个真实模型 / {summary.providers} 家供应商</p>
            <div className="mt-5"><BarChart data={providerDistribution(12)} /></div>
          </div>
          <div className="card-surface p-5 font-mono text-2xs leading-loose text-muted-foreground">
            <p className="text-overline uppercase tracking-widest text-foreground">数据面 / 管理面</p>
            <p className="mt-3">数据面：/v1/* · Bearer sk-…</p>
            <p>管理面：/api/* · JWT</p>
            <p className="mt-3">GET /api/admin/dashboard</p>
            <p>GET/PUT /api/admin/settings</p>
            <p>GET /api/admin/users</p>
            <p>GET /api/models · PUT /api/models/:id</p>
            <p>GET/POST/PUT /api/aliases</p>
            <p className="mt-3 text-foreground">所有静态展示模型逐字来自 data/models-real.json。</p>
          </div>
        </div>
      </section>
    </ConsoleLayout>
  );
}
