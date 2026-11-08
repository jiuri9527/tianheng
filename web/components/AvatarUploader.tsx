"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import Avatar, {
  AVATAR_ACCEPT,
  AVATAR_MAX_BYTES,
  apiJSON,
  readAccessToken,
  writeAccessToken,
  type AvatarMeta,
  type MeResponse,
  type UploadResponse,
} from "@/components/Avatar";
import EmptyState from "@/components/EmptyState";

const ALLOWED_TYPES = new Set(["image/png", "image/jpeg", "image/webp", "image/gif"]);

interface LoadState {
  loading: boolean;
  unavailable: boolean;
  message: string;
  userId: string;
  avatar: AvatarMeta | null;
}

export default function AvatarUploader() {
  const [token, setToken] = useState("");
  const [state, setState] = useState<LoadState>({
    loading: true, unavailable: false, message: "", userId: "", avatar: null,
  });
  const [file, setFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null);
  const previewRef = useRef<string | null>(null);

  const load = async () => {
    setState((s) => ({ ...s, loading: true }));
    const res = await apiJSON<MeResponse>("/api/avatar/me");
    if (res.unavailable) {
      setState({ loading: false, unavailable: true, message: res.message, userId: "", avatar: null });
      return;
    }
    if (!res.ok) {
      setState({ loading: false, unavailable: false, message: res.message, userId: "", avatar: null });
      return;
    }
    setState({
      loading: false, unavailable: false, message: "",
      userId: res.data?.user_id ?? "",
      avatar: res.data?.avatar ?? null,
    });
  };

  useEffect(() => {
    setToken(readAccessToken());
    void load();
    return () => {
      if (previewRef.current) URL.revokeObjectURL(previewRef.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onFile = (f: File | null) => {
    if (previewRef.current) {
      URL.revokeObjectURL(previewRef.current);
      previewRef.current = null;
    }
    setResult(null);
    if (!f) {
      setFile(null);
      setPreview(null);
      return;
    }
    if (!ALLOWED_TYPES.has(f.type)) {
      setFile(null);
      setPreview(null);
      setResult({ ok: false, message: `不支持的类型 ${f.type || "(未知)"}；仅允许 PNG / JPEG / WebP / GIF` });
      return;
    }
    if (f.size > AVATAR_MAX_BYTES) {
      setFile(null);
      setPreview(null);
      setResult({ ok: false, message: `文件 ${f.size} 字节超过 2MiB 上限` });
      return;
    }
    const url = URL.createObjectURL(f);
    previewRef.current = url;
    setFile(f);
    setPreview(url);
  };

  const submit = async () => {
    if (!file) {
      setResult({ ok: false, message: "请先选择图片文件" });
      return;
    }
    setBusy(true);
    setResult(null);
    const fd = new FormData();
    // 第三个参数用中性名，明确客户端文件名不参与服务端落盘命名。
    fd.append("avatar", file, "avatar");
    const res = await apiJSON<UploadResponse>("/api/avatar/upload", {
      method: "POST",
      body: fd,
    });
    setBusy(false);
    if (res.unavailable) {
      setResult({ ok: false, message: `POST /api/avatar/upload 未送达：${res.message}` });
      return;
    }
    if (!res.ok) {
      setResult({ ok: false, message: `HTTP ${res.status} · ${res.message}` });
      return;
    }
    setResult({
      ok: true,
      message: `上传成功：avatar_id=${res.data?.avatar?.avatar_id ?? ""}，${res.data?.avatar?.bytes ?? 0} 字节，decoded=${String(res.data?.avatar?.decoded)}，thumbnail=${String(res.data?.avatar?.has_thumbnail)}`,
    });
    setState((s) => ({ ...s, avatar: res.data?.avatar ?? s.avatar, userId: res.data?.avatar?.user_id ?? s.userId }));
  };

  const onToken = (v: string) => {
    setToken(v);
    writeAccessToken(v);
  };

  const inputCls =
    "focus-ring w-full rounded-md border border-border bg-input-bg px-3 py-2 text-sm text-foreground transition-colors duration-quick hover:border-primary/40";

  return (
    <section className="card-surface p-5 md:p-6" aria-label="头像上传">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="font-brand text-base font-semibold text-heading">头像上传</h2>
          <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">
            multipart 字段 <code className="font-mono">avatar</code>；服务端按真实魔数校验，生成 128×128 缩略图，文件名由服务端决定。
          </p>
        </div>
        <button
          type="button"
          onClick={() => void load()}
          className="focus-ring rounded-md border border-border px-3 py-1.5 text-2xs font-medium text-foreground transition-colors duration-quick hover:bg-card-hover"
        >
          刷新状态
        </button>
      </div>

      {/* JWT：登录后由 /api/auth/login 返回，仅存浏览器本地，不上传任何假凭据 */}
      <div className="mt-5">
        <label htmlFor="avatar-jwt" className="mb-1.5 block text-overline uppercase tracking-widest text-muted-foreground">
          JWT access token（POST /api/auth/login 获取）
        </label>
        <input
          id="avatar-jwt"
          type="password"
          value={token}
          onChange={(e) => onToken(e.target.value)}
          placeholder="粘贴 access_token 后自动保存在本机"
          autoComplete="off"
          className={`${inputCls} font-mono text-xs`}
        />
        <p className="mt-1.5 text-2xs text-muted-foreground">
          未登录时 <Link href="/login" className="focus-ring rounded-sm text-primary hover:underline">去登录</Link>；未填写 token 时上传/保存会返回 401 真实错误。
        </p>
      </div>

      {/* 当前头像：真实空态 */}
      <div className="mt-6 rounded-lg border border-border bg-muted/30 p-4">
        {state.loading ? (
          <p className="text-xs text-muted-foreground">读取 GET /api/avatar/me …</p>
        ) : state.unavailable ? (
          <EmptyState
            title="头像服务未连接"
            description={`${state.message}。头像与个人资料均为真实数据，后端 /api/* 未连接时不填充任何占位图或假资料。`}
            hint="GET /api/avatar/me"
          />
        ) : (
          <div className="flex flex-wrap items-center gap-5">
            <Avatar userId={state.userId} version={state.avatar?.avatar_id} size={96} />
            <div className="min-w-0 flex-1">
              <p className="text-sm font-medium text-heading">
                {state.avatar?.has_avatar ? "已有真实头像" : "尚未上传头像"}
              </p>
              {state.userId && (
                <p className="mt-1 truncate font-mono text-2xs text-muted-foreground">user_id={state.userId}</p>
              )}
              {state.avatar?.has_avatar ? (
                <dl className="mt-3 grid grid-cols-2 gap-x-6 gap-y-1.5 text-2xs sm:grid-cols-3">
                  <Meta label="content_type" value={state.avatar.content_type ?? "—"} />
                  <Meta label="bytes" value={String(state.avatar.bytes ?? 0)} />
                  <Meta label="ext" value={state.avatar.ext ?? "—"} />
                  <Meta label="decoded" value={String(state.avatar.decoded ?? false)} />
                  <Meta label="thumbnail" value={String(state.avatar.has_thumbnail ?? false)} />
                  <Meta label="avatar_id" value={state.avatar.avatar_id ?? "—"} />
                </dl>
              ) : (
                <p className="mt-2 text-xs text-muted-foreground">
                  probe 状态：无数据（真实空态）。上传后由服务端写入真实头像文件与元数据。
                </p>
              )}
            </div>
          </div>
        )}
      </div>

      {/* 上传表单 */}
      <div className="mt-5 grid gap-4 sm:grid-cols-[1fr_auto] sm:items-end">
        <div>
          <label htmlFor="avatar-file" className="mb-1.5 block text-overline uppercase tracking-widest text-muted-foreground">
            选择图片（≤ 2MiB）
          </label>
          <input
            id="avatar-file"
            type="file"
            accept={AVATAR_ACCEPT}
            onChange={(e) => onFile(e.target.files?.[0] ?? null)}
            className="focus-ring block w-full rounded-md border border-border bg-input-bg px-3 py-2 text-xs text-foreground file:mr-3 file:rounded-sm file:border-0 file:bg-muted file:px-3 file:py-1 file:text-2xs file:text-foreground"
          />
          <p className="mt-1.5 font-mono text-2xs text-muted-foreground">accept={AVATAR_ACCEPT}</p>
        </div>
        <button
          type="button"
          onClick={() => void submit()}
          disabled={busy || !file}
          className="focus-ring inline-flex h-[38px] items-center justify-center rounded-md bg-primary px-5 text-button font-medium text-primary-foreground transition-[filter] duration-quick hover:brightness-110 disabled:pointer-events-none disabled:opacity-50"
        >
          {busy ? "上传中…" : "上传头像"}
        </button>
      </div>

      {preview && (
        <div className="mt-4 flex items-center gap-3 rounded-md border border-border bg-card px-3 py-2.5">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={preview} alt="本地预览" className="size-12 rounded-sm border border-border object-cover" />
          <div className="min-w-0">
            <p className="truncate text-xs text-heading">{file?.name}</p>
            <p className="font-mono text-2xs text-muted-foreground">{file?.size} bytes · {file?.type} · 仅本地预览</p>
          </div>
        </div>
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

      <ul className="mt-5 space-y-1.5 border-t border-border pt-4 text-2xs leading-relaxed text-muted-foreground">
        <li>· 魔数：PNG 89 50 4E 47 / JPEG FF D8 FF / WebP RIFF….WEBP / GIF 47 49 46 38，并校验 Content-Type 一致。</li>
        <li>· 文件名由服务端随机 id + 用户 id 生成，用户上传的原始文件名被丢弃（防路径穿越 / XSS）。</li>
        <li>· 落盘 cfg.DataDir/avatars/&lt;userid&gt;.&lt;ext&gt;，目录 0700、文件 0600，原子写。</li>
        <li>· PNG/JPEG/GIF 用标准库解码校验并生成 128×128 缩略图；WebP 仅按魔数校验、原样保存并标记 decoded=false。</li>
      </ul>
    </section>
  );
}

function Meta({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="font-mono text-muted-foreground">{label}</dt>
      <dd className="truncate font-mono text-foreground" title={value}>{value}</dd>
    </div>
  );
}
