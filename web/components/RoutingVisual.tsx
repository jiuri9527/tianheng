import Link from "next/link";
import type { RealModel } from "@/lib/types";
import { formatContext, formatUsd, promptPricePerMtok } from "@/lib/models";

/**
 * 模型路由可视化（对应真实首页 "Model routing visualization" 板块）
 * 叶子节点全部是 web/data/models.json 中的真实模型 id；策略/熔断参数取自
 * LLM-API-Aggregator-Prompt.md F8 的规范常量，不编造运行时数值。
 */
export default function RoutingVisual({ models }: { models: RealModel[] }) {
  const channels = [
    { key: "openai", label: "OpenAI / ChatGPT", auth: "Bearer", tone: "positive" },
    { key: "anthropic", label: "Anthropic / Claude", auth: "x-api-key", tone: "warning" },
    { key: "xai", label: "xAI / Grok", auth: "Bearer", tone: "info" },
    { key: "google", label: "Google Gemini", auth: "Bearer", tone: "negative" },
    { key: "openrouter", label: "OpenAI 兼容 / 自定义", auth: "Bearer", tone: "neutral" },
  ] as const;

  const byProvider = (key: string) =>
    models.filter((m) => m.id.split("/")[0] === key);

  return (
    <div className="card-surface overflow-hidden">
      {/* 顶层链路 */}
      <div className="border-b border-border bg-muted/30 px-5 py-6 md:px-8">
        <div className="grid items-stretch gap-3 md:grid-cols-[1fr_auto_1fr_auto_1fr]">
          <Stage
            index="01"
            title="客户端"
            subtitle="Bearer sk-xxxx"
            detail="一个平台令牌，一个地址"
          />
          <Arrow />
          <Stage
            index="02"
            title="统一出口 /v1"
            subtitle="chat/completions · messages · responses"
            detail="OpenAI / Anthropic 双协议入口"
            highlight
          />
          <Arrow />
          <Stage
            index="03"
            title="路由内核"
            subtitle="selector · failover · circuit breaker"
            detail="解析 model → 候选渠道 → 选中"
          />
        </div>
      </div>

      {/* 选择器策略 */}
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-5 py-4 md:px-8">
        <span className="text-overline uppercase tracking-widest text-muted-foreground">
          选择策略 (F8)
        </span>
        {["weighted_round_robin", "least_in_flight", "priority"].map((s, i) => (
          <span
            key={s}
            className={`rounded-full border px-2.5 py-1 font-mono text-2xs ${
              i === 0
                ? "border-primary/30 bg-primary/10 text-primary"
                : "border-border bg-muted/60 text-muted-foreground"
            }`}
          >
            {s}
          </span>
        ))}
        <span className="ml-auto flex flex-wrap gap-2 font-mono text-2xs text-muted-foreground">
          <span className="rounded-sm border border-border px-2 py-1">MAX_FAILOVER=3</span>
          <span className="rounded-sm border border-border px-2 py-1">CIRCUIT_COOLDOWN_SEC=30</span>
          <span className="rounded-sm border border-border px-2 py-1">HEALTH_CHECK=60s</span>
        </span>
      </div>

      {/* 渠道 + 真实模型叶节点 */}
      <div className="grid gap-4 p-5 md:grid-cols-2 md:p-8 xl:grid-cols-3">
        {channels.map((ch) => {
          const list = byProvider(ch.key);
          return (
            <div key={ch.key} className="rounded-lg border border-border bg-background p-4">
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  <span className="size-2 rounded-full bg-primary" aria-hidden="true" />
                  <h4 className="text-sm font-semibold text-heading">{ch.label}</h4>
                </div>
                <span className="font-mono text-2xs text-muted-foreground">{ch.auth}</span>
              </div>
              <div className="mt-3 space-y-1.5">
                {list.length > 0 ? (
                  list.map((m) => (
                    <Link
                      key={m.id}
                      href={`/models/${encodeURIComponent(m.id)}`}
                      className="focus-ring flex items-center justify-between gap-2 rounded-sm border border-border/70 bg-card px-2.5 py-2 transition-colors duration-quick hover:border-primary/40 hover:bg-card-hover"
                    >
                      <span className="truncate font-mono text-2xs text-foreground">{m.id}</span>
                      <span className="shrink-0 font-mono text-2xs text-muted-foreground">
                        {formatContext(m.context_length)}
                      </span>
                    </Link>
                  ))
                ) : (
                  <p className="rounded-sm border border-dashed border-border px-2.5 py-2 font-mono text-2xs text-muted-foreground">
                    上游 GET /models 发现后自动入池
                  </p>
                )}
              </div>
              <p className="mt-3 font-mono text-2xs text-muted-foreground/70">
                {formatUsd(list.length ? promptPricePerMtok(list[0]) : null)} / 1M input (示例真实模型)
              </p>
            </div>
          );
        })}

        <div className="rounded-lg border border-dashed border-border bg-dots p-4">
          <h4 className="text-sm font-semibold text-heading">协议适配器 (F7)</h4>
          <ul className="mt-3 space-y-2 text-xs text-muted-foreground">
            <li>OpenAICompatAdapter · 透传 + 路径/Header 覆盖</li>
            <li>AnthropicAdapter · OpenAI ↔ Messages 双向转换</li>
            <li>GeminiAdapter · OpenAI 兼容层</li>
            <li>StreamIterator · 统一 chunk + SSE 背压</li>
          </ul>
          <p className="mt-3 font-mono text-2xs text-muted-foreground/70">
            tool_calls ↔ tool_use · stop_reason ↔ finish_reason
          </p>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-x-6 gap-y-2 border-t border-border bg-muted/30 px-5 py-4 text-2xs text-muted-foreground md:px-8">
        <span className="flex items-center gap-2">
          <span className="h-px w-6 border-t-2 border-dashed border-primary" /> 流式 SSE 转发 (F6)
        </span>
        <span className="flex items-center gap-2">
          <span className="size-2 rounded-full bg-positive" /> 健康候选
        </span>
        <span className="flex items-center gap-2">
          <span className="size-2 rounded-full bg-negative" /> 429/5xx 熔断并切换
        </span>
        <span className="ml-auto font-mono">source: data/models-real.json</span>
      </div>
    </div>
  );
}

function Stage({
  index,
  title,
  subtitle,
  detail,
  highlight = false,
}: {
  index: string;
  title: string;
  subtitle: string;
  detail: string;
  highlight?: boolean;
}) {
  return (
    <div
      className={`rounded-lg border p-4 ${
        highlight ? "border-primary/40 bg-primary/5" : "border-border bg-card"
      }`}
    >
      <p className="font-mono text-overline text-muted-foreground">{index}</p>
      <h4 className="mt-1.5 font-brand text-sm font-bold text-heading">{title}</h4>
      <p className="mt-1 break-all font-mono text-2xs text-primary">{subtitle}</p>
      <p className="mt-2 text-2xs leading-relaxed text-muted-foreground">{detail}</p>
    </div>
  );
}

function Arrow() {
  return (
    <div className="flex items-center justify-center" aria-hidden="true">
      <svg viewBox="0 0 40 12" className="h-3 w-10 rotate-90 text-primary md:rotate-0">
        <line x1="0" y1="6" x2="34" y2="6" stroke="currentColor" strokeWidth="1.6" strokeDasharray="5 4" className="animate-flow-dash" />
        <path d="M32 2l6 4-6 4" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </div>
  );
}
