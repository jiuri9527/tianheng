"use client";

import { useCallback, useEffect, useState } from "react";
import { api, UNAVAILABLE_HINT } from "@/lib/api";

/**
 * 自动发现的真实模型面板（F3 / F4）。
 * 只渲染 GET /api/channels/:id 返回的真实运行时数据；
 * 未拉取成功时显示 unavailable 真实空态，绝不编造任何模型 id / 参数 / 输入输出样例。
 */

type ProbeState = "unavailable" | "ok" | "failed" | "pending" | string;

interface RuntimeModel {
  id?: string;
  upstream_id?: string;
  alias?: string;
  display_name?: string;
  context_length?: number | null;
  max_output_tokens?: number | null;
  input_price_per_mtok?: number | null;
  output_price_per_mtok?: number | null;
  supports_vision?: boolean;
  supports_tools?: boolean;
  supports_stream?: boolean;
  supports_reasoning?: boolean;
  param_source?: string | null;
  probe_status?: ProbeState;
  probe_input?: unknown;
  probe_output?: string | null;
  probe_prompt_tokens?: number | null;
  probe_completion_tokens?: number | null;
  probe_latency_ms?: number | null;
  raw_json?: Record<string, unknown> | null;
}

interface ChannelDetailRuntime {
  id?: string;
  name?: string;
  provider_type?: string;
  base_uri?: string;
  probe_status?: ProbeState;
  models_discovered?: number;
  last_error?: string | null;
  models?: RuntimeModel[];
}

interface Props {
  channelId?: string;
  title?: string;
}

const PARAM_KEYS = [
  "context_length",
  "architecture",
  "pricing",
  "top_provider",
  "supported_parameters",
  "owned_by",
  "created",
  "description",
] as const;

const num = (v: number | null | undefined): string =>
  v === null || v === undefined ? "—" : String(v);

const pretty = (v: unknown): string => {
  if (v === null || v === undefined) return "—";
  if (typeof v === "string") return v;
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
};

export default function AutoDiscoveryPanel({ channelId, title }: Props) {
  const [detail, setDetail] = useState<ChannelDetailRuntime | null>(null);
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [unavailable, setUnavailable] = useState(false);
  const [message, setMessage] = useState("");

  const load = useCallback(async () => {
    if (!channelId) return;
    setLoading(true);
    const res = await api.getChannel(channelId);
    setLoading(false);
    if (res.unavailable || !res.ok || !res.data) {
      setUnavailable(true);
      setDetail(null);
      setMessage(res.unavailable ? UNAVAILABLE_HINT : (res.error?.message ?? `HTTP ${res.status}`));
      return;
    }
    setUnavailable(false);
    const d = (res.data as unknown as ChannelDetailRuntime | null) ?? null;
    setDetail(d);
    setMessage("");
  }, [channelId]);

  useEffect(() => {
    void load();
  }, [load]);

  const refresh = async () => {
    if (!channelId) return;
    setRefreshing(true);
    const sync = await api.syncChannelModels(channelId);
    const probe = await api.probeChannel(channelId);
    await load();
    setRefreshing(false);
    if (sync.unavailable || probe.unavailable) {
      setUnavailable(true);
      setMessage(UNAVAILABLE_HINT);
      return;
    }
    const added = sync.data?.added ?? 0;
    const removed = sync.data?.removed ?? 0;
    const probed = probe.data?.probed ?? 0;
    const failed = probe.data?.failed ?? 0;
    setMessage(
      `真实同步完成：新增 ${added} / 移除 ${removed}；真实探测：成功 ${probed} / 失败 ${failed}（失败模型保持 probe_status，绝不编造输出）`,
    );
  };

  const models = detail?.models ?? [];
  const channelUnavailable =
    unavailable || !detail || detail.probe_status === "unavailable" || models.length === 0;

  return (
    <section className="card-surface p-5 md:p-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="font-brand text-base font-semibold text-heading">
            {title ?? "自动发现的真实模型"}
          </h2>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
            模型清单逐字来自该渠道真实上游 GET /models；参数卡片与输入/输出样例来自真实响应与真实极小探测调用。
          </p>
        </div>
        <button
          type="button"
          onClick={() => void refresh()}
          disabled={!channelId || refreshing || loading}
          className="focus-ring inline-flex items-center gap-2 rounded-md border border-border bg-card px-4 py-2 text-button font-medium text-foreground transition-colors duration-quick hover:bg-card-hover disabled:opacity-60"
        >
          <svg viewBox="0 0 16 16" fill="none" aria-hidden="true" className="size-4">
            <path d="M13 8a5 5 0 1 1-1.46-3.54M13 2v3h-3" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
          {refreshing ? "刷新中…" : "刷新真实模型"}
        </button>
      </div>

      {!channelId && (
        <div className="mt-5 rounded-lg border border-dashed border-border bg-muted/25 px-4 py-6 text-center">
          <p className="text-sm text-muted-foreground">提交渠道后自动展示该渠道真实发现的模型，无需填写任何模型名。</p>
        </div>
      )}

      {channelId && loading && (
        <p className="mt-5 font-mono text-2xs text-muted-foreground">正在读取真实渠道详情…</p>
      )}

      {message && (
        <p className="mt-4 font-mono text-2xs leading-relaxed text-muted-foreground" role="status">{message}</p>
      )}

      {channelId && !loading && channelUnavailable && (
        <div className="mt-5 rounded-lg border border-dashed border-border bg-muted/25 px-4 py-7 text-center">
          <p className="font-mono text-2xs uppercase tracking-widest text-muted-foreground">probe_status = unavailable</p>
          <p className="mt-2 text-sm text-heading">上游 /models 未拉取成功，模型列表为空</p>
          <p className="mx-auto mt-2 max-w-xl text-xs leading-relaxed text-muted-foreground">
            {detail?.last_error
              ? `真实错误：${detail.last_error}`
              : "站点不会填充任何占位或编造模型；配置好 base_uri 与 api_key 后点击「刷新真实模型」重新拉取。"}
          </p>
        </div>
      )}

      {channelId && !loading && !channelUnavailable && (
        <div className="mt-5 space-y-4">
          <div className="flex flex-wrap items-center gap-2 font-mono text-2xs text-muted-foreground">
            <span className="rounded-sm border border-border px-2 py-1">channel {detail?.id ?? "—"}</span>
            <span className="rounded-sm border border-border px-2 py-1">{detail?.provider_type ?? "—"}</span>
            <span className="rounded-sm border border-positive/30 px-2 py-1 text-positive-text">
              {models.length} 个真实模型
            </span>
            {detail?.base_uri && (
              <span className="truncate rounded-sm border border-border px-2 py-1">{detail.base_uri}</span>
            )}
          </div>

          <div className="overflow-x-auto rounded-md border border-border">
            <table className="w-full min-w-[720px] border-collapse text-left text-xs">
              <thead className="bg-muted/40 text-overline uppercase tracking-widest text-muted-foreground">
                <tr>
                  <th className="px-3 py-2 font-medium">模型 id（真实）</th>
                  <th className="px-3 py-2 font-medium">名称</th>
                  <th className="px-3 py-2 font-medium">上下文</th>
                  <th className="px-3 py-2 font-medium">最大输出</th>
                  <th className="px-3 py-2 font-medium">输入 / 输出价 ($/M)</th>
                  <th className="px-3 py-2 font-medium">能力位</th>
                  <th className="px-3 py-2 font-medium">探测</th>
                </tr>
              </thead>
              <tbody>
                {models.map((m, idx) => {
                  const modelId = m.upstream_id ?? m.alias ?? m.id ?? `row-${idx}`;
                  const caps = [
                    m.supports_stream ? "stream" : "",
                    m.supports_tools ? "tools" : "",
                    m.supports_vision ? "vision" : "",
                    m.supports_reasoning ? "reasoning" : "",
                  ].filter(Boolean);
                  return (
                    <tr key={modelId} className="border-t border-border align-top">
                      <td className="px-3 py-2 font-mono text-2xs text-heading">{modelId}</td>
                      <td className="px-3 py-2 text-muted-foreground">{m.display_name ?? "—"}</td>
                      <td className="px-3 py-2 font-mono text-muted-foreground">{num(m.context_length)}</td>
                      <td className="px-3 py-2 font-mono text-muted-foreground">{num(m.max_output_tokens)}</td>
                      <td className="px-3 py-2 font-mono text-muted-foreground">
                        {num(m.input_price_per_mtok)} / {num(m.output_price_per_mtok)}
                      </td>
                      <td className="px-3 py-2 font-mono text-2xs text-muted-foreground">
                        {caps.length ? caps.join(" · ") : "—"}
                      </td>
                      <td className="px-3 py-2">
                        <span
                          className={`rounded-sm border px-2 py-0.5 font-mono text-2xs ${
                            m.probe_status === "ok"
                              ? "border-positive/30 text-positive-text"
                              : "border-border text-muted-foreground"
                          }`}
                        >
                          {m.probe_status ?? "unavailable"}
                        </span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className="grid gap-3 md:grid-cols-2">
            {models.map((m, idx) => {
              const modelId = m.upstream_id ?? m.alias ?? m.id ?? `card-${idx}`;
              const raw = m.raw_json ?? null;
              const rawEntries = raw
                ? PARAM_KEYS.filter((k) => raw[k] !== undefined && raw[k] !== null).map((k) => [k, raw[k]] as const)
                : [];
              const hasProbe = m.probe_status === "ok";
              return (
                <article key={`detail-${modelId}`} className="rounded-md border border-border bg-card p-4">
                  <p className="truncate font-mono text-xs font-medium text-heading">{modelId}</p>
                  <p className="mt-1 font-mono text-2xs text-muted-foreground">
                    param_source={m.param_source ?? "upstream"} · probe_status={m.probe_status ?? "unavailable"}
                  </p>

                  <h3 className="mt-4 text-overline uppercase tracking-widest text-muted-foreground">真实参数卡片（逐字上游字段）</h3>
                  {rawEntries.length === 0 ? (
                    <p className="mt-2 text-2xs text-muted-foreground">上游未返回额外参数字段。</p>
                  ) : (
                    <dl className="mt-2 grid gap-2">
                      {rawEntries.map(([k, v]) => (
                        <div key={k} className="rounded-sm border border-border bg-muted/30 px-2 py-1.5">
                          <dt className="font-mono text-2xs text-primary">{k}</dt>
                          <dd className="mt-1 whitespace-pre-wrap break-all font-mono text-2xs leading-relaxed text-foreground/90">
                            {pretty(v)}
                          </dd>
                        </div>
                      ))}
                    </dl>
                  )}

                  <h3 className="mt-4 text-overline uppercase tracking-widest text-muted-foreground">真实输入 / 输出样例</h3>
                  {hasProbe ? (
                    <div className="mt-2 space-y-2">
                      <div>
                        <p className="font-mono text-2xs text-muted-foreground">input</p>
                        <pre className="mt-1 overflow-x-auto rounded-sm border border-border bg-muted/30 p-2 font-mono text-2xs leading-relaxed text-foreground/90">
                          {pretty(m.probe_input ?? null)}
                        </pre>
                      </div>
                      <div>
                        <p className="font-mono text-2xs text-muted-foreground">output</p>
                        <pre className="mt-1 whitespace-pre-wrap break-all rounded-sm border border-border bg-muted/30 p-2 font-mono text-2xs leading-relaxed text-foreground/90">
                          {m.probe_output ? m.probe_output : "（上游真实响应未包含文本输出）"}
                        </pre>
                      </div>
                      <p className="font-mono text-2xs text-muted-foreground">
                        prompt_tokens={num(m.probe_prompt_tokens)} · completion_tokens={num(m.probe_completion_tokens)} · latency_ms={num(m.probe_latency_ms)}
                      </p>
                    </div>
                  ) : (
                    <p className="mt-2 text-2xs leading-relaxed text-muted-foreground">
                      真实探测未完成或未成功（probe_status={m.probe_status ?? "unavailable"}）。不展示任何编造的输入或输出内容。
                    </p>
                  )}
                </article>
              );
            })}
          </div>
        </div>
      )}
    </section>
  );
}
