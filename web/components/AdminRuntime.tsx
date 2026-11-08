"use client";

import { useEffect, useState } from "react";
import { api, authHeaders, UNAVAILABLE_HINT } from "@/lib/api";
import type { Channel, Settings, User } from "@/lib/types";
import EmptyState from "@/components/EmptyState";
import { ChannelStatusBadge, VisibilityBadge } from "@/components/StatusBadge";

type Kind = "channels" | "users" | "settings";

/** F1 / F11 管理面运行时数据：全部来自 /api/*，未连接时展示真实空态与契约 */
export default function AdminRuntime({ kind }: { kind: Kind }) {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [state, setState] = useState<"loading" | "ok" | "unavailable" | "error">("loading");
  const [message, setMessage] = useState("");
  const [feedback, setFeedback] = useState<string | null>(null);

  const endpoint =
    kind === "channels" ? "/api/channels" : kind === "users" ? "/api/admin/users" : "/api/admin/settings";

  const load = async () => {
    setState("loading");
    if (kind === "channels") {
      const r = await api.listChannels();
      if (r.unavailable) { setState("unavailable"); setMessage(UNAVAILABLE_HINT); return; }
      if (!r.ok) { setState("error"); setMessage(r.error?.message ?? ""); return; }
      setChannels(r.data?.items ?? []);
    } else if (kind === "users") {
      const r = await api.adminUsers();
      if (r.unavailable) { setState("unavailable"); setMessage(UNAVAILABLE_HINT); return; }
      if (!r.ok) { setState("error"); setMessage(r.error?.message ?? ""); return; }
      setUsers(r.data?.items ?? []);
    } else {
      const r = await api.adminSettings();
      if (r.unavailable) { setState("unavailable"); setMessage(UNAVAILABLE_HINT); return; }
      if (!r.ok) { setState("error"); setMessage(r.error?.message ?? ""); return; }
      setSettings(r.data);
    }
    setState("ok");
  };

  useEffect(() => { void load(); }, [kind]);

  // 天衡没有角色概念：管理面用户操作只保留真实可用的「余额归零」，
  // 通过 PUT /api/admin/users/{id} 真实提交，不再构造假请求。
  const updateUser = async (id: string) => {
    try {
      const r = await fetch(`/api/admin/users/${id}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json", ...authHeaders() },
        body: JSON.stringify({ balance_usd: 0 }),
      });
      const body = await r.json().catch(() => ({}));
      setFeedback(
        r.ok
          ? `已重置余额：${(body as { email?: string }).email ?? id} → balance_usd=0（HTTP ${r.status}）`
          : `重置失败：HTTP ${r.status} ${JSON.stringify(body).slice(0, 160)}`,
      );
      if (r.ok) await load();
    } catch (e) {
      setFeedback(`重置失败：${(e as Error).message}`);
    }
  };

  const saveSettings = async (patch: Partial<Settings>) => {
    const r = await api.updateSettings(patch);
    setFeedback(r.unavailable ? UNAVAILABLE_HINT : r.ok ? "设置已保存" : r.error?.message ?? "保存失败");
    if (r.ok && r.data) setSettings(r.data);
  };

  if (kind === "settings") {
    // 默认值取自 LLM-API-Aggregator-Prompt.md 第 8 节 .env.example 规范常量
    const defaults: Settings = {
      default_timeout_sec: 180,
      max_failover: 3,
      circuit_cooldown_sec: 30,
      model_sync_cron: "0 */6 * * *",
      contributor_reward_rate: 0.15,
      registration_open: true,
      log_full_body: true,
      log_mask_keys: true,
      body_limit_mb: 20,
      rate_rpm_default: 0,
      rate_tpm_default: 0,
    };
    const fields: { key: keyof Settings; label: string; type: "number" | "text" | "bool" }[] = [
      { key: "default_timeout_sec", label: "DEFAULT_TIMEOUT_SEC", type: "number" },
      { key: "max_failover", label: "MAX_FAILOVER", type: "number" },
      { key: "circuit_cooldown_sec", label: "CIRCUIT_COOLDOWN_SEC", type: "number" },
      { key: "model_sync_cron", label: "MODEL_SYNC_CRON", type: "text" },
      { key: "contributor_reward_rate", label: "CONTRIBUTOR_REWARD_RATE", type: "number" },
      { key: "body_limit_mb", label: "BODY_LIMIT_MB", type: "number" },
      { key: "registration_open", label: "REGISTRATION_OPEN", type: "bool" },
      { key: "log_full_body", label: "LOG_FULL_BODY", type: "bool" },
      { key: "log_mask_keys", label: "LOG_MASK_KEYS", type: "bool" },
    ];
    return (
      <div className="space-y-4">
        <div className="rounded-lg border border-border bg-muted/30 px-4 py-3 font-mono text-2xs text-muted-foreground">
          {state === "unavailable" ? message : state === "ok" ? "GET/PUT /api/admin/settings · 已连接" : `GET ${endpoint} · 读取中…`}
          {" · "}天衡不设任何使用限制，故此处没有限流/配额项；每项修改都会真实 PUT /api/admin/settings 并回写返回值。
        </div>
        <div className="card-surface grid gap-4 p-5 sm:grid-cols-2">
          {fields.map((f) => {
            const value = settings?.[f.key] ?? defaults[f.key];
            return (
              <div key={f.key}>
                <label htmlFor={`set-${f.key}`} className="mb-1.5 block font-mono text-2xs text-muted-foreground">{f.label}</label>
                {f.type === "bool" ? (
                  <button
                    id={`set-${f.key}`}
                    type="button"
                    onClick={() => void saveSettings({ [f.key]: !value } as Partial<Settings>)}
                    className={`focus-ring w-full rounded-md border px-3 py-2 text-left font-mono text-xs ${value ? "border-positive/30 bg-positive/8 text-positive-text" : "border-border bg-input-bg text-muted-foreground"}`}
                  >
                    {String(value)} · 点击切换
                  </button>
                ) : (
                  <input
                    id={`set-${f.key}`}
                    type={f.type === "number" ? "number" : "text"}
                    defaultValue={String(value)}
                    onBlur={(e) => {
                      const raw = e.target.value;
                      const v = f.type === "number" ? Number(raw) : raw;
                      void saveSettings({ [f.key]: v } as Partial<Settings>);
                    }}
                    className="focus-ring w-full rounded-md border border-border bg-input-bg px-3 py-2 font-mono text-xs text-foreground"
                  />
                )}
              </div>
            );
          })}
        </div>
        {feedback && <p className="rounded-lg border border-border bg-card px-4 py-3 text-sm text-heading" role="status">{feedback}</p>}
      </div>
    );
  }

  if (state === "unavailable" || state === "error") {
    return (
      <EmptyState
        title={`${kind === "channels" ? "渠道" : "用户"}列表为空`}
        description={message || `数据源 ${endpoint}；后端未连接时不填充编造行。`}
        hint={`GET ${endpoint}`}
        action={<button type="button" onClick={() => void load()} className="focus-ring rounded-md border border-border px-4 py-2 text-button font-medium text-foreground hover:bg-card-hover">重新连接</button>}
      />
    );
  }

  if (kind === "channels") {
    return (
      <div className="card-surface overflow-x-auto">
        <table className="w-full min-w-[900px] border-collapse text-left">
          <thead>
            <tr className="border-b border-border bg-muted/40 text-overline uppercase tracking-wider text-muted-foreground">
              {["name", "provider_type", "base_uri", "visibility", "status", "models_discovered", "weight / priority", "操作"].map((h) => (
                <th key={h} className="px-4 py-3 font-medium">{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {channels.map((c) => (
              <tr key={c.id} className="border-b border-border/60">
                <td className="px-4 py-3 text-sm text-heading">{c.name}</td>
                <td className="px-4 py-3 font-mono text-2xs text-muted-foreground">{c.provider_type}</td>
                <td className="px-4 py-3 font-mono text-2xs text-muted-foreground">{c.base_uri}</td>
                <td className="px-4 py-3"><VisibilityBadge visibility={c.visibility} /></td>
                <td className="px-4 py-3"><ChannelStatusBadge status={c.status} /></td>
                <td className="px-4 py-3 font-mono text-xs tabular-nums">{c.models_discovered}</td>
                <td className="px-4 py-3 font-mono text-xs tabular-nums">{c.weight} / {c.priority}</td>
                <td className="px-4 py-3">
                  <button type="button" onClick={async () => { const r = await api.syncChannelModels(c.id); setFeedback(r.unavailable ? UNAVAILABLE_HINT : r.ok ? `同步完成：${JSON.stringify(r.data)}` : r.error?.message ?? "失败"); }} className="focus-ring rounded-sm text-2xs font-medium text-primary hover:underline">同步</button>
                </td>
              </tr>
            ))}
            {channels.length === 0 && <tr><td colSpan={8} className="px-4 py-10 text-center text-sm text-muted-foreground">{state === "loading" ? "读取中…" : "暂无运行时渠道记录。"}</td></tr>}
          </tbody>
        </table>
        {feedback && <p className="border-t border-border px-4 py-3 text-xs text-muted-foreground" role="status">{feedback}</p>}
      </div>
    );
  }

  return (
    <div className="card-surface overflow-x-auto">
      <table className="w-full min-w-[720px] border-collapse text-left">
        <thead>
          <tr className="border-b border-border bg-muted/40 text-overline uppercase tracking-wider text-muted-foreground">
            {["email", "role", "balance_usd", "status", "created_at", "操作"].map((h) => <th key={h} className="px-4 py-3 font-medium">{h}</th>)}
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id} className="border-b border-border/60">
              <td className="px-4 py-3 font-mono text-2xs text-foreground">{u.email}</td>
              <td className="px-4 py-3 text-xs text-heading">{u.role}</td>
              <td className="px-4 py-3 font-mono text-xs tabular-nums">{u.balance_usd}</td>
              <td className="px-4 py-3 text-xs text-muted-foreground">{u.status}</td>
              <td className="px-4 py-3 font-mono text-2xs text-muted-foreground">{u.created_at}</td>
              <td className="px-4 py-3">
                <button
                  type="button"
                  onClick={() => void updateUser(u.id)}
                  className="focus-ring rounded-sm text-2xs font-medium text-primary hover:underline"
                >
                  重置余额为 0
                </button>
              </td>
            </tr>
          ))}
          {users.length === 0 && <tr><td colSpan={6} className="px-4 py-10 text-center text-sm text-muted-foreground">{state === "loading" ? "读取中…" : "暂无运行时用户记录。"}</td></tr>}
        </tbody>
      </table>
      {feedback && <p className="border-t border-border px-4 py-3 text-xs text-muted-foreground" role="status">{feedback}</p>}
    </div>
  );
}
