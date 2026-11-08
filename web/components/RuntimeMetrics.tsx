"use client";

import { useEffect, useState } from "react";
import { api, UNAVAILABLE_HINT } from "@/lib/api";
import type { DashboardStats } from "@/lib/types";
import StatCard from "@/components/StatCard";

/** F10 运行时流量指标：只展示 /api/admin/dashboard 的真实值，未连接时显示 — 并说明来源 */
export default function RuntimeMetrics() {
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [state, setState] = useState<"loading" | "ok" | "unavailable" | "error">("loading");
  const [message, setMessage] = useState("");

  useEffect(() => {
    let alive = true;
    api.dashboard().then((res) => {
      if (!alive) return;
      if (res.unavailable) { setState("unavailable"); setMessage(UNAVAILABLE_HINT); return; }
      if (!res.ok) { setState("error"); setMessage(res.error?.message ?? `HTTP ${res.status}`); return; }
      setStats(res.data);
      setState("ok");
    });
    return () => { alive = false; };
  }, []);

  const num = (v: number | undefined, suffix = "") =>
    v === undefined ? "—" : `${v}${suffix}`;

  return (
    <section aria-label="运行时流量指标">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <h2 className="font-brand text-base font-semibold text-heading">运行时流量指标 (F10)</h2>
        <span className={`rounded-full border px-2.5 py-1 font-mono text-2xs ${
          state === "ok" ? "border-positive/30 bg-positive/8 text-positive-text" : "border-border bg-muted/60 text-muted-foreground"
        }`}>
          {state === "ok" ? "GET /api/admin/dashboard · live" : "GET /api/admin/dashboard"}
        </span>
      </div>
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <StatCard label="QPS" value={num(stats?.qps)} sub="每秒请求数" />
        <StatCard label="成功率" value={num(stats?.success_rate, "%")} sub="2xx / 全部请求" tone="positive" />
        <StatCard label="平均 TTFT" value={stats ? `${stats.avg_ttft_ms} ms` : "—"} sub="首字延迟" />
        <StatCard label="总 tokens" value={stats ? stats.total_tokens.toLocaleString() : "—"} sub="prompt + completion" />
        <StatCard label="总花费" value={stats ? `$${stats.total_cost_usd.toFixed(4)}` : "—"} sub="USD" tone="warning" />
        <StatCard label="活跃渠道 / 模型" value={stats ? `${stats.active_channels} / ${stats.total_models}` : "—"} sub="健康候选集" />
      </div>
      {state !== "ok" && (
        <p className="mt-4 rounded-lg border border-border bg-muted/30 px-4 py-3 font-mono text-2xs leading-relaxed text-muted-foreground">
          {state === "loading" ? "读取中…" : message} · 本区不填充编造的调用量；下方「模型池统计」由 data/models-real.json 真实派生。
        </p>
      )}
      {state === "ok" && stats && (
        <div className="mt-4 grid gap-4 lg:grid-cols-2">
          <div className="card-surface p-5">
            <p className="text-overline uppercase tracking-widest text-muted-foreground">渠道健康矩阵</p>
            <ul className="mt-3 space-y-2">
              {stats.channel_health.map((c) => (
                <li key={c.name} className="flex items-center justify-between gap-3 text-xs">
                  <span className="font-mono text-muted-foreground">{c.name}</span>
                  <span className="flex items-center gap-2">
                    <span className={`size-2 rounded-full ${c.status === "active" ? "bg-positive" : "bg-negative"}`} />
                    <span className="font-mono text-foreground">{c.latency_ms != null ? `${c.latency_ms} ms` : "—"}</span>
                  </span>
                </li>
              ))}
            </ul>
          </div>
          <div className="card-surface p-5">
            <p className="text-overline uppercase tracking-widest text-muted-foreground">Top 模型（实时）</p>
            <ul className="mt-3 space-y-2">
              {stats.top_models.map((m) => (
                <li key={m.label} className="flex items-center justify-between gap-3 text-xs">
                  <span className="truncate font-mono text-muted-foreground">{m.label}</span>
                  <span className="font-mono text-foreground">{m.value}</span>
                </li>
              ))}
            </ul>
          </div>
        </div>
      )}
    </section>
  );
}
