"use client";

/**
 * 完整模型排行榜 · 绝对真实
 * 线上优先读取 GET /api/rankings（Go 后端从 go:embed 真实数据集计算），
 * 后端未连接时回退到 web/data/models.json 同公式本地计算。
 * 页面不渲染任何调用量、评分、趋势、热度、延迟等无法从真实数据集推导的数据。
 */
import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import SectionHeading from "@/components/SectionHeading";
import {
  computeAll,
  normalizeApiReport,
  DIMENSIONS,
  META,
  type ApiReport,
  type Board,
  type Dimension,
} from "@/lib/rankings";
import { ALL_MODELS } from "@/lib/models";

export default function RankingsPage() {
  const [boards, setBoards] = useState<Board[]>([]);
  const [dim, setDim] = useState<Dimension>("context");
  const [query, setQuery] = useState("");
  const [origin, setOrigin] = useState<"loading" | "api" | "dataset-fallback">("loading");
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    fetch("/api/rankings")
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        return r.json() as Promise<ApiReport>;
      })
      .then((rep) => {
        if (cancelled) return;
        const normalized = normalizeApiReport(rep);
        if (normalized.length === 0) throw new Error("empty rankings response");
        setBoards(normalized);
        setOrigin("api");
      })
      .catch((e: Error) => {
        if (cancelled) return;
        setBoards(computeAll());
        setOrigin("dataset-fallback");
        setError(e.message);
      });
    return () => { cancelled = true; };
  }, []);

  const board = useMemo(
    () => boards.find((b) => b.dimension === dim) ?? boards[0],
    [boards, dim],
  );

  const filtered = useMemo(() => {
    if (!board) return [];
    const q = query.trim().toLowerCase();
    if (!q) return board.entries;
    return board.entries.filter(
      (e) => e.id.toLowerCase().includes(q) || e.name.toLowerCase().includes(q) || e.provider.toLowerCase().includes(q),
    );
  }, [board, query]);

  const sha = board?.sourceSha256 || "";
  const totalModels = board?.totalModels ?? ALL_MODELS.length;

  return (
    <div className="mx-auto w-full max-w-7xl px-4 py-12 md:px-6 md:py-16">
      <SectionHeading
        eyebrow="Rankings · 真实数据"
        title="完整模型排行榜"
        description="全部排名值逐字取自 data/models-real.json（HTTP 200 抓取自 https://openrouter.ai/api/v1/models），或由真实字段做可复核换算。无调用量、评分、趋势、热度、延迟等编造指标。"
        href="/models"
        linkLabel="模型广场"
      />

      {/* 数据源与真实性证据 */}
      <div className="card-surface mt-8 flex flex-wrap items-center justify-between gap-4 p-5">
        <div className="flex flex-wrap items-center gap-x-8 gap-y-2 text-xs">
          <span className="text-muted-foreground">
            真实模型 <strong className="font-mono text-foreground">{totalModels}</strong>
          </span>
          <span className="text-muted-foreground">
            供应商 <strong className="font-mono text-foreground">{board?.totalProviders ?? "—"}</strong>
          </span>
          <span className="text-muted-foreground">
            维度 <strong className="font-mono text-foreground">{DIMENSIONS.length}</strong>
          </span>
          <span className="text-muted-foreground">
            来源{" "}
            <strong className="font-mono text-foreground">
              {origin === "api" ? "GET /api/rankings（Go 后端实时计算）" : origin === "dataset-fallback" ? "web/data/models.json（真实副本本地计算）" : "读取中…"}
            </strong>
          </span>
        </div>
        <div className="flex flex-col items-start gap-1">
          <span className="text-overline uppercase tracking-widest text-muted-foreground">source_sha256（由真实字节计算）</span>
          <code className="font-mono text-2xs text-foreground">{sha || "由 scripts/verify-models.mjs 校验"}</code>
        </div>
      </div>

      {error && origin === "dataset-fallback" && (
        <div className="mt-4 rounded-md border border-warning/25 bg-warning/8 px-4 py-3 text-xs text-warning-text">
          后端 /api/rankings 未连接（{error}），已回退到真实数据集本地计算；排名公式与后端完全一致，数据仍为真实条目。
        </div>
      )}

      {/* 维度切换 */}
      <div className="mt-8 flex flex-wrap gap-2">
        {DIMENSIONS.map((d) => (
          <button
            key={d}
            type="button"
            onClick={() => setDim(d)}
            className={`focus-ring rounded-md border px-3 py-2 text-button font-medium transition-colors duration-quick ${
              dim === d
                ? "border-primary/40 bg-primary text-primary-foreground"
                : "border-border bg-card text-foreground hover:bg-card-hover"
            }`}
          >
            {META[d].title}
          </button>
        ))}
      </div>

      {board && (
        <>
          {/* 可复核公式 */}
          <div className="card-surface mt-6 grid gap-4 p-5 md:grid-cols-[1fr_auto]">
            <div>
              <p className="text-overline uppercase tracking-widest text-muted-foreground">排名公式（真实、可复核）</p>
              <p className="mt-2 font-mono text-xs leading-relaxed text-foreground">{board.formula}</p>
            </div>
            <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
              <span>单位 <strong className="font-mono text-foreground">{board.valueUnit}</strong></span>
              <span>完整榜单 <strong className="font-mono text-foreground">{board.entryCount}</strong> 条</span>
              <span>生成于 <strong className="font-mono text-foreground">{board.generatedAt}</strong></span>
            </div>
          </div>

          {/* 过滤（纯客户端，不引入新数据） */}
          <div className="mt-6 flex flex-wrap items-center justify-between gap-3">
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="按真实 id / 名称 / 供应商筛选"
              className="focus-ring w-full max-w-sm rounded-md border border-border bg-card px-3 py-2 font-mono text-xs text-foreground"
            />
            <span className="font-mono text-2xs text-muted-foreground">
              当前显示 {filtered.length} / {board.entryCount} 条 · 每条 source: data/models-real.json
            </span>
          </div>

          {/* 完整榜单 */}
          <div className="card-surface mt-4 overflow-hidden">
            <div className="max-h-[42rem] overflow-auto">
              <table className="w-full border-collapse text-left text-xs">
                <thead className="sticky top-0 z-10 bg-muted/90 backdrop-blur">
                  <tr className="text-overline uppercase tracking-wider text-muted-foreground">
                    <th className="px-4 py-3 font-medium">#</th>
                    <th className="px-4 py-3 font-medium">真实模型 id</th>
                    <th className="px-4 py-3 font-medium">名称</th>
                    <th className="px-4 py-3 font-medium">供应商</th>
                    <th className="px-4 py-3 text-right font-medium">{board.valueUnit}</th>
                    <th className="px-4 py-3 text-right font-medium">上下文</th>
                    <th className="px-4 py-3 text-right font-medium">输入价 / 1M</th>
                    <th className="px-4 py-3 text-right font-medium">输出价 / 1M</th>
                    <th className="px-4 py-3 text-right font-medium">能力参数</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map((e) => (
                    <tr key={`${board.dimension}-${e.id}`} className="border-t border-border/60 hover:bg-card-hover/50">
                      <td className="px-4 py-2.5 font-mono tabular-nums text-muted-foreground">{e.rank}</td>
                      <td className="px-4 py-2.5 font-mono text-foreground">
                        <Link href={`/models/${encodeURIComponent(e.id)}`} className="hover:text-primary hover:underline">
                          {e.id}
                        </Link>
                      </td>
                      <td className="px-4 py-2.5 text-foreground">{e.name}</td>
                      <td className="px-4 py-2.5 font-mono text-muted-foreground">{e.provider}</td>
                      <td className="px-4 py-2.5 text-right font-mono tabular-nums text-foreground">{e.valueLabel}</td>
                      <td className="px-4 py-2.5 text-right font-mono tabular-nums text-muted-foreground">
                        {e.contextLength > 0 ? e.contextLength.toLocaleString() : "—"}
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono tabular-nums text-muted-foreground">
                        {e.promptPerMtok === null ? "—" : e.promptPerMtok.toFixed(6)}
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono tabular-nums text-muted-foreground">
                        {e.completionPerMtok === null ? "—" : e.completionPerMtok.toFixed(6)}
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono tabular-nums text-muted-foreground">{e.paramCount || "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>

          <p className="mt-4 text-2xs leading-relaxed text-muted-foreground">
            本页不含任何运行时调用统计。真实调用量、TTFT、花费等只在 <Link href="/usage" className="text-primary hover:underline">用量</Link> 与{" "}
            <Link href="/logs" className="text-primary hover:underline">日志</Link> 页由后端 /api/* 提供；后端未连接时显示真实空态，不填充编造数据。
          </p>
        </>
      )}
    </div>
  );
}
