"use client";

import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
  forumApi,
  getForumToken,
  setForumToken,
  clearForumToken,
  FORUM_UNAVAILABLE_HINT,
  type MeView,
  type AuthTokens,
} from "./ForumClient";

interface Props {
  /** 登录状态变化回调（父组件请用 useCallback 保持稳定引用）。 */
  onAuthChange?: (me: MeView | null) => void;
}

/** 论坛身份：调用真实 /api/auth/login|register 获取 JWT，存 sessionStorage。 */
export default function ForumAuth({ onAuthChange }: Props) {
  const [me, setMe] = useState<MeView | null>(null);
  const [token, setToken] = useState("");
  const [mode, setMode] = useState<"login" | "register">("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const refreshMe = useCallback(async () => {
    const t = getForumToken();
    setToken(t);
    if (!t) {
      setMe(null);
      onAuthChange?.(null);
      return;
    }
    const res = await forumApi.me();
    if (res.ok && res.data) {
      setMe(res.data);
      onAuthChange?.(res.data);
    } else {
      setMe(null);
      onAuthChange?.(null);
    }
  }, [onAuthChange]);

  useEffect(() => {
    void refreshMe();
  }, [refreshMe]);

  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setBusy(true);
    setMsg(null);
    const res =
      mode === "login"
        ? await forumApi.login(email, password)
        : await forumApi.register(email, password);
    setBusy(false);
    if (res.unavailable) {
      setMsg({ ok: false, text: FORUM_UNAVAILABLE_HINT });
      return;
    }
    if (!res.ok || !res.data) {
      setMsg({ ok: false, text: res.error?.message ?? `HTTP ${res.status}` });
      return;
    }
    const data = res.data as AuthTokens | { user: MeView; tokens: AuthTokens };
    const access = "tokens" in data ? data.tokens.access_token : data.access_token;
    setForumToken(access);
    setPassword("");
    setMsg({ ok: true, text: "已登录，可参与发帖 / 回复 / 点赞。" });
    await refreshMe();
  };

  const logout = async () => {
    clearForumToken();
    setMsg(null);
    await refreshMe();
  };

  const inputCls =
    "focus-ring w-full rounded-md border border-border bg-input-bg px-3 py-2 text-sm text-foreground transition-colors duration-quick hover:border-primary/40";

  return (
    <div className="card-surface p-5">
      <p className="text-overline uppercase tracking-widest text-primary">Community · 身份</p>
      {me ? (
        <div className="mt-3">
          <h2 className="font-brand text-base font-semibold text-heading">已登录</h2>
          {/* 用户内容一律纯文本渲染，绝不 dangerouslySetInnerHTML */}
          <p className="mt-2 break-all font-mono text-xs text-foreground">{me.email}</p>
          <p className="mt-1 font-mono text-2xs text-muted-foreground">
            role: {me.role} · id: {me.id}
          </p>
          <p className="mt-1 break-all font-mono text-2xs text-muted-foreground">
            JWT: {token.slice(0, 12)}…（仅存于本会话 sessionStorage）
          </p>
          <button
            type="button"
            onClick={() => void logout()}
            className="focus-ring mt-4 rounded-md border border-border bg-card px-3 py-1.5 text-button text-foreground transition-colors duration-quick hover:bg-card-hover"
          >
            退出登录
          </button>
        </div>
      ) : (
        <form className="mt-3 space-y-3" onSubmit={(e) => void submit(e)}>
          <h2 className="font-brand text-base font-semibold text-heading">登录后参与交流</h2>
          <p className="text-xs leading-relaxed text-muted-foreground">
            论坛读接口公开；发帖 / 回复 / 点赞 / 置顶 / 删除需要真实 JWT。未登录时列表仍展示真实数据或真实空态。
          </p>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => setMode("login")}
              className={`focus-ring rounded-sm border px-2.5 py-1 text-2xs ${mode === "login" ? "border-primary bg-primary text-primary-foreground" : "border-border text-muted-foreground"}`}
            >
              登录
            </button>
            <button
              type="button"
              onClick={() => setMode("register")}
              className={`focus-ring rounded-sm border px-2.5 py-1 text-2xs ${mode === "register" ? "border-primary bg-primary text-primary-foreground" : "border-border text-muted-foreground"}`}
            >
              注册
            </button>
          </div>
          <div>
            <label htmlFor="forum-email" className="mb-1 block text-2xs text-muted-foreground">email</label>
            <input id="forum-email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" className={inputCls} />
          </div>
          <div>
            <label htmlFor="forum-password" className="mb-1 block text-2xs text-muted-foreground">password（至少 8 位）</label>
            <input id="forum-password" type="password" required minLength={8} value={password} onChange={(e) => setPassword(e.target.value)} className={inputCls} autoComplete={mode === "register" ? "new-password" : "current-password"} />
          </div>
          <button type="submit" disabled={busy} className="focus-ring w-full rounded-md bg-primary px-3 py-2 text-button font-medium text-primary-foreground transition-[filter] duration-quick hover:brightness-110 disabled:opacity-60">
            {busy ? "提交中…" : mode === "login" ? "登录" : "注册"}
          </button>
        </form>
      )}
      {msg && (
        <div role="status" className={`mt-4 rounded-md border px-3 py-2 text-xs leading-relaxed ${msg.ok ? "border-positive/30 bg-positive/8 text-positive-text" : "border-warning/30 bg-warning/8 text-warning-text"}`}>
          {msg.text}
        </div>
      )}
    </div>
  );
}
