"use client";

import { useEffect, useState } from "react";
import { api, UNAVAILABLE_HINT } from "@/lib/api";
import type { RequestLog } from "@/lib/types";
import EmptyState from "@/components/EmptyState";

export default function LogsTable() {
  const [requestId, setRequestId] = useState("");
  const [alias, setAlias] = useState("");
  const [status, setStatus] = useState("");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const [rows, setRows] = useState<RequestLog[]>([]);
  const [total, setTotal] = useState(0);
  const [state, setState] = useState<"idle" | "loading" | "unavailable" | "error" | "ok">("idle");
  const [message, setMessage] = useState("");

  const load = async (p = 1) => {
    setState("loading");
    const res = await api.logs({ request_id: requestId, alias, status, q, page: p });
    if (res.unavailable) {
      setState("unavailable");
      setMessage(UNAVAILABLE_HINT);
      setRows([]);
      setTotal(0);
      return;
    }
    if (!res.ok) {
      setState("error");
      setMessage(res.error?.message ?? `HTTP ${res.status}`);
      setRows([]);
      return;
    }
    setRows(res.data?.items ?? []);
    setTotal(res.data?.total ?? 0);
    setState("ok");
  };

  useEffect(() => {
    void load(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const inputCls = "focus-ring w-full rounded-md border border-border bg-input-bg px-3 py-2 text-sm text-foreground transition-colors duration-quick hover:border-primary/40";
  const labelCls = "mb-1.5 block text-overline uppercase tracking-widest text-muted-foreground";

  return (
    <div className="space-y-5">
      <div className="card-surface p-4">
        <form
          onSubmit={(e) => { e.preventDefault(); setPage(1); void load(1); }}
          className="grid gap-3 md:grid-cols-[1.2fr_1fr_0.8fr_1fr_auto]"
        >
          <div>
            <label htmlFor="log-rid" className={labelCls}>request_id</label>
            <input id="log-rid" value={requestId} onChange={(e) => setRequestId(e.target.value)} placeholder="全链路 ID" className={`${inputCls} font-mono text-xs`} />
          </div>
          <div>
            <label htmlFor="log-alias" className={labelCls}>alias / model</label>
            <input id="log-alias" value={alias} onChange={(e) => setAlias(e.target.value)} placeholder="真实模型 id" className={`${inputCls} font-mono text-xs`} />
          </div>
          <div>
            <label htmlFor="log-status" className={labelCls}>status_code</label>
            <select id="log-status" value={status} onChange={(e) => setStatus(e.target.value)} className={inputCls}>
              <option value="">全部</option>
              {[200, 400, 401, 402, 403, 404, 429, 500, 502, 504].map((s) => (
                <option key={s} value={s}>{s}</option>
              ))}
            </select>
          </div>
          <div>
            <label htmlFor="log-q" className={labelCls}>关键字</label>
            <input id="log-q" value={q} onChange={(e) => setQ(e.target.value)} placeholder="完整输入/输出检索" className={inputCls} />
          </div>
          <div className="flex items-end">
            <button type="submit" className="focus-ring h-[38px] w-full rounded-md bg-primary px-4 text-button font-medium text-primary-foreground transition-[filter] duration-quick hover:brightness-110 md:w-auto">
              检索
            </button>
          </div>
        </form>
        <p className="mt-3 font-mono text-2xs text-muted-foreground">GET /api/logs · 完整输入 messages / 输出 content / tokens / TTFT / 命中渠道 / 花费</p>
      </div>

      {state === "unavailable" || state === "error" ? (
        <EmptyState
          title="运行时请求日志为空"
          description={message}
          hint="GET /api/logs"
          action={
            <button type="button" onClick={() => void load(1)} className="focus-ring rounded-md border border-border px-4 py-2 text-button font-medium text-foreground hover:bg-card-hover">
              重新连接
            </button>
          }
        />
      ) : (
        <div className="card-surface overflow-x-auto">
          <table className="w-full min-w-[1000px] border-collapse text-left">
            <thead>
              <tr className="border-b border-border bg-muted/40 text-overline uppercase tracking-wider text-muted-foreground">
                <th className="px-4 py-3 font-medium">request_id</th>
                <th className="px-3 py-3 font-medium">model / alias</th>
                <th className="px-3 py-3 font-medium">channel</th>
                <th className="px-3 py-3 font-medium text-right">in / out tokens</th>
                <th className="px-3 py-3 font-medium text-right">TTFT / latency</th>
                <th className="px-3 py-3 font-medium text-right">status</th>
                <th className="px-3 py-3 font-medium text-right">cost</th>
                <th className="px-3 py-3 font-medium">created_at</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.request_id} className="border-b border-border/60 transition-colors duration-quick hover:bg-card-hover">
                  <td className="px-4 py-3 font-mono text-2xs text-foreground">{r.request_id}</td>
                  <td className="px-3 py-3 font-mono text-2xs text-muted-foreground">{r.alias || r.model_id}</td>
                  <td className="px-3 py-3 font-mono text-2xs text-muted-foreground">{r.channel_id}</td>
                  <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">{r.prompt_tokens} / {r.completion_tokens}</td>
                  <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">{r.ttft_ms} / {r.latency_ms} ms</td>
                  <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">{r.status_code}</td>
                  <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">${r.cost_usd.toFixed(6)}</td>
                  <td className="px-3 py-3 font-mono text-2xs text-muted-foreground">{r.created_at}</td>
                </tr>
              ))}
              {rows.length === 0 && (
                <tr>
                  <td colSpan={8} className="px-4 py-12 text-center text-sm text-muted-foreground">
                    {state === "loading" ? "读取中…" : "该筛选条件下暂无运行时日志（数据源 /api/logs）。"}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
          {total > 0 && (
            <div className="flex items-center justify-between border-t border-border px-4 py-3 text-xs text-muted-foreground">
              <span>共 {total} 条 · 第 {page} 页</span>
              <div className="flex gap-2">
                <button type="button" disabled={page <= 1} onClick={() => { const p = page - 1; setPage(p); void load(p); }} className="focus-ring rounded-md border border-border px-3 py-1.5 text-button disabled:opacity-40">上一页</button>
                <button type="button" onClick={() => { const p = page + 1; setPage(p); void load(p); }} className="focus-ring rounded-md border border-border px-3 py-1.5 text-button">下一页</button>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
