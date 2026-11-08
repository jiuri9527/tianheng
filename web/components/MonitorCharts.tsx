// 文件: web/components/MonitorCharts.tsx
// 纯 SVG 曲线（零第三方图表依赖）。所有 points 来自后端真实 request_logs 聚合，
// 无数据时传全 0 序列，画一条真实贴底平线，不生成任何占位/编造曲线点。
"use client";

export interface Point {
  label: string;
  value: number;
}

/** 单条折线：QPS / 成功率 / TTFT / token 等时间序列。全 0 时贴底平线。 */
export function MonitorLineChart({
  points,
  unit = "",
  height = 200,
  tone = "primary",
  decimals = 2,
}: {
  points: Point[];
  unit?: string;
  height?: number;
  tone?: "primary" | "positive" | "warning" | "info";
  decimals?: number;
}) {
  const w = 720;
  const h = height;
  const padL = 52;
  const padR = 12;
  const padT = 14;
  const padB = 28;
  const innerW = w - padL - padR;
  const innerH = h - padT - padB;
  const stroke =
    tone === "positive" ? "rgb(var(--positive))" :
    tone === "warning" ? "rgb(var(--warning))" :
    tone === "info" ? "rgb(var(--info))" : "rgb(var(--primary))";

  const values = points.map((p) => (Number.isFinite(p.value) ? p.value : 0));
  const max = Math.max(...values, 1);
  const step = innerW / Math.max(1, values.length - 1);
  const coords = values.map((v, i) => ({
    x: padL + i * step,
    y: padT + innerH - (v / max) * innerH,
    v,
    label: points[i]?.label ?? "",
  }));
  const line = coords.map((c) => `${c.x.toFixed(1)},${c.y.toFixed(1)}`).join(" ");
  const area = `${padL},${padT + innerH} ${line} ${padL + innerW},${padT + innerH}`;

  const fmt = (v: number): string => {
    if (Math.abs(v) >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
    if (Math.abs(v) >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
    if (Number.isInteger(v)) return String(v);
    return v.toFixed(decimals);
  };

  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="w-full" role="img" aria-label="真实时间序列折线">
      {[0, 0.25, 0.5, 0.75, 1].map((f) => {
        const y = padT + innerH - f * innerH;
        return (
          <g key={f}>
            <line x1={padL} x2={padL + innerW} y1={y} y2={y} stroke="rgb(var(--border))" strokeWidth="1" />
            <text x={padL - 8} y={y + 3} textAnchor="end" fontSize="10" fill="rgb(var(--muted-foreground))" fontFamily="monospace">
              {fmt(max * f)}{unit}
            </text>
          </g>
        );
      })}
      <polygon points={area} fill={stroke} opacity="0.10" />
      <polyline points={line} fill="none" stroke={stroke} strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
      {coords.map((c, i) => (
        <circle key={i} cx={c.x} cy={c.y} r="2" fill="rgb(var(--background))" stroke={stroke} strokeWidth="1.5">
          <title>{`${c.label} · ${c.v.toFixed(decimals)}${unit}`}</title>
        </circle>
      ))}
      {coords.map((c, i) =>
        i === 0 || i === coords.length - 1 || i === Math.floor(coords.length / 2) ? (
          <text key={`t${i}`} x={c.x} y={h - 8} textAnchor="middle" fontSize="10" fill="rgb(var(--muted-foreground))" fontFamily="monospace">
            {c.label}
          </text>
        ) : null,
      )}
    </svg>
  );
}

/** 双折线（prompt vs completion tokens），同一坐标系，真实值。 */
export function MonitorDualLineChart({
  a,
  b,
  height = 200,
}: {
  a: { points: Point[]; name: string; tone: string };
  b: { points: Point[]; name: string; tone: string };
  height?: number;
}) {
  const w = 720;
  const h = height;
  const padL = 52;
  const padR = 12;
  const padT = 14;
  const padB = 28;
  const innerW = w - padL - padR;
  const innerH = h - padT - padB;
  const av = a.points.map((p) => (Number.isFinite(p.value) ? p.value : 0));
  const bv = b.points.map((p) => (Number.isFinite(p.value) ? p.value : 0));
  const max = Math.max(...av, ...bv, 1);
  const n = Math.max(av.length, bv.length, 1);
  const step = innerW / Math.max(1, n - 1);
  const toLine = (vals: number[]) =>
    vals.map((v, i) => `${(padL + i * step).toFixed(1)},${(padT + innerH - (v / max) * innerH).toFixed(1)}`).join(" ");

  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="w-full" role="img" aria-label="token 消耗双折线">
      {[0, 0.5, 1].map((f) => {
        const y = padT + innerH - f * innerH;
        return (
          <g key={f}>
            <line x1={padL} x2={padL + innerW} y1={y} y2={y} stroke="rgb(var(--border))" strokeWidth="1" />
            <text x={padL - 8} y={y + 3} textAnchor="end" fontSize="10" fill="rgb(var(--muted-foreground))" fontFamily="monospace">
              {Math.round(max * f).toLocaleString()}
            </text>
          </g>
        );
      })}
      <polyline points={toLine(av)} fill="none" stroke={a.tone} strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
      <polyline points={toLine(bv)} fill="none" stroke={b.tone} strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
      <g fontSize="10" fontFamily="monospace">
        <rect x={padL + 4} y={padT + 2} width="10" height="3" fill={a.tone} />
        <text x={padL + 18} y={padT + 7} fill="rgb(var(--muted-foreground))">{a.name}</text>
        <rect x={padL + 120} y={padT + 2} width="10" height="3" fill={b.tone} />
        <text x={padL + 134} y={padT + 7} fill="rgb(var(--muted-foreground))">{b.name}</text>
      </g>
    </svg>
  );
}

/** 按模型 / 渠道 / 供应商 / 状态的分布条（真实分组聚合值）。 */
export function MonitorBarList({
  rows,
  unit = "",
  maxRows = 8,
}: {
  rows: { key: string; value: number }[];
  unit?: string;
  maxRows?: number;
}) {
  const top = rows.slice(0, maxRows);
  const max = Math.max(...top.map((r) => r.value), 1);
  if (top.length === 0) {
    return <p className="py-8 text-center font-mono text-2xs text-muted-foreground">暂无真实分组数据</p>;
  }
  return (
    <ul className="space-y-2.5">
      {top.map((r) => (
        <li key={r.key} className="grid grid-cols-[minmax(0,10rem)_1fr_auto] items-center gap-3">
          <span className="truncate font-mono text-2xs text-muted-foreground" title={r.key}>{r.key}</span>
          <div className="h-2.5 overflow-hidden rounded-full bg-muted">
            <div
              className="h-full rounded-full transition-[width] duration-very-slow ease-smooth-out"
              style={{ width: `${Math.max(2, (r.value / max) * 100)}%`, backgroundColor: "rgb(var(--primary))" }}
            />
          </div>
          <span className="w-16 text-right font-mono text-2xs tabular-nums text-foreground">
            {r.value.toLocaleString()}{unit}
          </span>
        </li>
      ))}
    </ul>
  );
}
