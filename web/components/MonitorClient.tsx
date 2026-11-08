// 文件: web/components/MonitorClient.tsx
// 实时监控客户端：GET /api/telemetry/snapshot 首屏 + EventSource SSE 实时推送
// （event: snapshot）+ 定时轮询兜底。只渲染后端真实聚合数字；后端不可达或
// available=false 时显示真实空态，绝不填充编造的 QPS/成功率/TTFT/token/曲线点。
"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import StatCard from "@/components/StatCard";
import EmptyState from "@/components/EmptyState";
import { MonitorLineChart, MonitorDualLineChart, MonitorBarList, type Point } from "@/components/MonitorCharts";
import type { TelemetrySnapshot, MonitorGroupStat } from "@/components/MonitorTypes";

const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "";
const SNAPSHOT_URL = `${API_BASE}/api/telemetry/snapshot`;
const STREAM_URL = `${API_BASE}/api/telemetry/stream`;
const POLL_MS = 5000; // SSE 断开时的定时刷新兜底

const PRIMARY = "rgb(var(--primary))";
const INFO = "rgb(var(--info))";

function pointsOf(series: TelemetrySnapshot["series"], pick: (b: TelemetrySnapshot["series"][number]) => number): Point[] {
  return series.map((b) => ({ label: b.label, value: Number.isFinite(pick(b)) ? pick(b) : 0 }));
}

function groupRows(g: MonitorGroupStat[]): { key: string; value: number }[] {
  return (g ?? []).map((x) => ({ key: x.key, value: x.requests }));
}

function fmtNum(v: number | undefined, digits = 0): string {
  if (v === undefined || !Number.isFinite(v)) return "0";
  return v.toLocaleString(undefined, { maximumFractionDigits: digits, minimumFractionDigits: 0 });
}

function fmtTime(d: Date | null): string {
  if (!d) return "—";
  return d.toLocaleTimeString(undefined, { hour12: false });
}

export default function MonitorClient() {
  const [snap, setSnap] = useState<TelemetrySnapshot | null>(null);
  const [lastRefresh, setLastRefresh] = useState<Date | null>(null);
  const [conn, setConn] = useState<"loading" | "sse" | "poll" | "unavailable">("loading");
  const [message, setMessage] = useState("");
  const aliveRef = useRef(true);

  const apply = useCallback((data: TelemetrySnapshot, via: "sse" | "poll") => {
    if (!aliveRef.current) return;
    setSnap(data);
    setLastRefresh(new Date());
    setConn(via);
  }, []);

  const fetchSnapshot = useCallback(async () => {
    try {
      const res = await fetch(SNAPSHOT_URL, { cache: "no-store" });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = (await res.json()) as TelemetrySnapshot;
      apply(data, "poll");
      setMessage("");
    } catch (e) {
      if (!aliveRef.current) return;
      setConn("unavailable");
      setMessage(e instanceof Error ? e.message : "snapshot 读取失败");
    }
  }, [apply]);

  useEffect(() => {
    aliveRef.current = true;
    void fetchSnapshot();

    // 实时 SSE：后端推送 event: snapshot，故显式监听自定义事件；同时兼容默认 message。
    let es: EventSource | null = null;
    try {
      es = new EventSource(STREAM_URL);
      const onFrame = (ev: Event) => {
        try {
          const me = ev as MessageEvent<string>;
          const data = JSON.parse(me.data) as TelemetrySnapshot;
          apply(data, "sse");
          setMessage("");
        } catch {
          /* 忽略单个坏帧，下一帧继续 */
        }
      };
      es.addEventListener("snapshot", onFrame as EventListener);
      es.addEventListener("message", onFrame as EventListener);
      es.onerror = () => {
        // EventSource 会自动重连；期间用轮询兜底，并把连接态标为 poll。
        if (aliveRef.current) setConn((c) => (c === "sse" ? "poll" : c));
      };
    } catch {
      /* 环境不支持 EventSource 时退化为纯轮询 */
    }

    // 定时刷新兜底（SSE 断开、代理不支持流式、或首屏失败重试）。
    const timer = window.setInterval(() => { void fetchSnapshot(); }, POLL_MS);

    return () => {
      aliveRef.current = false;
      window.clearInterval(timer);
      if (es) es.close();
    };
  }, [apply, fetchSnapshot]);

  const empty = !snap || !snap.available;
  const series = snap?.series ?? [];
  const g = snap?.groups;

  const connBadge =
    conn === "sse" ? "SSE · live" :
    conn === "poll" ? "轮询 · poll" :
    conn === "unavailable" ? "后端未连接" : "读取中…";

  return (
    <section aria-label="实时监控面板" className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <span className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 font-mono text-2xs ${
            conn === "sse" ? "border-positive/30 bg-positive/8 text-positive-text" : "border-border bg-muted/60 text-muted-foreground"
          }`}>
            <span className={`size-1.5 rounded-full ${conn === "sse" ? "bg-positive" : "bg-muted-foreground"}`} />
            {connBadge}
          </span>
          <span className="rounded-full border border-border bg-muted/40 px-2.5 py-1 font-mono text-2xs text-muted-foreground">
            public · 无需 JWT
          </span>
        </div>
        <div className="flex items-center gap-3">
          <span className="font-mono text-2xs text-muted-foreground">
            最后刷新：<span className="text-foreground tabular-nums">{fmtTime(lastRefresh)}</span>
          </span>
          <button
            type="button"
            onClick={() => void fetchSnapshot()}
            className="focus-ring rounded-md border border-border px-3 py-1.5 text-button font-medium text-foreground hover:bg-card-hover"
          >
            刷新
          </button>
        </div>
      </div>

      {/* 数据来源说明：全部来自真实 request_logs 聚合 */}
      <p className="rounded-lg border border-border bg-muted/30 px-4 py-2.5 font-mono text-2xs leading-relaxed text-muted-foreground">
        数据源：{snap?.source || "store.request_logs/ListRequestLogs（真实调用日志聚合）"} · 仅聚合数字，不回显完整输入输出 / api_key / sk- 令牌 / JWT / 密码哈希 · 无数据为真实空态（全 0）。
      </p>

      {/* 真实空态：后端不可达 或 available=false（无日志） */}
      {(empty || conn === "unavailable") && (
        <EmptyState
          title={conn === "unavailable" ? "监控后端未连接" : "暂无真实调用数据"}
          description={
            conn === "unavailable"
              ? `GET /api/telemetry/snapshot 与 SSE /api/telemetry/stream 暂不可达（${message}）。页面不填充编造的 QPS / 成功率 / TTFT / token / 曲线点；下方卡片与曲线保持真实全 0 空态。`
              : "request_logs 中还没有真实调用记录，因此 available=false、所有聚合值为真实 0、曲线为贴底平线。产生真实 /v1 调用后，面板会经 SSE 每 2 秒自动刷新。"
          }
          hint="GET /api/telemetry/snapshot · GET /api/telemetry/stream"
        />
      )}

      {/* 总览数字卡（真实聚合值；空态为 0） */}
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="总请求数" value={fmtNum(snap?.total_requests)} sub="request_logs 全量" />
        <StatCard label="QPS" value={fmtNum(snap?.qps, 3)} sub={`最近 ${snap?.qps_window_seconds ?? 60}s / 峰值 ${fmtNum(snap?.peak_qps, 3)}`} tone="info" />
        <StatCard label="成功率" value={`${fmtNum(snap?.success_rate, 1)}%`} sub={`2xx ${fmtNum(snap?.success_count)} / 失败 ${fmtNum(snap?.error_count)}`} tone="positive" />
        <StatCard label="平均 TTFT" value={`${fmtNum(snap?.avg_ttft_ms, 1)} ms`} sub={`最大 ${fmtNum(snap?.max_ttft_ms)} ms · 样本 ${fmtNum(snap?.ttft_sample_count)}`} tone="warning" />
        <StatCard label="prompt tokens" value={fmtNum(snap?.prompt_tokens)} sub="输入 token 总量" />
        <StatCard label="completion tokens" value={fmtNum(snap?.completion_tokens)} sub="输出 token 总量" />
        <StatCard label="总 tokens" value={fmtNum(snap?.total_tokens)} sub="prompt + completion" tone="info" />
        <StatCard label="流式请求" value={fmtNum(snap?.stream_requests)} sub="stream=true 调用数" />
      </div>

      {/* 多条真实曲线 */}
      <div className="grid gap-5 lg:grid-cols-2">
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">QPS · 最近 60 分钟（每分钟）</h3>
          <p className="mt-1 text-2xs text-muted-foreground">每分钟 requests / 60，来自真实日志分钟桶</p>
          <div className="mt-4"><MonitorLineChart points={pointsOf(series, (b) => b.qps)} unit="" decimals={3} /></div>
        </div>
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">成功率 · 最近 60 分钟（每分钟）</h3>
          <p className="mt-1 text-2xs text-muted-foreground">2xx / 全部，百分比 0–100</p>
          <div className="mt-4"><MonitorLineChart points={pointsOf(series, (b) => b.success_rate)} unit="%" tone="positive" decimals={1} /></div>
        </div>
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">平均 TTFT · 最近 60 分钟（每分钟）</h3>
          <p className="mt-1 text-2xs text-muted-foreground">仅统计 TTFT&gt;0 的样本，单位 ms</p>
          <div className="mt-4"><MonitorLineChart points={pointsOf(series, (b) => b.avg_ttft_ms)} unit="ms" tone="warning" decimals={1} /></div>
        </div>
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">token 消耗 · prompt vs completion</h3>
          <p className="mt-1 text-2xs text-muted-foreground">每分钟真实 token 量</p>
          <div className="mt-4">
            <MonitorDualLineChart
              a={{ points: pointsOf(series, (b) => b.prompt_tokens), name: "prompt", tone: PRIMARY }}
              b={{ points: pointsOf(series, (b) => b.completion_tokens), name: "completion", tone: INFO }}
            />
          </div>
        </div>
      </div>

      {/* 按维度分组（真实聚合） */}
      <div className="grid gap-5 lg:grid-cols-2">
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">按模型分布（by_model）</h3>
          <div className="mt-4"><MonitorBarList rows={groupRows(g?.by_model ?? [])} /></div>
        </div>
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">按供应商分布（by_provider）</h3>
          <div className="mt-4"><MonitorBarList rows={groupRows(g?.by_provider ?? [])} /></div>
        </div>
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">按渠道分布（by_channel）</h3>
          <div className="mt-4"><MonitorBarList rows={groupRows(g?.by_channel ?? [])} /></div>
        </div>
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">按状态分布（by_status）</h3>
          <div className="mt-4"><MonitorBarList rows={groupRows(g?.by_status ?? [])} /></div>
        </div>
      </div>
    </section>
  );
}
