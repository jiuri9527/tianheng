"use client";

import { useState } from "react";
import Link from "next/link";
import { api, UNAVAILABLE_HINT } from "@/lib/api";

export default function AuthForm({ mode }: { mode: "login" | "register" }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null);
  const isRegister = mode === "register";

  const submit = async () => {
    setBusy(true);
    setResult(null);
    const res = isRegister ? await api.register(email, password) : await api.login(email, password);
    setBusy(false);
    if (res.unavailable) { setResult({ ok: false, message: UNAVAILABLE_HINT }); return; }
    if (!res.ok) { setResult({ ok: false, message: res.error?.message ?? `HTTP ${res.status}` }); return; }
    setResult({
      ok: true,
      message: isRegister
        ? "注册成功：你已是超级用户（天衡没有管理员角色，所有用户权限相同），已签发 JWT access + refresh。"
        : "登录成功：已签发 JWT access_token + refresh_token，管理面 /api/* 使用 Bearer JWT。",
    });
  };

  const inputCls = "focus-ring w-full rounded-md border border-border bg-input-bg px-3 py-2 text-sm text-foreground transition-colors duration-quick hover:border-primary/40";

  return (
    <div className="mx-auto w-full max-w-md px-4 py-20 md:py-28">
      <div className="card-surface p-7">
        <p className="text-overline uppercase tracking-widest text-primary">F1 · 用户与鉴权</p>
        <h1 className="mt-2 font-brand text-display font-bold tracking-tight text-heading">
          {isRegister ? "创建账户" : "登录"}
        </h1>
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
          {isRegister
            ? "邮箱 + 密码（bcrypt/argon2 哈希）；注册即超级用户——天衡没有管理员，也没有任何使用限制。"
            : "JWT access + refresh；退出、改密、角色管理在管理后台。"}
        </p>

        <form
          className="mt-7 space-y-4"
          onSubmit={(e) => { e.preventDefault(); void submit(); }}
        >
          <div>
            <label htmlFor="auth-email" className="mb-1.5 block text-overline uppercase tracking-widest text-muted-foreground">email</label>
            <input id="auth-email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" className={inputCls} />
          </div>
          <div>
            <label htmlFor="auth-password" className="mb-1.5 block text-overline uppercase tracking-widest text-muted-foreground">password</label>
            <input id="auth-password" type="password" required minLength={8} value={password} onChange={(e) => setPassword(e.target.value)} placeholder="至少 8 位" className={inputCls} autoComplete={isRegister ? "new-password" : "current-password"} />
          </div>
          <button type="submit" disabled={busy} className="focus-ring w-full rounded-md bg-primary px-4 py-2.5 text-button font-medium text-primary-foreground transition-[filter] duration-quick hover:brightness-110 disabled:opacity-60">
            {busy ? "提交中…" : isRegister ? "注册" : "登录"}
          </button>
        </form>

        {result && (
          <div className={`mt-5 rounded-md border px-3 py-2.5 text-xs leading-relaxed ${result.ok ? "border-positive/30 bg-positive/8 text-positive-text" : "border-warning/30 bg-warning/8 text-warning-text"}`} role="status">
            {result.message}
          </div>
        )}

        <p className="mt-6 border-t border-border pt-5 text-center text-xs text-muted-foreground">
          {isRegister ? (
            <>已有账户？<Link href="/login" className="focus-ring rounded-sm font-medium text-primary hover:underline">去登录</Link></>
          ) : (
            <>还没有账户？<Link href="/register" className="focus-ring rounded-sm font-medium text-primary hover:underline">去注册</Link></>
          )}
        </p>
        <p className="mt-3 text-center font-mono text-2xs text-muted-foreground">
          {isRegister ? "POST /api/auth/register" : "POST /api/auth/login"}
        </p>
      </div>
    </div>
  );
}
