"use client";

/**
 * 社区论坛前端数据层（零第三方依赖，只用浏览器原生 fetch）。
 *
 * 硬约束：
 *  - 数据只从 /api/forum/* 读取；后端未连接时返回 unavailable:true，
 *    页面渲染真实空态，绝不填充任何演示帖子 / 用户 / 点赞数 / 时间。
 *  - 所有用户内容在组件里以 React 文本节点渲染，绝不使用
 *    dangerouslySetInnerHTML；后端已对输入做 sanitizeText + JSON HTML 转义。
 *  - 写操作携带 /api/auth/login 签发的真实 JWT（存 sessionStorage）。
 */

const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "";
const TOKEN_KEY = "llmhub.forum.jwt";

export interface BoardView {
  id: string;
  slug: string;
  name: string;
  description: string;
  post_count: number;
  created_at: number;
}

export interface PostView {
  id: string;
  board_id: string;
  board_slug: string;
  author_id: string;
  author_name: string;
  title: string;
  excerpt: string;
  content?: string;
  pinned: boolean;
  like_count: number;
  reply_count: number;
  liked: boolean;
  created_at: number;
  updated_at: number;
}

export interface ReplyView {
  id: string;
  post_id: string;
  author_id: string;
  author_name: string;
  content: string;
  created_at: number;
}

export interface BoardList {
  items: BoardView[];
  total: number;
}

export interface PostList {
  items: PostView[];
  total: number;
  page: number;
  page_size: number;
  query: string;
  board: string;
}

export interface PostDetail {
  post: PostView;
  replies: ReplyView[];
}

export interface LikeResult {
  post_id: string;
  like_count: number;
  liked: boolean;
}

export interface ActionResult {
  ok: boolean;
  id?: string;
}

export interface ApiError {
  message: string;
  type: string;
  code: string;
  request_id: string;
}

export type ForumResult<T> = {
  ok: boolean;
  status: number;
  data: T | null;
  error: ApiError | null;
  unavailable: boolean;
};

export interface AuthTokens {
  access_token: string;
  refresh_token: string;
  token_type: string;
  expires_in: number;
}

export interface MeView {
  id: string;
  email: string;
  role: string;
}

export interface CreatePostInput {
  board: string;
  board_name?: string;
  board_description?: string;
  title: string;
  content: string;
}

export function getForumToken(): string {
  if (typeof window === "undefined") return "";
  try {
    return window.sessionStorage.getItem(TOKEN_KEY) ?? "";
  } catch {
    return "";
  }
}

export function setForumToken(token: string): void {
  if (typeof window === "undefined") return;
  try {
    if (token) window.sessionStorage.setItem(TOKEN_KEY, token);
    else window.sessionStorage.removeItem(TOKEN_KEY);
  } catch {
    /* sessionStorage 不可用时按未登录处理 */
  }
}

export function clearForumToken(): void {
  setForumToken("");
}

/** 真实 UTC 时间格式（固定时区，避免 SSR / 客户端 hydration 不一致）。 */
export function formatUnix(sec: number): string {
  if (!sec || !Number.isFinite(sec)) return "—";
  const d = new Date(sec * 1000);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getUTCFullYear()}-${p(d.getUTCMonth() + 1)}-${p(d.getUTCDate())} ${p(d.getUTCHours())}:${p(d.getUTCMinutes())} UTC`;
}

const qs = (params: Record<string, string | number | undefined>): string => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : "";
};

async function request<T>(path: string, init: RequestInit = {}): Promise<ForumResult<T>> {
  const token = getForumToken();
  try {
    const res = await fetch(`${API_BASE}${path}`, {
      ...init,
      cache: "no-store",
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        ...(init.headers ?? {}),
      },
    });
    const text = await res.text();
    let body: unknown = null;
    try {
      body = text ? JSON.parse(text) : null;
    } catch {
      body = null;
    }
    if (!res.ok) {
      const err = (body as { error?: ApiError } | null)?.error ?? null;
      return {
        ok: false,
        status: res.status,
        data: null,
        error: err ?? {
          message: `HTTP ${res.status}`,
          type: "upstream_error",
          code: String(res.status),
          request_id: "",
        },
        unavailable: false,
      };
    }
    return { ok: true, status: res.status, data: body as T, error: null, unavailable: false };
  } catch {
    return { ok: false, status: 0, data: null, error: null, unavailable: true };
  }
}

export const forumApi = {
  boards: () => request<BoardList>("/api/forum/boards"),
  posts: (params: { board?: string; q?: string; page?: number; page_size?: number } = {}) =>
    request<PostList>(`/api/forum/posts${qs(params)}`),
  getPost: (id: string) =>
    request<PostDetail>(`/api/forum/posts/${encodeURIComponent(id)}`),
  createPost: (input: CreatePostInput) =>
    request<PostView>("/api/forum/posts", { method: "POST", body: JSON.stringify(input) }),
  reply: (id: string, content: string) =>
    request<ReplyView>(`/api/forum/posts/${encodeURIComponent(id)}/replies`, {
      method: "POST",
      body: JSON.stringify({ content }),
    }),
  like: (id: string) =>
    request<LikeResult>(`/api/forum/posts/${encodeURIComponent(id)}/like`, { method: "POST" }),
  pin: (id: string, pinned: boolean) =>
    request<PostView>(`/api/forum/posts/${encodeURIComponent(id)}/pin`, {
      method: "PUT",
      body: JSON.stringify({ pinned }),
    }),
  remove: (id: string) =>
    request<ActionResult>(`/api/forum/posts/${encodeURIComponent(id)}`, { method: "DELETE" }),
  login: (email: string, password: string) =>
    request<AuthTokens>("/api/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    }),
  register: (email: string, password: string) =>
    request<{ user: MeView; tokens: AuthTokens }>("/api/auth/register", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    }),
  me: () => request<MeView>("/api/me"),
};

export const FORUM_UNAVAILABLE_HINT =
  "后端 /api/forum/* 未连接：论坛数据为空，页面展示真实空态，不填充任何演示内容。";
