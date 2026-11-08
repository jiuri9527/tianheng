"use client";

import { useMemo, useState } from "react";
import { api, PROVIDER_AUTH_STYLE, PROVIDER_BASE_URI_HINTS, PROVIDER_LABELS, UNAVAILABLE_HINT } from "@/lib/api";
import type { ChannelCreateInput, ProviderType, Visibility } from "@/lib/types";
import AutoDiscoveryPanel from "@/app/channels/AutoDiscoveryPanel";

const DENY_HOSTS = [
  "127.", "10.", "192.168.", "169.254.169.254", "::1", "localhost", "0.0.0.0",
];
const isPrivate172 = (host: string): boolean => {
  const m = /^172\.(\d+)\./.exec(host);
  return Boolean(m && Number(m[1]) >= 16 && Number(m[1]) <= 31);
};

/** F13 SSRF 白名单校验的前端预检（后端在保存时做 DNS 二次校验） */
export const preflightBaseUri = (raw: string): { ok: boolean; reason: string } => {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return { ok: false, reason: "base_uri 不是合法 URL" };
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") {
    return { ok: false, reason: `禁止协议 ${u.protocol}（仅 http(s)）` };
  }
  const h = u.hostname.toLowerCase();
  if (DENY_HOSTS.some((d) => h.startsWith(d)) || isPrivate172(h) || h === "[::1]") {
    return { ok: false, reason: "命中内网/回环/元数据地址白名单" };
  }
  const port = u.port ? Number(u.port) : u.protocol === "https:" ? 443 : 80;
  if (port !== 80 && port !== 443) {
    return { ok: false, reason: `非标准端口 ${port}（仅 80/443）` };
  }
  return { ok: true, reason: "URL 合法；提交后站点会用该 key 实时 GET /models 自动发现全部真实模型" };
};

/** POST /api/channels 的真实响应（模型由站点自动发现，用户不填任何模型名） */
interface CreateChannelResponse {
  id: string;
  key_hint: string;
  models_discovered: number;
  connection_ok?: boolean;
  connection_error?: string;
  probe_status?: string;
}

interface SubmitResult {
  ok: boolean;
  message: string;
  detail?: string;
  unavailable?: boolean;
}

export default function ChannelForm() {
  // 用户只需填写这四个字段；模型清单由站点实时从真实上游拉取。
  const [providerType, setProviderType] = useState<ProviderType>("openai");
  const [baseUri, setBaseUri] = useState(PROVIDER_BASE_URI_HINTS.openai);
  const [apiKey, setApiKey] = useState("");
  const [visibility, setVisibility] = useState<Visibility>("private");

  // 高级选项（可选，均有自动默认值，不要求用户填写）
  const [name, setName] = useState("");
  const [weight, setWeight] = useState(100);
  const [priority, setPriority] = useState(0);
  const [modelsPath, setModelsPath] = useState("/models");
  const [chatPath, setChatPath] = useState("/chat/completions");
  const [customHeaders, setCustomHeaders] = useState("{}");

  const [preflight, setPreflight] = useState<{ ok: boolean; reason: string } | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<SubmitResult | null>(null);
  const [createdId, setCreatedId] = useState("");

  const authStyle = useMemo(() => PROVIDER_AUTH_STYLE[providerType], [providerType]);

  const onProvider = (p: ProviderType) => {
    setProviderType(p);
    setBaseUri(PROVIDER_BASE_URI_HINTS[p]);
    setModelsPath(p === "openai_compatible" ? "/v1/models" : "/models");
    setChatPath(p === "anthropic" ? "/messages" : "/chat/completions");
    setPreflight(null);
  };

  const buildPayload = (): ChannelCreateInput => ({
    // 名称留空时由后端自动生成，用户不需要填写渠道名，更不需要填写模型名。
    name: name.trim() || `${providerType} 自动发现渠道`,
    provider_type: providerType,
    base_uri: baseUri,
    api_key: apiKey,
    visibility,
    weight,
    priority,
    custom_headers: (() => {
      try {
        const v: unknown = JSON.parse(customHeaders || "{}");
        return typeof v === "object" && v !== null && !Array.isArray(v)
          ? (v as Record<string, string>)
          : {};
      } catch {
        return {};
      }
    })(),
    models_path: modelsPath,
    chat_path: chatPath,
  });

  const testConnection = async () => {
    const pf = preflightBaseUri(baseUri);
    setPreflight(pf);
    const h = await api.healthz();
    setResult({
      ok: pf.ok && !h.unavailable,
      message: pf.ok
        ? h.unavailable
          ? "URL 预检通过，但后端 /healthz 未连接；上游连通性由 POST /api/channels 在服务端验证。"
          : "URL 预检通过，后端可达。提交后站点自动 GET /models 拉取全部真实模型。"
        : `URL 预检拦截：${pf.reason}`,
      detail: `GET ${baseUri.replace(/\/$/, "")}${modelsPath} · ${authStyle}`,
    });
  };

  const submit = async () => {
    setSubmitting(true);
    setResult(null);
    setCreatedId("");
    const pf = preflightBaseUri(baseUri);
    if (!pf.ok) {
      setPreflight(pf);
      setResult({ ok: false, message: `已阻止提交：${pf.reason}` });
      setSubmitting(false);
      return;
    }
    if (!apiKey.trim()) {
      setResult({ ok: false, message: "api_key 为必填项" });
      setSubmitting(false);
      return;
    }
    const res = await api.createChannel(buildPayload());
    setSubmitting(false);
    if (res.unavailable || !res.ok || !res.data) {
      setResult({
        ok: false,
        unavailable: res.unavailable,
        message: res.unavailable
          ? "后端 /api/channels 未连接，渠道未创建。"
          : (res.error?.message ?? `HTTP ${res.status}`),
        detail: res.unavailable ? UNAVAILABLE_HINT : res.error?.code,
      });
      return;
    }
    const data = res.data as unknown as CreateChannelResponse;
    setCreatedId(data.id);
    const discovered = data.models_discovered ?? 0;
    const unavailable = data.probe_status === "unavailable" || data.connection_ok === false;
    setResult({
      ok: true,
      unavailable,
      message: `渠道创建成功：id=${data.id}，key_hint=${data.key_hint}，自动发现真实模型数=${discovered}`,
      detail: unavailable
        ? `上游 /models 未拉取成功（probe_status=unavailable），模型列表保持为空，绝不编造。${data.connection_error ? `真实错误：${data.connection_error}` : ""}`
        : "站点已用该 key 实时拉取真实 /models 并后台采集真实输入/输出样例；下方表格逐字来自上游响应。",
    });
  };

  const inputCls =
    "focus-ring w-full rounded-md border border-border bg-input-bg px-3 py-2 text-sm text-foreground transition-colors duration-quick hover:border-primary/40";
  const labelCls = "mb-1.5 block text-overline uppercase tracking-widest text-muted-foreground";

  return (
    <div className="space-y-6">
      <div className="card-surface p-5 md:p-6">
        <div className="mb-5 rounded-md border border-border bg-muted/30 px-4 py-3 text-xs leading-relaxed text-muted-foreground">
          只填 <span className="font-mono text-foreground">provider_type · base_uri · api_key · visibility</span>。
          提交后站点自动用该密钥实时请求上游 <span className="font-mono text-foreground">GET /models</span>，
          解析全部真实 id / 名称 / 上下文长度 / 价格 / 架构 / 能力位，并对每条模型做一次真实极小调用采集真实输入/输出样例。
          <strong className="font-medium text-foreground"> 不需要填写任何模型名</strong>。
        </div>

        <div className="grid gap-4 md:grid-cols-2">
          <div>
            <label htmlFor="ch-provider" className={labelCls}>provider_type · 供应商类型</label>
            <select id="ch-provider" value={providerType} onChange={(e) => onProvider(e.target.value as ProviderType)} className={inputCls}>
              {(Object.keys(PROVIDER_LABELS) as ProviderType[]).map((p) => (
                <option key={p} value={p}>{PROVIDER_LABELS[p]}</option>
              ))}
            </select>
          </div>
          <div>
            <label htmlFor="ch-vis" className={labelCls}>visibility · 可见性</label>
            <select id="ch-vis" value={visibility} onChange={(e) => setVisibility(e.target.value as Visibility)} className={inputCls}>
              <option value="public">public · 模型进入全局池</option>
              <option value="private">private · 仅贡献者可用</option>
            </select>
          </div>
          <div className="md:col-span-2">
            <label htmlFor="ch-base" className={labelCls}>base_uri</label>
            <input id="ch-base" value={baseUri} onChange={(e) => { setBaseUri(e.target.value); setPreflight(null); }} className={`${inputCls} font-mono text-xs`} />
            <p className="mt-1.5 font-mono text-2xs text-muted-foreground">
              自动填充自 F2 上游常量表；拒绝内网/回环/元数据地址（SSRF 防护，后端再做 DNS 二次校验）。
            </p>
          </div>
          <div className="md:col-span-2">
            <label htmlFor="ch-key" className={labelCls}>api_key</label>
            <input id="ch-key" type="password" value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder="sk-… 或 x-api-key" className={`${inputCls} font-mono text-xs`} autoComplete="off" />
            <p className="mt-1.5 text-2xs text-muted-foreground">
              {authStyle} · 保存后仅返回 key_hint（末 4 位），加密落库，绝不回显明文。
            </p>
          </div>
        </div>

        <details className="mt-5 rounded-md border border-border bg-muted/20 px-4 py-3">
          <summary className="focus-ring cursor-pointer rounded-sm text-xs font-medium text-heading">
            高级选项（可选 · 全部有自动默认值）
          </summary>
          <div className="mt-4 grid gap-4 md:grid-cols-2">
            <div>
              <label htmlFor="ch-name" className={labelCls}>渠道名称（留空自动生成）</label>
              <input id="ch-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="自动生成" className={inputCls} />
            </div>
            <div>
              <label htmlFor="ch-weight" className={labelCls}>weight / priority</label>
              <div className="grid grid-cols-2 gap-2">
                <input id="ch-weight" type="number" value={weight} min={0} onChange={(e) => setWeight(Number(e.target.value))} className={`${inputCls} font-mono`} />
                <input aria-label="priority" type="number" value={priority} min={0} onChange={(e) => setPriority(Number(e.target.value))} className={`${inputCls} font-mono`} />
              </div>
            </div>
            <div>
              <label htmlFor="ch-models" className={labelCls}>models_path</label>
              <input id="ch-models" value={modelsPath} onChange={(e) => setModelsPath(e.target.value)} className={`${inputCls} font-mono text-xs`} />
            </div>
            <div>
              <label htmlFor="ch-chat" className={labelCls}>chat_path</label>
              <input id="ch-chat" value={chatPath} onChange={(e) => setChatPath(e.target.value)} className={`${inputCls} font-mono text-xs`} />
            </div>
            <div className="md:col-span-2">
              <label htmlFor="ch-headers" className={labelCls}>custom_headers (JSON)</label>
              <textarea id="ch-headers" rows={2} value={customHeaders} onChange={(e) => setCustomHeaders(e.target.value)} className={`${inputCls} font-mono text-xs`} />
            </div>
          </div>
        </details>

        <div className="mt-6 flex flex-wrap items-center gap-3 border-t border-border pt-5">
          <button type="button" onClick={() => void testConnection()} className="focus-ring inline-flex items-center gap-2 rounded-md border border-border bg-card px-4 py-2 text-button font-medium text-foreground transition-colors duration-quick hover:bg-card-hover">
            <svg viewBox="0 0 16 16" fill="none" aria-hidden="true" className="size-4">
              <path d="M2.5 8h4l1.5-3 2 6 1.5-3h2" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            测试连接
          </button>
          <button type="button" onClick={() => void submit()} disabled={submitting} className="focus-ring inline-flex items-center rounded-md bg-primary px-5 py-2 text-button font-medium text-primary-foreground transition-[filter] duration-quick hover:brightness-110 disabled:opacity-60">
            {submitting ? "提交中…" : "提交渠道（自动发现模型）"}
          </button>
          <span className="font-mono text-2xs text-muted-foreground">POST /api/channels</span>
        </div>
      </div>

      {preflight && (
        <div className={`rounded-lg border px-4 py-3 text-sm ${preflight.ok ? "border-positive/30 bg-positive/8 text-positive-text" : "border-negative/30 bg-negative/8 text-negative-text"}`}>
          <span className="font-mono text-2xs">{preflight.ok ? "PASS" : "BLOCKED"}</span> · {preflight.reason}
        </div>
      )}
      {result && (
        <div className={`card-surface border-l-4 p-4 ${result.unavailable ? "border-l-warning" : result.ok ? "border-l-positive" : "border-l-warning"}`} role="status">
          <p className="text-sm font-medium text-heading">{result.message}</p>
          {result.detail && <p className="mt-2 text-xs leading-relaxed text-muted-foreground">{result.detail}</p>}
        </div>
      )}

      {createdId && (
        <AutoDiscoveryPanel
          channelId={createdId}
          title={`渠道 ${createdId} · 自动发现的真实模型`}
        />
      )}

      <details className="card-surface group p-4">
        <summary className="focus-ring cursor-pointer rounded-sm text-sm font-medium text-heading">
          请求体预览（POST /api/channels）
        </summary>
        <pre className="mt-3 overflow-x-auto rounded-md border border-border bg-muted/40 p-3 font-mono text-2xs leading-relaxed text-foreground/90">
{JSON.stringify({ ...buildPayload(), api_key: apiKey ? "****" + apiKey.slice(-4) : "" }, null, 2)}
        </pre>
      </details>
    </div>
  );
}
