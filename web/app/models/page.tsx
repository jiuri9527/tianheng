import Link from "next/link";
import type { Metadata } from "next";
import { CAPABILITY_LABELS, getProviders, queryModels, type ModelSortKey, type Capabilities } from "@/lib/models";
import { datasetSummary } from "@/lib/stats";
import ConsoleLayout from "@/components/ConsoleLayout";
import FilterBar from "@/components/FilterBar";
import ModelCard from "@/components/ModelCard";
import Pagination from "@/components/Pagination";
import StatCard from "@/components/StatCard";

export const metadata: Metadata = { title: "模型广场" };

type SP = Record<string, string | string[] | undefined>;
const one = (v: string | string[] | undefined): string =>
  Array.isArray(v) ? (v[0] ?? "") : (v ?? "");

export default function ModelsPage({ searchParams }: { searchParams: SP }) {
  const q = one(searchParams.q);
  const provider = one(searchParams.provider) || "all";
  const rawCapability = one(searchParams.capability) || "all";
  const capability: keyof Capabilities | "all" =
    rawCapability === "all"
      ? "all"
      : CAPABILITY_LABELS.some((c) => c.key === rawCapability)
        ? (rawCapability as keyof Capabilities)
        : "all";
  const sort = (one(searchParams.sort) || "context-desc") as ModelSortKey;
  const page = Math.max(1, Number(one(searchParams.page)) || 1);
  const pageSize = 24;

  const result = queryModels({ q, provider, capability, sort, page, pageSize });
  const providers = getProviders();
  const summary = datasetSummary();

  const hrefFor = (p: number) => {
    const params = new URLSearchParams();
    if (q) params.set("q", q);
    if (provider !== "all") params.set("provider", provider);
    if (capability !== "all") params.set("capability", capability);
    if (sort !== "context-desc") params.set("sort", sort);
    if (p > 1) params.set("page", String(p));
    const s = params.toString();
    return s ? `/models?${s}` : "/models";
  };

  return (
    <ConsoleLayout
      eyebrow="Model Plaza · F12"
      title="模型广场"
      description="全局聚合模型池。全部 460 条逐字来自 data/models-real.json，按供应商 / 能力 / 价格 / 上下文筛选；运行时渠道模型由 GET /api/models 追加，未连接时静态池仍完整可用。"
      actions={
        <Link href="/channels" className="focus-ring rounded-md bg-primary px-4 py-2 text-button font-medium text-primary-foreground no-underline transition-[filter] duration-quick hover:brightness-110">
          贡献渠道
        </Link>
      }
    >
      <div className="mb-8 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="真实模型" value={summary.models} sub="source: data/models-real.json" />
        <StatCard label="供应商" value={summary.providers} sub="真实前缀统计" />
        <StatCard label="免费模型" value={summary.freeModels} sub="prompt=0 且 completion=0" tone="positive" />
        <StatCard label="最长上下文" value={(summary.maxContext / 1_000_000).toFixed(2) + "M"} sub={summary.maxContextModelId} />
      </div>

      <FilterBar
        providers={providers}
        initial={{ q, provider, capability, sort }}
      />

      <div className="mt-6 flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          命中 <span className="font-mono font-semibold text-foreground">{result.total}</span> 条真实模型
          {provider !== "all" && <> · 供应商 <span className="font-mono">{provider}</span></>}
          {capability !== "all" && <> · 能力 <span className="font-mono">{capability}</span></>}
        </p>
        <span className="font-mono text-2xs text-muted-foreground">pageSize={pageSize}</span>
      </div>

      {result.items.length > 0 ? (
        <div className="mt-5 grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
          {result.items.map((m) => <ModelCard key={m.id} model={m} />)}
        </div>
      ) : (
        <div className="mt-5 rounded-lg border border-dashed border-border px-6 py-16 text-center text-sm text-muted-foreground">
          该筛选条件下无真实模型。清空条件后可见全部 {summary.models} 条。
        </div>
      )}

      <Pagination page={result.page} pages={result.pages} total={result.total} hrefFor={hrefFor} />

      <div className="mt-10 rounded-lg border border-border bg-muted/30 px-5 py-4 font-mono text-2xs leading-relaxed text-muted-foreground">
        data source: web/data/models.json ← data/models-real.json（HTTP 200，SHA-256 {summary.sha256}）
        <br />
        每个模型 id / name / context_length / pricing / architecture 均经 scripts/verify-models.mjs 逐条 deep-equal 校验。
      </div>
    </ConsoleLayout>
  );
}
