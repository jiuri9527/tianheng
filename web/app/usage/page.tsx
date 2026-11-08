import type { Metadata } from "next";
import {
  capabilityCoverage,
  contextBands,
  datasetSummary,
  modalityDistribution,
  priceBands,
  providerDistribution,
} from "@/lib/stats";
import ConsoleLayout from "@/components/ConsoleLayout";
import StatCard from "@/components/StatCard";
import UsageRuntime from "@/components/UsageRuntime";
import { BarChart, Donut, LineChart } from "@/components/Chart";

export const metadata: Metadata = { title: "用量" };

export default function UsagePage() {
  const summary = datasetSummary();
  const providers = providerDistribution(10);
  const contexts = contextBands();
  const prices = priceBands();
  const caps = capabilityCoverage();
  const modalities = modalityDistribution();

  return (
    <ConsoleLayout
      eyebrow="Usage · F10"
      title="用量与模型池分析"
      description="上半区为 GET /api/usage 的真实调用明细（后端未连接时为空态）；下半区全部由 data/models-real.json 的 460 条真实条目派生，不使用任何编造的调用量或花费。"
    >
      <UsageRuntime />

      <div className="mt-10 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="真实模型" value={summary.models} sub="data/models-real.json" />
        <StatCard label="定价字段种类" value={summary.distinctPricingKeys} sub="pricing keys" />
        <StatCard label="模态组合" value={summary.distinctModalities} sub="architecture.modality" />
        <StatCard label="平均上下文" value={`${Math.round(summary.avgContext / 1000)}K`} sub="真实条目均值" />
      </div>

      <div className="mt-8 grid gap-5 lg:grid-cols-2">
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">供应商模型数 Top 10</h3>
          <p className="mt-1 text-2xs text-muted-foreground">按真实 id 前缀统计（共 {summary.providers} 家）</p>
          <div className="mt-5"><BarChart data={providers} /></div>
        </div>
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">上下文长度分布</h3>
          <p className="mt-1 text-2xs text-muted-foreground">context_length 真实分桶</p>
          <div className="mt-5"><LineChart data={contexts} /></div>
        </div>
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">价格分桶（输入 USD/1M）</h3>
          <p className="mt-1 text-2xs text-muted-foreground">由 pricing.prompt × 1e6 换算；-1 记为动态定价</p>
          <div className="mt-5"><BarChart data={prices} /></div>
        </div>
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">模态构成</h3>
          <p className="mt-1 text-2xs text-muted-foreground">architecture.input/output_modalities 派生</p>
          <div className="mt-5"><Donut data={modalities} /></div>
        </div>
      </div>

      <div className="mt-8 card-surface p-5">
        <h3 className="text-sm font-semibold text-heading">能力覆盖（supported_parameters / architecture 派生）</h3>
        <div className="mt-5"><BarChart data={caps} /></div>
      </div>

      <p className="mt-8 font-mono text-2xs text-muted-foreground">
        F10 指标：llmhub_requests_total / llmhub_ttft_seconds / llmhub_tokens_total / llmhub_channel_health · GET /metrics
      </p>
    </ConsoleLayout>
  );
}
