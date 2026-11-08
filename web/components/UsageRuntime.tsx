"use client";

import { useEffect, useState } from "react";
import { api, UNAVAILABLE_HINT } from "@/lib/api";
import type { UsagePoint } from "@/lib/types";
import EmptyState from "@/components/EmptyState";

export default function UsageRuntime() {
  const [rows, setRows] = useState<UsagePoint[]>([]);
  const [state, setState] = useState<"loading" | "ok" | "unavailable" | "error">("loading");
  const [message, setMessage] = useState("");
  const [groupBy, setGroupBy] = useState("day");

  const load = async (g: string) => {
    setState("loading");
    const res = await api.usage(undefined, undefined, g);
    if (res.unavailable) { setState("unavailable"); setMessage(UNAVAILABLE_HINT); setRows([]); return; }
    if (!res.ok) { setState("error"); setMessage(res.error?.message ?? `HTTP ${res.status}`); setRows([]); return; }
    setRows(res.data ?? []);
    setState("ok");
  };

  useEffect(() => { void load("day"); }, []);

  return (
    <section aria-label="用量明细" className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="font-brand text-base font-semibold text-heading">用量明细 (F10)</h2>
        <div className="flex items-center gap-2">
          <label htmlFor="usage-group" className="text-2xs text-muted-foreground">group_by</label>
          <select
            id="usage-group"
            value={groupBy}
            onChange={(e) => { setGroupBy(e.target.value); void load(e.target.value); }}
            className="focus-ring rounded-md border border-border bg-input-bg px-2 py-1.5 font-mono text-2xs text-foreground"
          >
            {["day", "model", "channel"].map((g) => <option key={g} value={g}>{g}</option>)}
          </select>
          <button type="button" onClick={() => void load(groupBy)} className="focus-ring rounded-md border border-border px-3 py-1.5 text-button font-medium text-foreground hover:bg-card-hover">刷新</button>
        </div>
      </div>

      {state === "unavailable" || state === "error" ? (
        <EmptyState
          title="用量记录为空"
          description={message || "用量聚合来自 GET /api/usage；后端未连接时不填充编造行。"}
          hint="GET /api/usage?group_by=day"
        />
      ) : (
        <div className="card-surface overflow-x-auto">
          <table className="w-full min-w-[720px] border-collapse text-left">
            <thead>
              <tr className="border-b border-border bg-muted/40 text-overline uppercase tracking-wider text-muted-foreground">
                <th className="px-4 py-3 font-medium">day</th>
                <th className="px-3 py-3 font-medium">alias</th>
                <th className="px-3 py-3 font-medium text-right">requests</th>
                <th className="px-3 py-3 font-medium text-right">prompt_tokens</th>
                <th className="px-3 py-3 font-medium text-right">completion_tokens</th>
                <th className="px-3 py-3 font-medium text-right">cost_usd</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={`${r.day}-${r.alias}-${i}`} className="border-b border-border/60">
                  <td className="px-4 py-3 font-mono text-2xs text-foreground">{r.day}</td>
                  <td className="px-3 py-3 font-mono text-2xs text-muted-foreground">{r.alias}</td>
                  <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">{r.requests}</td>
                  <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">{r.prompt_tokens}</td>
                  <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">{r.completion_tokens}</td>
                  <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">${r.cost_usd.toFixed(6)}</td>
                </tr>
              ))}
              {rows.length === 0 && (
                <tr><td colSpan={6} className="px-4 py-10 text-center text-sm text-muted-foreground">
                  {state === "loading" ? "读取中…" : "该聚合维度下暂无运行时用量记录。"}
                </td></tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
