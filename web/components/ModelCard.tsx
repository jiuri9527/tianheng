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
import { ProviderBadge } from "@/components/StatusBadge";

export default function ModelCard({
  model,
  trend,
}: {
  model: RealModel;
  /** 真实首页展示的是 Weekly Trend，静态数据集不含该字段；此处不编造，默认不渲染 */
  trend?: { label: string; value: string } | null;
}) {
  const caps = capabilitiesOf(model);
  const provider = providerOf(model.id);
  const chips = [
    caps.vision && "视觉",
    caps.tools && "工具",
    caps.reasoning && "推理",
    caps.structuredOutputs && "结构化",
    caps.caching && "缓存",
  ].filter(Boolean) as string[];

  return (
    <Link
      href={`/models/${encodeURIComponent(model.id)}`}
      className="focus-ring group card-surface flex h-full flex-col gap-4 p-5 transition-[box-shadow,transform,border-color] duration-fast ease-smooth-out hover:border-primary/40 hover:shadow-card-hover motion-safe:hover:-translate-y-0.5"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <ProviderBadge provider={provider} />
          <h3 className="mt-3 truncate font-brand text-base font-semibold tracking-tight text-heading">
            {model.name}
          </h3>
          <p className="mt-1 truncate font-mono text-2xs text-muted-foreground">
            {model.id}
          </p>
        </div>
        <svg
          viewBox="0 0 16 16"
          fill="none"
          aria-hidden="true"
          className="mt-1 size-4 shrink-0 text-muted-foreground transition-transform duration-quick ease-smooth-out group-hover:translate-x-0.5"
        >
          <path d="M3 8h10M9 4l4 4-4 4" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </div>

      <p className="line-clamp-2 text-sm leading-relaxed text-muted-foreground">
        {model.description}
      </p>

      <div className="mt-auto flex flex-wrap gap-1.5">
        {chips.map((c) => (
          <span
            key={c}
            className="rounded-sm border border-border bg-muted/60 px-1.5 py-0.5 text-2xs text-muted-foreground"
          >
            {c}
          </span>
        ))}
        {chips.length === 0 && (
          <span className="rounded-sm border border-border bg-muted/60 px-1.5 py-0.5 text-2xs text-muted-foreground">
            text→text
          </span>
        )}
      </div>

      <div className="grid grid-cols-3 gap-2 border-t border-border pt-4">
        <div>
          <p className="text-overline uppercase text-muted-foreground">上下文</p>
          <p className="mt-1 font-mono text-sm font-medium tabular-nums text-foreground">
            {formatContext(model.context_length)}
          </p>
        </div>
        <div>
          <p className="text-overline uppercase text-muted-foreground">输入 / 1M</p>
          <p className="mt-1 font-mono text-sm font-medium tabular-nums text-foreground">
            {formatUsd(promptPricePerMtok(model))}
          </p>
        </div>
        <div>
          <p className="text-overline uppercase text-muted-foreground">输出 / 1M</p>
          <p className="mt-1 font-mono text-sm font-medium tabular-nums text-foreground">
            {formatUsd(completionPricePerMtok(model))}
          </p>
        </div>
      </div>

      {trend && (
        <div className="flex items-center justify-between text-2xs text-muted-foreground">
          <span>{trend.label}</span>
          <span className="font-mono text-positive-text">{trend.value}</span>
        </div>
      )}
    </Link>
  );
}
