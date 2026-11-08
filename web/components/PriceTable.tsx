import Link from "next/link";
import type { RealModel } from "@/lib/types";
import {
  capabilitiesOf,
  completionPricePerMtok,
  formatContext,
  formatUsd,
  promptPricePerMtok,
  providerOf,
} from "@/lib/models";
import { ScatterPlot } from "@/components/Chart";
import { pricePerformancePoints } from "@/lib/stats";

/** 价格与性能对照（对应真实首页 "Price and Performance — Keep costs in check..." 板块） */
export default function PriceTable({ models }: { models: RealModel[] }) {
  const prices = models.map((m) => promptPricePerMtok(m)).filter((v): v is number => v !== null);
  const maxPrice = Math.max(...prices, 0.01);
  const scatter = pricePerformancePoints()
    .filter((p) => p.prompt !== null)
    .map((p) => ({ id: p.id, label: p.name, x: p.prompt as number, y: p.context }));

  return (
    <div className="space-y-8">
      <div className="card-surface overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-5 py-4">
          <div>
            <h3 className="text-sm font-semibold text-heading">真实模型价格对照</h3>
            <p className="mt-1 text-2xs text-muted-foreground">
              上游原始单位为 USD/token，此处换算为 USD / 1M tokens；-1（动态路由定价）显示为「动态」。
            </p>
          </div>
          <span className="rounded-full border border-border bg-muted px-2.5 py-1 font-mono text-2xs text-muted-foreground">
            source: data/models-real.json
          </span>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[860px] border-collapse text-left">
            <thead>
              <tr className="border-b border-border bg-muted/40 text-overline uppercase tracking-wider text-muted-foreground">
                <th className="px-5 py-3 font-medium">模型 / id</th>
                <th className="px-3 py-3 font-medium">供应商</th>
                <th className="px-3 py-3 font-medium text-right">上下文</th>
                <th className="px-3 py-3 font-medium text-right">输入 $/1M</th>
                <th className="px-3 py-3 font-medium text-right">输出 $/1M</th>
                <th className="px-3 py-3 font-medium">相对价格</th>
                <th className="px-3 py-3 font-medium text-right">最大输出</th>
                <th className="px-3 py-3 font-medium text-right">能力数</th>
              </tr>
            </thead>
            <tbody>
              {models.map((m) => {
                const p = promptPricePerMtok(m);
                const c = completionPricePerMtok(m);
                const caps = capabilitiesOf(m);
                const capCount = Object.values(caps).filter(Boolean).length;
                return (
                  <tr key={m.id} className="border-b border-border/60 transition-colors duration-quick hover:bg-card-hover">
                    <td className="px-5 py-3">
                      <Link
                        href={`/models/${encodeURIComponent(m.id)}`}
                        className="focus-ring block max-w-[16rem] truncate rounded-sm text-sm font-medium text-heading hover:text-primary"
                      >
                        {m.name}
                      </Link>
                      <p className="mt-0.5 max-w-[16rem] truncate font-mono text-2xs text-muted-foreground">
                        {m.id}
                      </p>
                    </td>
                    <td className="px-3 py-3 font-mono text-2xs text-muted-foreground">
                      {providerOf(m.id)}
                    </td>
                    <td className="px-3 py-3 text-right font-mono text-xs tabular-nums text-foreground">
                      {formatContext(m.context_length)}
                    </td>
                    <td className="px-3 py-3 text-right font-mono text-xs tabular-nums text-foreground">
                      {formatUsd(p)}
                    </td>
                    <td className="px-3 py-3 text-right font-mono text-xs tabular-nums text-muted-foreground">
                      {formatUsd(c)}
                    </td>
                    <td className="px-3 py-3">
                      <div className="h-1.5 w-24 overflow-hidden rounded-full bg-muted">
                        <div
                          className="h-full rounded-full bg-primary"
                          style={{ width: `${p === null ? 0 : Math.max(3, (p / maxPrice) * 100)}%` }}
                        />
                      </div>
                    </td>
                    <td className="px-3 py-3 text-right font-mono text-xs tabular-nums text-foreground">
                      {m.top_provider?.max_completion_tokens ?? "—"}
                    </td>
                    <td className="px-3 py-3 text-right font-mono text-xs tabular-nums text-foreground">
                      {capCount}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>

      <div className="card-surface p-5">
        <h3 className="text-sm font-semibold text-heading">价格 × 上下文长度分布</h3>
        <p className="mt-1 text-2xs text-muted-foreground">
          {scatter.length} 个真实模型（排除动态定价条目）；横轴输入价格对数刻度，纵轴上下文长度。
        </p>
        <div className="mt-4">
          <ScatterPlot points={scatter} />
        </div>
      </div>
    </div>
  );
}
