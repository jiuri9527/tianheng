"use client";

import { useEffect, useState } from "react";
import type { DiscoveredModel, ModelProbe, RealModel } from "@/lib/types";
import {
  completionPricePerMtok,
  formatContext,
  formatUsd,
  pricePerMtok,
  promptPricePerMtok,
  providerOf,
} from "@/lib/models";
import { api, UNAVAILABLE_HINT } from "@/lib/api";
import ProbeSampleCard from "@/components/ProbeSampleCard";
import { Badge, ProbeStatusBadge, VisibilityBadge } from "@/components/StatusBadge";

/** 真实数据集条目 → F3「已发现模型」视图对象（不改值，仅投影） */
const toDiscovered = (m: RealModel): DiscoveredModel => ({
  upstream_id: m.id,
  display_name: m.name,
  alias: m.id,
  provider_type: "openai_compatible",
  context_length: m.context_length,
  max_output_tokens: m.top_provider?.max_completion_tokens ?? null,
  input_modalities: m.architecture?.input_modalities ?? [],
  output_modalities: m.architecture?.output_modalities ?? [],
  supports_vision: (m.architecture?.input_modalities ?? []).includes("image"),
  supports_tools: (m.supported_parameters ?? []).some((p) => p === "tools" || p === "tool_choice"),
  supports_reasoning: (m.supported_parameters ?? []).some((p) => p === "reasoning" || p === "include_reasoning"),
  supports_stream: true,
  input_price_per_mtok: promptPricePerMtok(m),
  output_price_per_mtok: completionPricePerMtok(m),
  param_source: "upstream",
  probe_status: "unavailable",
  raw_json: null,
});

interface Props {
  channelId: string;
  provider: string;
  providerLabel: string;
  models: RealModel[];
  totalCount: number;
}

export default function ChannelDetailView({
  channelId,
  provider,
  providerLabel,
  models,
  totalCount,
}: Props) {
  const [mode, setMode] = useState<"loading" | "runtime" | "static">("loading");
  const [runtimeRows, setRuntimeRows] = useState<DiscoveredModel[]>([]);
  const [runtimeProbes, setRuntimeProbes] = useState<Record<string, ModelProbe>>({});
  const [staticRows, setStaticRows] = useState<DiscoveredModel[]>(models.map(toDiscovered));
  const [visible, setVisible] = useState(6);
  const [busy, setBusy] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<{ ok: boolean; message: string } | null>(null);

  useEffect(() => {
    let alive = true;
    api.getChannel(channelId).then((res) => {
      if (!alive) return;
      if (res.ok && res.data) {
        setRuntimeRows(res.data.models ?? []);
        setRuntimeProbes(res.data.probes ?? {});
        setMode("runtime");
      } else {
        setMode("static");
      }
    });
    return () => { alive = false; };
  }, [channelId]);

  const rows = mode === "runtime" ? runtimeRows : staticRows;
  const probes = mode === "runtime" ? runtimeProbes : {};
  const shown = rows.slice(0, visible);

  const run = async (key: string, fn: () => Promise<{ unavailable: boolean; ok: boolean; data: unknown; error: { message: string } | null }>) => {
    setBusy(key);
    setFeedback(null);
    const res = await fn();
    setBusy(null);
    if (res.unavailable) {
      setFeedback({ ok: false, message: `POST 未送达：${UNAVAILABLE_HINT}` });
      return;
    }
    if (!res.ok) {
      setFeedback({ ok: false, message: res.error?.message ?? "请求失败" });
      return;
    }
    setFeedback({ ok: true, message: `执行成功：${JSON.stringify(res.data)}` });
    // 重新拉取运行时真实结果（若有后端）
    const refreshed = await api.getChannel(channelId);
    if (refreshed.ok && refreshed.data) {
      setRuntimeRows(refreshed.data.models ?? []);
      setRuntimeProbes(refreshed.data.probes ?? {});
      setMode("runtime");
    }
  };

  return (
    <div className="space-y-6">
      {/* 渠道状态 + 操作 */}
      <div className="card-surface flex flex-wrap items-center justify-between gap-4 p-5">
        <div className="flex flex-wrap items-center gap-3">
          <Badge tone={mode === "runtime" ? "positive" : "info"}>
            {mode === "runtime" ? "实时渠道数据" : mode === "loading" ? "读取中…" : "真实数据集离线投影"}
          </Badge>
          <VisibilityBadge visibility="public" />
          <span className="font-mono text-2xs text-muted-foreground">
            channel_id={channelId} · provider={provider} ({providerLabel})
          </span>
        </div>
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            disabled={busy !== null}
            onClick={() => run("sync", () => api.syncChannelModels(channelId))}
            className="focus-ring rounded-md border border-border bg-card px-3 py-2 text-button font-medium text-foreground transition-colors duration-quick hover:bg-card-hover disabled:opacity-50"
          >
            {busy === "sync" ? "同步中…" : "重新同步"}
          </button>
          <button
            type="button"
            disabled={busy !== null}
            onClick={() => run("probe", () => api.probeChannel(channelId))}
            className="focus-ring rounded-md border border-border bg-card px-3 py-2 text-button font-medium text-foreground transition-colors duration-quick hover:bg-card-hover disabled:opacity-50"
          >
            {busy === "probe" ? "探测中…" : "重新探测"}
          </button>
          <button
            type="button"
            disabled={busy !== null}
            onClick={() => run("test", () => api.testChannel(channelId))}
            className="focus-ring rounded-md bg-primary px-3 py-2 text-button font-medium text-primary-foreground transition-[filter] duration-quick hover:brightness-110 disabled:opacity-50"
          >
            {busy === "test" ? "测试中…" : "测试连接"}
          </button>
        </div>
      </div>

      <div className="rounded-lg border border-info/20 bg-info/8 px-4 py-3 text-xs leading-relaxed text-info-text">
        {mode === "runtime" ? (
          <>模型清单与探测结果来自 <code className="font-mono">GET /api/channels/{channelId}</code> 的真实上游发现。</>
        ) : (
          <>
            后端 /api/* 未连接，渠道模型列表由 <code className="font-mono">data/models-real.json</code> 中
            <code className="font-mono"> provider={provider} </code> 的真实条目投影展示（共 {totalCount} 条，本页载入 {models.length} 条）。
            所有条目 <code className="font-mono">probe_status=unavailable</code>，不填充任何编造的输入/输出样例与延迟。
          </>
        )}
      </div>

      {feedback && (
        <div className={`rounded-lg border px-4 py-3 text-sm ${feedback.ok ? "border-positive/30 bg-positive/8 text-positive-text" : "border-warning/30 bg-warning/8 text-warning-text"}`} role="status">
          {feedback.message}
        </div>
      )}

      {/* 已发现真实模型表 */}
      <div className="card-surface overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-5 py-4">
          <h3 className="text-sm font-semibold text-heading">已发现的真实模型</h3>
          <span className="font-mono text-2xs text-muted-foreground">
            {rows.length} 条 · source: data/models-real.json
          </span>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[900px] border-collapse text-left">
            <thead>
              <tr className="border-b border-border bg-muted/40 text-overline uppercase tracking-wider text-muted-foreground">
                <th className="px-4 py-3 font-medium">upstream_id</th>
                <th className="px-3 py-3 font-medium">display_name</th>
                <th className="px-3 py-3 font-medium text-right">context</th>
                <th className="px-3 py-3 font-medium text-right">max_output</th>
                <th className="px-3 py-3 font-medium">modality</th>
                <th className="px-3 py-3 font-medium text-right">in $/1M</th>
                <th className="px-3 py-3 font-medium text-right">out $/1M</th>
                <th className="px-3 py-3 font-medium">probe</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((r) => (
                <tr key={r.upstream_id} className="border-b border-border/60 transition-colors duration-quick hover:bg-card-hover">
                  <td className="px-4 py-3 font-mono text-2xs text-foreground">{r.upstream_id}</td>
                  <td className="px-3 py-3 text-xs text-heading">{r.display_name}</td>
                  <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">{formatContext(r.context_length)}</td>
                  <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">{r.max_output_tokens ?? "—"}</td>
                  <td className="px-3 py-3 font-mono text-2xs text-muted-foreground">
                    {[...r.input_modalities].join("+")}→{[...r.output_modalities].join("+")}
                  </td>
                  <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">{formatUsd(r.input_price_per_mtok)}</td>
                  <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">{formatUsd(r.output_price_per_mtok)}</td>
                  <td className="px-3 py-3"><ProbeStatusBadge status={r.probe_status} /></td>
                </tr>
              ))}
              {shown.length === 0 && (
                <tr>
                  <td colSpan={8} className="px-4 py-10 text-center text-sm text-muted-foreground">
                    渠道模型列表为空（probe_status=unavailable），等待真实上游同步。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        {visible < rows.length && (
          <div className="border-t border-border p-4 text-center">
            <button
              type="button"
              onClick={() => setVisible((v) => Math.min(rows.length, v + 6))}
              className="focus-ring rounded-md border border-border px-4 py-2 text-button font-medium text-foreground transition-colors duration-quick hover:bg-card-hover"
            >
              显示更多真实模型（还有 {rows.length - visible} 条）
            </button>
          </div>
        )}
      </div>

      {/* 每个模型的真实参数卡片 + 探测样例 */}
      <div>
        <h3 className="mb-4 font-brand text-base font-semibold text-heading">
          真实参数卡片 · 真实输入/输出样例 · 实测延迟
        </h3>
        <div className="space-y-5">
          {shown.map((r) => {
            const real = models.find((m) => m.id === r.upstream_id);
            return (
              <div key={r.upstream_id} className="space-y-4 rounded-xl border border-border bg-muted/20 p-4 md:p-5">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div>
                    <h4 className="font-brand text-sm font-bold text-heading">{r.display_name}</h4>
                    <p className="mt-1 font-mono text-2xs text-muted-foreground">{r.upstream_id}</p>
                  </div>
                  <div className="flex items-center gap-2">
                    <Badge tone="info" mono>{r.param_source}</Badge>
                    <ProbeStatusBadge status={r.probe_status} />
                    <button
                      type="button"
                      disabled={busy !== null}
                      onClick={() => run(`model:${r.upstream_id}`, () => api.probeModel(r.upstream_id))}
                      className="focus-ring rounded-md border border-border bg-card px-2.5 py-1.5 text-2xs font-medium text-foreground transition-colors duration-quick hover:bg-card-hover disabled:opacity-50"
                    >
                      {busy === `model:${r.upstream_id}` ? "探测中…" : "单模型重探"}
                    </button>
                  </div>
                </div>

                {/* 真实参数卡片 */}
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                  <Param label="context_length" value={String(r.context_length)} />
                  <Param label="max_completion_tokens" value={r.max_output_tokens != null ? String(r.max_output_tokens) : "—"} />
                  <Param label="输入模态" value={r.input_modalities.join(" + ") || "—"} />
                  <Param label="输出模态" value={r.output_modalities.join(" + ") || "—"} />
                  <Param label="tokenizer" value={real?.architecture?.tokenizer ?? "—"} />
                  <Param label="modality" value={real?.architecture?.modality ?? "—"} />
                  <Param label="prompt (USD/token)" value={real ? String(real.pricing?.prompt ?? "—") : "—"} />
                  <Param label="completion (USD/token)" value={real ? String(real.pricing?.completion ?? "—") : "—"} />
                  <Param label="input_cache_read" value={real ? String(pricePerMtok(real.pricing?.["input_cache_read"] as string | undefined) ?? 0) + " /1M" : "—"} />
                  <Param label="supports_vision" value={String(r.supports_vision)} />
                  <Param label="supports_tools" value={String(r.supports_tools)} />
                  <Param label="supports_reasoning" value={String(r.supports_reasoning)} />
                </div>

                {real && (
                  <div className="rounded-lg border border-border bg-card p-4">
                    <p className="text-overline uppercase tracking-widest text-muted-foreground">
                      supported_parameters（{real.supported_parameters.length} 项，逐字来自上游）
                    </p>
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      {real.supported_parameters.map((p) => (
                        <span key={p} className="rounded-sm border border-border bg-muted/60 px-1.5 py-0.5 font-mono text-2xs text-muted-foreground">
                          {p}
                        </span>
                      ))}
                    </div>
                    <p className="mt-3 text-2xs text-muted-foreground">
                      供应商 {providerOf(real.id)} · 全部定价字段：
                      {Object.entries(real.pricing).map(([k, v]) => ` ${k}=${String(v)}`).join(" · ")}
                    </p>
                  </div>
                )}

                <ProbeSampleCard modelId={r.upstream_id} probe={probes[r.upstream_id] ?? null} />
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function Param({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border border-border bg-card px-3 py-2">
      <p className="truncate font-mono text-2xs text-muted-foreground" title={label}>{label}</p>
      <p className="mt-1 truncate font-mono text-xs text-foreground" title={value}>{value}</p>
    </div>
  );
}
