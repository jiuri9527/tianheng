"use client";

import { useState } from "react";
import { api, UNAVAILABLE_HINT } from "@/lib/api";
import type { TokenRecord } from "@/lib/types";
import CodeBlock from "@/components/CodeBlock";
import EmptyState from "@/components/EmptyState";

interface Props {
  modelOptions: { id: string; name: string }[];
  exampleModelId: string;
  deployHost: string;
}

export default function TokenManager({ modelOptions, exampleModelId, deployHost }: Props) {
  const [name, setName] = useState("default");
  // 天衡：以下限制字段全部保留（不简化接口形态），但后端一律强制为 0/空 = 不限。
  const [expiresAt, setExpiresAt] = useState("");
  const [allowed, setAllowed] = useState<string[]>([]);
  const [rpm, setRpm] = useState(0);
  const [tpm, setTpm] = useState(0);
  const [maxConcurrency, setMaxConcurrency] = useState(0);
  const [quota, setQuota] = useState(0);
  const [ipAllowlist, setIpAllowlist] = useState("");
  const [busy, setBusy] = useState(false);
  const [createdToken, setCreatedToken] = useState<string | null>(null);
  const [tokens, setTokens] = useState<TokenRecord[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [feedback, setFeedback] = useState<{ ok: boolean; message: string } | null>(null);

  const base = `${deployHost}/v1`;
  const curl = `curl ${base}/chat/completions \\
  -H "Authorization: Bearer ${createdToken ?? "sk-xxxx"}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"${exampleModelId}","messages":[{"role":"user","content":"hi"}],"stream":true}'`;

  const python = `from openai import OpenAI

client = OpenAI(
    base_url="${base}",
    api_key="${createdToken ?? "sk-xxxx"}",
)

resp = client.chat.completions.create(
    model="${exampleModelId}",
    messages=[{"role": "user", "content": "hi"}],
    stream=True,
)
for chunk in resp:
    print(chunk.choices[0].delta.content or "", end="")`;

  const anthropic = `import anthropic

client = anthropic.Anthropic(
    base_url="${deployHost}",
    api_key="${createdToken ?? "sk-xxxx"}",
)

with client.messages.stream(
    model="${exampleModelId}",
    max_tokens=1024,
    messages=[{"role": "user", "content": "hi"}],
) as stream:
    for text in stream.text_stream:
        print(text, end="")`;

  const loadTokens = async () => {
    const res = await api.listTokens();
    setLoaded(true);
    if (res.ok && res.data) setTokens(res.data.items);
  };

  const create = async () => {
    setBusy(true);
    setFeedback(null);
    // 天衡：全部限制字段照常提交以保持接口完整；后端一律强制 0/空 = 不限。
    const res = await api.createToken({
      name,
      allowed_models: allowed,
      rpm,
      tpm,
      max_concurrency: maxConcurrency,
      quota_usd: quota,
      expires_at: expiresAt || null,
      ip_allowlist: ipAllowlist ? ipAllowlist.split(",").map((v) => v.trim()).filter(Boolean) : [],
    });
    setBusy(false);
    if (res.unavailable) {
      setFeedback({ ok: false, message: `POST /api/tokens 未送达：${UNAVAILABLE_HINT}` });
      return;
    }
    if (!res.ok) {
      setFeedback({ ok: false, message: res.error?.message ?? "创建失败" });
      return;
    }
    setCreatedToken(res.data?.token ?? null);
    setFeedback({ ok: true, message: "令牌已创建；明文仅此一次显示，请立即复制保存。" });
    await loadTokens();
  };

  const revoke = async (id: string) => {
    const res = await api.revokeToken(id);
    setFeedback(
      res.unavailable
        ? { ok: false, message: UNAVAILABLE_HINT }
        : { ok: res.ok, message: res.ok ? "令牌已吊销" : res.error?.message ?? "吊销失败" },
    );
    if (res.ok) setTokens((prev) => prev.filter((t) => t.id !== id));
  };

  const inputCls = "focus-ring w-full rounded-md border border-border bg-input-bg px-3 py-2 text-sm text-foreground transition-colors duration-quick hover:border-primary/40";
  const labelCls = "mb-1.5 block text-overline uppercase tracking-widest text-muted-foreground";

  return (
    <div className="space-y-6">
      <div className="card-surface p-5 md:p-6">
        <h3 className="text-sm font-semibold text-heading">创建平台令牌 (F9)</h3>
        <p className="mt-1 text-xs text-muted-foreground">
          生成 sk-&lt;32位随机&gt; 平台令牌；数据库只存 sha256，明文仅在创建响应中返回一次。
          天衡所有用户都是超级用户、且不设任何使用限制，因此创建时只需填写名称。
        </p>
        <div className="mt-5 grid gap-4 md:grid-cols-2">
          <div>
            <label htmlFor="tk-name" className={labelCls}>name</label>
            <input id="tk-name" value={name} onChange={(e) => setName(e.target.value)} className={inputCls} />
          </div>
          <div>
            <label htmlFor="tk-expires" className={labelCls}>expires_at（天衡恒为永不过期）</label>
            <input id="tk-expires" type="date" value={expiresAt} onChange={(e) => setExpiresAt(e.target.value)} className={inputCls} />
            <p className="mt-1 text-2xs text-muted-foreground">保留该控件仅为完整展示令牌字段；后端强制 null（永不过期）。</p>
          </div>
          <div className="md:col-span-2">
            <label htmlFor="tk-models" className={labelCls}>allowed_models（天衡无模型白名单，恒为全部）</label>
            <select
              id="tk-models"
              multiple
              size={5}
              value={allowed}
              onChange={(e) => setAllowed([...e.target.selectedOptions].map((o) => o.value))}
              className={`${inputCls} font-mono text-xs`}
            >
              {modelOptions.map((m) => (
                <option key={m.id} value={m.id}>{m.id} — {m.name}</option>
              ))}
            </select>
            <p className="mt-1.5 text-2xs text-muted-foreground">
              选项逐字来自 data/models-real.json，共 {modelOptions.length} 条真实模型；天衡不设白名单，任何令牌均可调用全部模型。
            </p>
          </div>
          <div>
            <label htmlFor="tk-rpm" className={labelCls}>rpm（天衡不限）</label>
            <input id="tk-rpm" type="number" value={rpm} onChange={(e) => setRpm(Number(e.target.value))} className={`${inputCls} font-mono`} />
          </div>
          <div>
            <label htmlFor="tk-tpm" className={labelCls}>tpm（天衡不限）</label>
            <input id="tk-tpm" type="number" value={tpm} onChange={(e) => setTpm(Number(e.target.value))} className={`${inputCls} font-mono`} />
          </div>
          <div>
            <label htmlFor="tk-conc" className={labelCls}>max_concurrency（天衡不限）</label>
            <input id="tk-conc" type="number" value={maxConcurrency} onChange={(e) => setMaxConcurrency(Number(e.target.value))} className={`${inputCls} font-mono`} />
          </div>
          <div>
            <label htmlFor="tk-quota" className={labelCls}>quota_usd（天衡不限）</label>
            <input id="tk-quota" type="number" step="0.01" value={quota} onChange={(e) => setQuota(Number(e.target.value))} className={`${inputCls} font-mono`} />
          </div>
          <div>
            <label htmlFor="tk-ip" className={labelCls}>ip_allowlist（天衡不设 IP 白名单）</label>
            <input id="tk-ip" value={ipAllowlist} onChange={(e) => setIpAllowlist(e.target.value)} placeholder="留空即不限制" className={`${inputCls} font-mono text-xs`} />
          </div>
          <div className="sm:col-span-2 rounded-md border border-positive/25 bg-positive/8 px-3 py-2 text-2xs leading-relaxed text-positive-text">
            天衡不设任何使用限制：无模型白名单、无 RPM/TPM、无并发上限、无配额、无 IP 白名单、无过期时间。令牌创建后即可调用全部真实模型。
          </div>
        </div>
        <div className="mt-5 flex flex-wrap items-center gap-3 border-t border-border pt-5">
          <button type="button" onClick={create} disabled={busy} className="focus-ring rounded-md bg-primary px-5 py-2 text-button font-medium text-primary-foreground transition-[filter] duration-quick hover:brightness-110 disabled:opacity-60">
            {busy ? "创建中…" : "创建令牌"}
          </button>
          <button type="button" onClick={loadTokens} className="focus-ring rounded-md border border-border px-4 py-2 text-button font-medium text-foreground transition-colors duration-quick hover:bg-card-hover">
            刷新令牌列表
          </button>
          <span className="font-mono text-2xs text-muted-foreground">POST /api/tokens</span>
        </div>
      </div>

      {feedback && (
        <div className={`rounded-lg border px-4 py-3 text-sm ${feedback.ok ? "border-positive/30 bg-positive/8 text-positive-text" : "border-warning/30 bg-warning/8 text-warning-text"}`} role="status">
          {feedback.message}
        </div>
      )}

      {createdToken && (
        <div className="card-surface border-l-4 border-l-positive p-5">
          <p className="text-overline uppercase tracking-widest text-positive-text">一次性明文令牌</p>
          <code className="mt-3 block break-all rounded-md border border-border bg-muted/50 px-4 py-3 font-mono text-sm text-foreground">
            {createdToken}
          </code>
          <p className="mt-2 text-2xs text-muted-foreground">刷新页面后不再显示；数据库仅保存 sha256 哈希。</p>
        </div>
      )}

      {/* 自动生成的调用示例 */}
      <div>
        <h3 className="mb-4 font-brand text-base font-semibold text-heading">可直接粘贴的调用示例</h3>
        <div className="grid gap-4 lg:grid-cols-3">
          <CodeBlock filename="curl · OpenAI 兼容 /v1/chat/completions" language="bash" code={curl} />
          <CodeBlock filename="python · OpenAI SDK" language="python" code={python} />
          <CodeBlock filename="python · Anthropic SDK (/v1/messages)" language="python" code={anthropic} />
        </div>
        <p className="mt-3 font-mono text-2xs text-muted-foreground">
          base_url = {base} · 示例模型 {exampleModelId} 来自 data/models-real.json
        </p>
      </div>

      {/* 令牌列表 */}
      <div>
        <h3 className="mb-4 font-brand text-base font-semibold text-heading">我的令牌</h3>
        {loaded && tokens.length === 0 ? (
          <EmptyState
            title="令牌列表为空"
            description="令牌记录来自 GET /api/tokens；后端未连接时不填充编造行。"
            hint="GET /api/tokens"
          />
        ) : (
          <div className="card-surface overflow-x-auto">
            <table className="w-full min-w-[720px] border-collapse text-left">
              <thead>
                <tr className="border-b border-border bg-muted/40 text-overline uppercase tracking-wider text-muted-foreground">
                  <th className="px-4 py-3 font-medium">name</th>
                  <th className="px-3 py-3 font-medium">key_hint</th>
                  <th className="px-3 py-3 font-medium text-right">限流（天衡不限）</th>
                  <th className="px-3 py-3 font-medium text-right">配额 / 已用</th>
                  <th className="px-3 py-3 font-medium">status</th>
                  <th className="px-3 py-3 font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {tokens.map((t) => (
                  <tr key={t.id} className="border-b border-border/60">
                    <td className="px-4 py-3 text-sm text-heading">{t.name}</td>
                    <td className="px-3 py-3 font-mono text-2xs text-muted-foreground">{t.key_hint}</td>
                    <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">{t.rpm === 0 && t.tpm === 0 ? "不限" : `${t.rpm} / ${t.tpm}`}</td>
                    <td className="px-3 py-3 text-right font-mono text-xs tabular-nums">{t.quota_usd === 0 ? "不限" : `${t.quota_usd}`} / {t.used_usd}</td>
                    <td className="px-3 py-3 text-xs text-muted-foreground">{t.status}</td>
                    <td className="px-3 py-3">
                      <button type="button" onClick={() => revoke(t.id)} className="focus-ring rounded-sm text-2xs font-medium text-negative-text hover:underline">吊销</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
