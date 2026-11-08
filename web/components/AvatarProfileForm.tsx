"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import {
  BIO_MAX_RUNES,
  NICKNAME_MAX_RUNES,
  apiJSON,
  decodeEntities,
  readAccessToken,
  type ProfileData,
  type ProfileResponse,
} from "@/components/Avatar";
import EmptyState from "@/components/EmptyState";

interface PutResponse {
  ok: boolean;
  profile: ProfileData;
  limits?: { nickname_runes: number; bio_runes: number; escaped: boolean };
  request_id?: string;
}

const runeCount = (s: string): number => [...s].length;

export default function AvatarProfileForm() {
  const [nickname, setNickname] = useState("");
  const [bio, setBio] = useState("");
  const [loadState, setLoadState] = useState<"loading" | "ok" | "unavailable" | "error">("loading");
  const [hasData, setHasData] = useState(false);
  const [userId, setUserId] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null);

  const load = async () => {
    setLoadState("loading");
    const res = await apiJSON<ProfileResponse>("/api/profile");
    if (res.unavailable) {
      setLoadState("unavailable");
      setMessage(res.message);
      return;
    }
    if (!res.ok) {
      setLoadState("error");
      setMessage(res.message);
      return;
    }
    setUserId(res.data?.user_id ?? "");
    setHasData(Boolean(res.data?.has_data));
    setNickname(decodeEntities(res.data?.profile?.nickname ?? ""));
    setBio(decodeEntities(res.data?.profile?.bio ?? ""));
    setLoadState("ok");
  };

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const save = async () => {
    if (runeCount(nickname) > NICKNAME_MAX_RUNES || runeCount(bio) > BIO_MAX_RUNES) {
      setResult({ ok: false, message: `昵称 ≤ ${NICKNAME_MAX_RUNES} 字符，简介 ≤ ${BIO_MAX_RUNES} 字符` });
      return;
    }
    setBusy(true);
    setResult(null);
    const res = await apiJSON<PutResponse>("/api/profile", {
      method: "PUT",
      headers: { "Content-Type": "application/json", ...(readAccessToken() ? { Authorization: `Bearer ${readAccessToken()}` } : {}) },
      body: JSON.stringify({ nickname, bio }),
    });
    setBusy(false);
    if (res.unavailable) {
      setResult({ ok: false, message: `PUT /api/profile 未送达：${res.message}` });
      return;
    }
    if (!res.ok) {
      setResult({ ok: false, message: `HTTP ${res.status} · ${res.message}` });
      return;
    }
    setHasData(true);
    setResult({
      ok: true,
      message: `已保存。服务端对昵称/简介做 HTML 转义后落盘：nickname=${res.data?.profile?.nickname ?? ""}`,
    });
  };

  const inputCls =
    "focus-ring w-full rounded-md border border-border bg-input-bg px-3 py-2 text-sm text-foreground transition-colors duration-quick hover:border-primary/40";

  return (
    <section className="card-surface p-5 md:p-6" aria-label="个人资料">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="font-brand text-base font-semibold text-heading">个人资料</h2>
          <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">
            昵称 / 简介长度限制 + HTML 转义后写入 <code className="font-mono">cfg.DataDir/profiles.json</code>（原子写、0600、mutex）。
          </p>
        </div>
        <button
          type="button"
          onClick={() => void load()}
          className="focus-ring rounded-md border border-border px-3 py-1.5 text-2xs font-medium text-foreground transition-colors duration-quick hover:bg-card-hover"
        >
          重新读取
        </button>
      </div>

      {loadState === "unavailable" ? (
        <div className="mt-5">
          <EmptyState
            title="资料服务未连接"
            description={`${message}。资料不做预置，数据库为空时显示真实空态，不填充任何假昵称或假简介。`}
            hint="GET /api/profile"
          />
        </div>
      ) : loadState === "error" ? (
        <div className="mt-5 rounded-lg border border-warning/30 bg-warning/8 px-4 py-3 text-xs text-warning-text">
          GET /api/profile 返回错误：{message}。请先填写上方 JWT 或
          <Link href="/login" className="focus-ring mx-1 rounded-sm text-primary hover:underline">登录</Link>。
        </div>
      ) : (
        <>
          {loadState === "ok" && !hasData && (
            <div className="mt-5 rounded-lg border border-dashed border-border bg-muted/20 px-4 py-3 text-xs text-muted-foreground">
              尚未设置资料（真实空态）。保存后才会写入第一条真实记录，服务端不会预置任何假资料。
            </div>
          )}
          {userId && (
            <p className="mt-4 font-mono text-2xs text-muted-foreground">user_id={userId}</p>
          )}

          <div className="mt-4 space-y-4">
            <div>
              <div className="mb-1.5 flex items-center justify-between">
                <label htmlFor="profile-nickname" className="text-overline uppercase tracking-widest text-muted-foreground">
                  昵称
                </label>
                <span className="font-mono text-2xs text-muted-foreground">
                  {runeCount(nickname)} / {NICKNAME_MAX_RUNES}
                </span>
              </div>
              <input
                id="profile-nickname"
                value={nickname}
                onChange={(e) => setNickname(e.target.value)}
                maxLength={NICKNAME_MAX_RUNES}
                placeholder="未设置"
                className={inputCls}
              />
            </div>
            <div>
              <div className="mb-1.5 flex items-center justify-between">
                <label htmlFor="profile-bio" className="text-overline uppercase tracking-widest text-muted-foreground">
                  简介
                </label>
                <span className="font-mono text-2xs text-muted-foreground">
                  {runeCount(bio)} / {BIO_MAX_RUNES}
                </span>
              </div>
              <textarea
                id="profile-bio"
                value={bio}
                onChange={(e) => setBio(e.target.value)}
                maxLength={BIO_MAX_RUNES}
                rows={4}
                placeholder="未设置"
                className={`${inputCls} resize-y`}
              />
            </div>
          </div>

          <div className="mt-5 flex flex-wrap items-center gap-3 border-t border-border pt-5">
            <button
              type="button"
              onClick={() => void save()}
              disabled={busy || loadState === "loading"}
              className="focus-ring rounded-md bg-primary px-5 py-2 text-button font-medium text-primary-foreground transition-[filter] duration-quick hover:brightness-110 disabled:opacity-50"
            >
              {busy ? "保存中…" : "保存资料"}
            </button>
            <span className="font-mono text-2xs text-muted-foreground">PUT /api/profile</span>
          </div>
        </>
      )}

      {result && (
        <div
          role="status"
          className={`mt-4 rounded-md border px-3 py-2.5 text-xs leading-relaxed ${
            result.ok ? "border-positive/30 bg-positive/8 text-positive-text" : "border-warning/30 bg-warning/8 text-warning-text"
          }`}
        >
          {result.message}
        </div>
      )}

      <p className="mt-5 border-t border-border pt-4 font-mono text-2xs leading-relaxed text-muted-foreground">
        GET /api/profile · PUT /api/profile · GET /api/profile/&#123;user_id&#125;（公开视图，不含邮箱/角色/余额）
      </p>
    </section>
  );
}
