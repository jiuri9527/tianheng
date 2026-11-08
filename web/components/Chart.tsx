import type { Datum } from "@/lib/stats";
import { formatCount } from "@/lib/models";

/** 全部图表为纯 SVG / CSS 实现，零第三方图表依赖；数值来自真实数据集或 /api/* */

const PALETTE = [
  "rgb(var(--primary))",
  "rgb(var(--info))",
  "rgb(var(--positive))",
  "rgb(var(--warning))",
  "rgb(var(--negative))",
  "rgb(var(--accent-foreground))",
  "rgb(var(--muted-foreground))",
];

export function Sparkline({
  values,
  height = 40,
}: {
  values: number[];
  height?: number;
}) {
  const w = 160;
  const h = height;
  const max = Math.max(...values, 1);
  const min = Math.min(...values, 0);
  const span = max - min || 1;
  const pts = values
    .map((v, i) => {
      const x = (i / Math.max(1, values.length - 1)) * w;
      const y = h - ((v - min) / span) * (h - 6) - 3;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="h-10 w-full text-primary" role="img" aria-label="趋势折线">
      <polyline points={pts} fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function LineChart({ data, height = 220 }: { data: Datum[]; height?: number }) {
  const w = 720;
  const h = height;
  const padL = 44;
  const padR = 12;
  const padT = 16;
  const padB = 30;
  const innerW = w - padL - padR;
  const innerH = h - padT - padB;
  const max = Math.max(...data.map((d) => d.value), 1);
  const step = innerW / Math.max(1, data.length - 1);
  const pts = data.map((d, i) => ({
    x: padL + i * step,
    y: padT + innerH - (d.value / max) * innerH,
    ...d,
  }));
  const line = pts.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" ");
  const area = `${padL},${padT + innerH} ${line} ${padL + innerW},${padT + innerH}`;

  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="w-full text-primary" role="img" aria-label="折线图">
      {[0, 0.25, 0.5, 0.75, 1].map((f) => {
        const y = padT + innerH - f * innerH;
        return (
          <g key={f}>
            <line x1={padL} x2={padL + innerW} y1={y} y2={y} stroke="rgb(var(--border))" strokeWidth="1" />
            <text x={padL - 8} y={y + 3} textAnchor="end" fontSize="10" fill="rgb(var(--muted-foreground))" fontFamily="monospace">
              {formatCount(Math.round(max * f))}
            </text>
          </g>
        );
      })}
      <polygon points={area} fill="rgb(var(--primary) / 0.10)" />
      <polyline points={line} fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
      {pts.map((p, i) => (
        <circle key={i} cx={p.x} cy={p.y} r="2.6" fill="rgb(var(--background))" stroke="currentColor" strokeWidth="1.8" />
      ))}
      {pts.map((p, i) =>
        i === 0 || i === pts.length - 1 || i === Math.floor(pts.length / 2) ? (
          <text key={i} x={p.x} y={h - 8} textAnchor="middle" fontSize="10" fill="rgb(var(--muted-foreground))" fontFamily="monospace">
            {p.label}
          </text>
        ) : null,
      )}
    </svg>
  );
}

export function BarChart({ data, unit = "" }: { data: Datum[]; unit?: string }) {
  const max = Math.max(...data.map((d) => d.value), 1);
  return (
    <div className="space-y-3">
      {data.map((d, i) => (
        <div key={d.label} className="grid grid-cols-[minmax(0,9rem)_1fr_auto] items-center gap-3">
          <span className="truncate font-mono text-2xs text-muted-foreground" title={d.label}>
            {d.label}
          </span>
          <div className="h-2.5 overflow-hidden rounded-full bg-muted">
            <div
              className="h-full rounded-full transition-[width] duration-very-slow ease-smooth-out"
              style={{
                width: `${Math.max(2, (d.value / max) * 100)}%`,
                backgroundColor: PALETTE[i % PALETTE.length],
              }}
            />
          </div>
          <span className="w-12 text-right font-mono text-xs tabular-nums text-foreground">
            {formatCount(d.value)}{unit}
          </span>
        </div>
      ))}
    </div>
  );
}

export function Donut({ data, size = 190 }: { data: Datum[]; size?: number }) {
  const total = data.reduce((a, b) => a + b.value, 0) || 1;
  const r = 70;
  const c = 2 * Math.PI * r;
  let offset = 0;
  return (
    <div className="flex flex-wrap items-center gap-6">
      <svg width={size} height={size} viewBox="0 0 180 180" role="img" aria-label="占比环形图">
        <g transform="rotate(-90 90 90)">
          {data.map((d, i) => {
            const len = (d.value / total) * c;
            const el = (
              <circle
                key={d.label}
                cx="90"
                cy="90"
                r={r}
                fill="none"
                strokeWidth="18"
                strokeDasharray={`${len} ${c - len}`}
                strokeDashoffset={-offset}
                style={{ stroke: PALETTE[i % PALETTE.length] }}
              />
            );
            offset += len;
            return el;
          })}
        </g>
        <text x="90" y="86" textAnchor="middle" fontSize="22" fontWeight="700" fill="rgb(var(--foreground))" fontFamily="monospace">
          {total}
        </text>
        <text x="90" y="104" textAnchor="middle" fontSize="10" fill="rgb(var(--muted-foreground))">
          总计
        </text>
      </svg>
      <ul className="min-w-0 flex-1 space-y-1.5">
        {data.map((d, i) => (
          <li key={d.label} className="flex items-center justify-between gap-3 text-xs">
            <span className="flex min-w-0 items-center gap-2">
              <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: PALETTE[i % PALETTE.length] }} />
              <span className="truncate text-muted-foreground">{d.label}</span>
            </span>
            <span className="font-mono tabular-nums text-foreground">
              {d.value} ({((d.value / total) * 100).toFixed(1)}%)
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

export interface ScatterPoint {
  id: string;
  label: string;
  x: number;
  y: number;
}

/** 价格（对数轴）× 上下文长度 散点，用于「价格与性能对照」 */
export function ScatterPlot({ points }: { points: ScatterPoint[] }) {
  const w = 720;
  const h = 300;
  const padL = 54;
  const padR = 16;
  const padT = 16;
  const padB = 40;
  const innerW = w - padL - padR;
  const innerH = h - padT - padB;
  const xs = points.map((p) => Math.log10(p.x + 0.01));
  const xMin = Math.min(...xs, -2);
  const xMax = Math.max(...xs, 1);
  const yMax = Math.max(...points.map((p) => p.y), 1);
  const px = (x: number) => padL + ((Math.log10(x + 0.01) - xMin) / (xMax - xMin || 1)) * innerW;
  const py = (y: number) => padT + innerH - (y / yMax) * innerH;

  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="w-full" role="img" aria-label="价格与上下文长度散点图">
      {[0, 0.25, 0.5, 0.75, 1].map((f) => {
        const y = padT + innerH - f * innerH;
        return (
          <g key={f}>
            <line x1={padL} x2={padL + innerW} y1={y} y2={y} stroke="rgb(var(--border))" strokeWidth="1" />
            <text x={padL - 8} y={y + 3} textAnchor="end" fontSize="10" fill="rgb(var(--muted-foreground))" fontFamily="monospace">
              {formatCount(Math.round(yMax * f))}
            </text>
          </g>
        );
      })}
      {[0.01, 0.1, 1, 10].map((v) => {
        const x = px(v);
        if (x < padL || x > padL + innerW) return null;
        return (
          <g key={v}>
            <line x1={x} x2={x} y1={padT} y2={padT + innerH} stroke="rgb(var(--border))" strokeWidth="1" strokeDasharray="3 4" />
            <text x={x} y={h - 20} textAnchor="middle" fontSize="10" fill="rgb(var(--muted-foreground))" fontFamily="monospace">
              ${v}
            </text>
          </g>
        );
      })}
      {points.map((p, i) => (
        <circle key={p.id + i} cx={px(p.x)} cy={py(p.y)} r="4.5" fill="rgb(var(--primary) / 0.55)" stroke="rgb(var(--primary))" strokeWidth="1.2">
          <title>{`${p.label} · $${p.x.toFixed(2)}/1M · ${formatCount(p.y)} context`}</title>
        </circle>
      ))}
      <text x={padL + innerW / 2} y={h - 4} textAnchor="middle" fontSize="10" fill="rgb(var(--muted-foreground))">
        输入价格 USD / 1M tokens（对数轴）
      </text>
    </svg>
  );
}
