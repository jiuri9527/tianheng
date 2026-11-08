"use client";

import { useState } from "react";

export interface AliasRow {
  id: string;
  name: string;
  provider: string;
  context: number;
  prompt: string;
}

/** F11 模型池与别名映射编辑（合并/拆分/禁用/改名/定价覆盖） */
export default function AdminModelTable({ rows }: { rows: AliasRow[] }) {
  const [alias, setAlias] = useState<Record<string, string>>(() =>
    Object.fromEntries(rows.map((r) => [r.id, r.id])),
  );
  const [enabled, setEnabled] = useState<Record<string, boolean>>(() =>
    Object.fromEntries(rows.map((r) => [r.id, true])),
  );
  const [feedback, setFeedback] = useState<Record<string, string>>({});

  const save = async (id: string) => {
    try {
      const res = await fetch(`/api/models/${encodeURIComponent(id)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ alias: alias[id], enabled: enabled[id], price_override: null }),
      });
      const text = await res.text();
      setFeedback((f) => ({ ...f, [id]: `HTTP ${res.status} · ${text.slice(0, 180) || "(空响应)"}` }));
    } catch {
      setFeedback((f) => ({ ...f, [id]: "后端 /api/* 未连接：PUT 未送达，未改动真实数据。" }));
    }
  };

  return (
    <div className="card-surface overflow-x-auto">
      <table className="w-full min-w-[1000px] border-collapse text-left">
        <thead>
          <tr className="border-b border-border bg-muted/40 text-overline uppercase tracking-wider text-muted-foreground">
            {["真实模型 id", "name", "provider", "context", "prompt/token", "alias（可编辑）", "enabled", "操作 / 结果"].map((h) => (
              <th key={h} className="px-4 py-3 font-medium">{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id} className="border-b border-border/60">
              <td className="px-4 py-2.5 font-mono text-2xs text-foreground">{r.id}</td>
              <td className="px-4 py-2.5 text-xs text-heading">{r.name}</td>
              <td className="px-4 py-2.5 font-mono text-2xs text-muted-foreground">{r.provider}</td>
              <td className="px-4 py-2.5 font-mono text-xs tabular-nums">{r.context.toLocaleString()}</td>
              <td className="px-4 py-2.5 font-mono text-2xs tabular-nums text-muted-foreground">{r.prompt}</td>
              <td className="px-4 py-2.5">
                <input
                  aria-label={`alias for ${r.id}`}
                  value={alias[r.id] ?? r.id}
                  onChange={(e) => setAlias((a) => ({ ...a, [r.id]: e.target.value }))}
                  className="focus-ring w-48 rounded-md border border-border bg-input-bg px-2 py-1 font-mono text-2xs text-foreground"
                />
              </td>
              <td className="px-4 py-2.5">
                <button
                  type="button"
                  onClick={() => setEnabled((s) => ({ ...s, [r.id]: !s[r.id] }))}
                  className={`focus-ring rounded-full border px-2.5 py-1 font-mono text-2xs ${enabled[r.id] ? "border-positive/30 bg-positive/8 text-positive-text" : "border-border bg-muted text-muted-foreground"}`}
                >
                  {enabled[r.id] ? "true" : "false"}
                </button>
              </td>
              <td className="px-4 py-2.5">
                <button type="button" onClick={() => void save(r.id)} className="focus-ring rounded-sm text-2xs font-medium text-primary hover:underline">保存</button>
                {feedback[r.id] && <p className="mt-1 max-w-[16rem] break-all font-mono text-2xs text-muted-foreground">{feedback[r.id]}</p>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
