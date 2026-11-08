import Link from "next/link";
import type { Metadata } from "next";
import { getFeaturedModels } from "@/lib/models";
import { datasetSummary, providerDistribution, priceBands, capabilityCoverage, topByContext, cheapestPaid } from "@/lib/stats";
import ConsoleLayout from "@/components/ConsoleLayout";
import RuntimeMetrics from "@/components/RuntimeMetrics";
import ModelCard from "@/components/ModelCard";
import { BarChart, Donut } from "@/components/Chart";

export const metadata: Metadata = { title: "仪表盘" };

const QUICK = [
  { href: "/channels", label: "贡献渠道", hint: "F2 · base_uri + api_key" },
  { href: "/channels/openai?provider=openai", label: "渠道模型详情", hint: "F3 / F4 · 真实参数与探测" },
  { href: "/models", label: "模型广场", hint: "F12 · 460 真实模型" },
  { href: "/tokens", label: "创建令牌", hint: "F9 · sk- 与调用示例" },
  { href: "/usage", label: "用量分析", hint: "F10 · 模型池派生统计" },
  { href: "/logs", label: "请求日志", hint: "F10 · 完整输入输出" },
];

export default function DashboardPage() {
  const summary = datasetSummary();
  const providers = providerDistribution(8);
  const caps = capabilityCoverage();
  const price = priceBands();
  const featured = getFeaturedModels();
  const topContext = topByContext(5);
  const cheap = cheapestPaid(5);

  return (
    <ConsoleLayout
      eyebrow="Overview · F1–F14"
      title="仪表盘"
      description="统一查看运行时流量指标、真实模型池构成与快捷操作。运行时数字来自 /api/*；模型池统计全部由 data/models-real.json 派生。"
      actions={
        <Link href="/channels" className="focus-ring rounded-md bg-primary px-4 py-2 text-button font-medium text-primary-foreground no-underline transition-[filter] duration-quick hover:brightness-110">
          + 贡献渠道
        </Link>
      }
    >
      <RuntimeMetrics />

      <section className="mt-12">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">快捷操作</h2>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {QUICK.map((q) => (
            <Link key={q.href + q.label} href={q.href} className="focus-ring card-surface group flex items-center justify-between gap-3 p-4 transition-[box-shadow,transform] duration-fast ease-smooth-out hover:shadow-card-hover motion-safe:hover:-translate-y-0.5">
              <div>
                <p className="text-sm font-medium text-heading">{q.label}</p>
                <p className="mt-1 font-mono text-2xs text-muted-foreground">{q.hint}</p>
              </div>
              <svg viewBox="0 0 16 16" fill="none" aria-hidden="true" className="size-4 shrink-0 text-muted-foreground transition-transform duration-quick group-hover:translate-x-0.5">
                <path d="M3 8h10M9 4l4 4-4 4" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </Link>
          ))}
        </div>
      </section>

      <section className="mt-12">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">真实模型池构成</h2>
        <div className="grid gap-5 lg:grid-cols-2">
          <div className="card-surface p-5">
            <h3 className="text-sm font-semibold text-heading">供应商分布（Top 8）</h3>
            <p className="mt-1 text-2xs text-muted-foreground">共 {summary.providers} 家真实供应商</p>
            <div className="mt-5"><BarChart data={providers} /></div>
          </div>
          <div className="card-surface p-5">
            <h3 className="text-sm font-semibold text-heading">价格分桶</h3>
            <p className="mt-1 text-2xs text-muted-foreground">输入价格 USD/1M（pricing.prompt 换算）</p>
            <div className="mt-5"><Donut data={price} /></div>
          </div>
          <div className="card-surface p-5 lg:col-span-2">
            <h3 className="text-sm font-semibold text-heading">能力覆盖</h3>
            <div className="mt-5"><BarChart data={caps} /></div>
          </div>
        </div>
      </section>

      <section className="mt-12">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">真实 Featured Models</h2>
        <div className="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
          {featured.map((m) => <ModelCard key={m.id} model={m} />)}
        </div>
      </section>

      <section className="mt-12 grid gap-5 lg:grid-cols-2">
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">最长上下文 Top 5（真实）</h3>
          <ul className="mt-4 space-y-2">
            {topContext.map((m) => (
              <li key={m.id}>
                <Link href={`/models/${encodeURIComponent(m.id)}`} className="focus-ring flex items-center justify-between gap-3 rounded-sm px-2 py-1.5 text-xs hover:bg-card-hover">
                  <span className="truncate font-mono text-muted-foreground">{m.id}</span>
                  <span className="shrink-0 font-mono text-foreground">{(m.context_length / 1_000_000).toFixed(2)}M</span>
                </Link>
              </li>
            ))}
          </ul>
        </div>
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">最低付费输入价格 Top 5（真实）</h3>
          <ul className="mt-4 space-y-2">
            {cheap.map((m) => (
              <li key={m.id}>
                <Link href={`/models/${encodeURIComponent(m.id)}`} className="focus-ring flex items-center justify-between gap-3 rounded-sm px-2 py-1.5 text-xs hover:bg-card-hover">
                  <span className="truncate font-mono text-muted-foreground">{m.id}</span>
                  <span className="shrink-0 font-mono text-foreground">${(Number(m.pricing.prompt) * 1e6).toFixed(4)}</span>
                </Link>
              </li>
            ))}
          </ul>
        </div>
      </section>
    </ConsoleLayout>
  );
}
