"use client";

/* eslint-disable @next/next/no-img-element */

import { useState } from "react";

/**
 * 头像上传与个人资料：前端公共工具。
 * 所有数据来自 /api/avatar/* 与 /api/profile（真实后端），未连接时渲染真实空态，
 * 不预置任何假资料、假头像或占位 base64 图片。
 */

export const AVATAR_MAX_BYTES = 2 * 1024 * 1024;
export const AVATAR_ACCEPT = "image/png,image/jpeg,image/webp,image/gif";
export const NICKNAME_MAX_RUNES = 32;
export const BIO_MAX_RUNES = 280;

export interface AvatarMeta {
  user_id: string;
  has_avatar: boolean;
  avatar_id?: string;
  content_type?: string;
  bytes?: number;
  decoded?: boolean;
  has_thumbnail?: boolean;
  ext?: string;
  updated_at?: number;
  url?: string;
  thumb_url?: string;
  original_url?: string;
}

export interface ProfileData {
  user_id: string;
  nickname: string;
  bio: string;
  has_avatar?: boolean;
  avatar_url?: string;
}

export interface ApiState<T> {
  ok: boolean;
  status: number;
  data: T | null;
  unavailable: boolean;
  message: string;
}

export interface MeResponse {
  user_id: string;
  avatar: AvatarMeta;
  profile: ProfileData;
  request_id?: string;
}

export interface ProfileResponse {
  user_id: string;
  profile: ProfileData;
  has_data: boolean;
  limits?: { nickname_runes: number; bio_runes: number };
  request_id?: string;
}

export interface UploadResponse {
  ok: boolean;
  avatar: AvatarMeta;
  profile: ProfileData;
  request_id?: string;
  note?: string;
}

const TOKEN_KEY = "llmhub_access_token";

export function readAccessToken(): string {
  if (typeof window === "undefined") return "";
  return window.localStorage.getItem(TOKEN_KEY) ?? "";
}

export function writeAccessToken(v: string): void {
  if (typeof window !== "undefined") window.localStorage.setItem(TOKEN_KEY, v);
}

export function authHeaders(): Record<string, string> {
  const t = readAccessToken();
  return t ? { Authorization: `Bearer ${t}` } : {};
}

/** 后端资料字段保存的是 html.EscapeString 后的文本，编辑框回填时做实体解码（不执行 HTML）。 */
export function decodeEntities(s: string): string {
  return s
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#34;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, "&");
}

export async function apiJSON<T>(path: string, init: RequestInit = {}): Promise<ApiState<T>> {
  try {
    const res = await fetch(path, {
      ...init,
      cache: "no-store",
      headers: { ...(init.headers as Record<string, string> | undefined), ...authHeaders() },
    });
    const text = await res.text();
    let parsed: unknown = null;
    try {
      parsed = text ? JSON.parse(text) : null;
    } catch {
      parsed = null;
    }
    if (!res.ok) {
      const msg =
        (parsed as { error?: { message?: string } } | null)?.error?.message ??
        `HTTP ${res.status}`;
      return { ok: false, status: res.status, data: null, unavailable: false, message: msg };
    }
    return { ok: true, status: res.status, data: parsed as T, unavailable: false, message: "" };
  } catch {
    return { ok: false, status: 0, data: null, unavailable: true, message: "后端 /api/* 未连接" };
  }
}

/** 头像显示：无 user_id 或图片加载失败时显示中性占位（不伪造头像数据）。 */
export default function Avatar({
  userId,
  size = 48,
  version,
  className = "",
}: {
  userId: string;
  size?: number;
  version?: string;
  className?: string;
}) {
  const [broken, setBroken] = useState(false);
  if (!userId || broken) {
    return (
      <span
        role="img"
        aria-label="暂无头像"
        style={{ width: size, height: size }}
        className={`inline-flex shrink-0 items-center justify-center rounded-full border border-border bg-muted font-mono text-2xs text-muted-foreground ${className}`}
      >
        —
      </span>
    );
  }
  const query = version ? `?v=${encodeURIComponent(version)}` : "";
  const src = `/api/avatar/${encodeURIComponent(userId)}${query}`;
  return (
    <img
      src={src}
      width={size}
      height={size}
      alt="用户头像"
      loading="lazy"
      onError={() => setBroken(true)}
      style={{ width: size, height: size }}
      className={`shrink-0 rounded-full border border-border object-cover ${className}`}
    />
  );
}
