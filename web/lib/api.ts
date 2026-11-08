import type {
  ApiResult,
  AuthTokens,
  Channel,
  ChannelCreateInput,
  ChannelDetail,
  DashboardStats,
  Paginated,
  ProbeResult,
  ProviderType,
  RealModel,
  RequestLog,
  Settings,
  SyncModelsResult,
  TestConnectionResult,
  TokenCreateInput,
  TokenRecord,
  UsagePoint,
  User,
} from "@/lib/types";

/**
 * 天衡（Tianheng）前端 API client（零第三方后端依赖）
 * - 默认同源 `/api/*` 与 `/v1/*`，由 Nginx / next.config.js rewrites 转发到 Go 网关；
 * - 未部署后端时 fetch 失败，返回 unavailable:true，页面渲染真实空态，
 *   绝不填充编造的模型、用量或日志；
 * - 所有静态展示数据来自 web/data/models.json（真实数据集）。
 */
const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "";

export const ENDPOINTS = {
  register: "/api/auth/register",
  login: "/api/auth/login",
  refresh: "/api/auth/refresh",
  logout: "/api/auth/logout",
  me: "/api/me",
  channels: "/api/channels",
  models: "/api/models",
  aliases: "/api/aliases",
  tokens: "/api/tokens",
  usage: "/api/usage",
  logs: "/api/logs",
  adminDashboard: "/api/admin/dashboard",
  adminUsers: "/api/admin/users",
  adminSettings: "/api/admin/settings",
  v1Models: "/v1/models",
  healthz: "/healthz",
  metrics: "/metrics",
} as const;

/** F2 上游真实端点常量（LLM-API-Aggregator-Prompt.md 第 1 节），贡献渠道时作下拉提示 */
export const PROVIDER_BASE_URI_HINTS: Record<ProviderType, string> = {
  openai: "https://api.openai.com/v1",
  anthropic: "https://api.anthropic.com/v1",
  xai: "https://api.x.ai/v1",
  gemini: "https://generativelanguage.googleapis.com/v1beta/openai",
  openai_compatible: "https://[HOST]/v1",
};

export const PROVIDER_LABELS: Record<ProviderType, string> = {
  openai: "OpenAI (ChatGPT)",
  anthropic: "Anthropic (Claude)",
  xai: "xAI (Grok)",
  gemini: "Google Gemini",
  openai_compatible: "OpenAI 兼容 / 自定义",
};

export const PROVIDER_AUTH_STYLE: Record<ProviderType, string> = {
  openai: "Authorization: Bearer <key>",
  anthropic: "x-api-key: <key> + anthropic-version: 2023-06-01",
  xai: "Authorization: Bearer <key>",
  gemini: "Authorization: Bearer <key>",
  openai_compatible: "Authorization: Bearer <key>",
};

/** 天衡：JWT 会话令牌存放键（登录/注册成功后写入，登出清除）。 */
export const TOKEN_STORAGE_KEY = "tianheng.jwt";

/** 读取当前浏览器会话中的 JWT（服务端渲染时为 null）。 */
export function getSessionToken(): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage.getItem(TOKEN_STORAGE_KEY);
  } catch {
    return null;
  }
}

/** 写入/清除 JWT 会话令牌。 */
export function setSessionToken(token: string | null): void {
  if (typeof window === "undefined") return;
  try {
    if (token) window.localStorage.setItem(TOKEN_STORAGE_KEY, token);
    else window.localStorage.removeItem(TOKEN_STORAGE_KEY);
  } catch {
    /* localStorage 不可用时静默降级为匿名请求 */
  }
}

/** 构造鉴权头：有 JWT 则带 Bearer，否则匿名（天衡管理面仅需 JWT 身份）。 */
export function authHeaders(): Record<string, string> {
  const t = getSessionToken();
  return t ? { Authorization: `Bearer ${t}` } : {};
}

/** 从后端鉴权响应中提取 access_token（兼容 {access_token} 与 {tokens:{access_token}}）。 */
function extractAccessToken(v: unknown): string | null {
  if (!v || typeof v !== "object") return null;
  const o = v as { access_token?: unknown; token?: unknown; tokens?: unknown };
  if (typeof o.access_token === "string" && o.access_token) return o.access_token;
  if (typeof o.token === "string" && o.token) return o.token;
  if (o.tokens && typeof o.tokens === "object") {
    const t = o.tokens as { access_token?: unknown };
    if (typeof t.access_token === "string" && t.access_token) return t.access_token;
  }
  return null;
}

async function request<T>(
  path: string,
  init: RequestInit = {},
): Promise<ApiResult<T>> {
  try {
    const res = await fetch(`${API_BASE}${path}`, {
      ...init,
      cache: "no-store",
      headers: {
        "Content-Type": "application/json",
        ...authHeaders(),
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
      const err =
        (body as { error?: ApiResult<T>["error"] } | null)?.error ?? null;
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
    return {
      ok: true,
      status: res.status,
      data: body as T,
      error: null,
      unavailable: false,
    };
  } catch {
    return {
      ok: false,
      status: 0,
      data: null,
      error: null,
      unavailable: true,
    };
  }
}

const qs = (params: Record<string, string | number | undefined>): string => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : "";
};

export const api = {
  /* F1 鉴权 */
  register: async (email: string, password: string) => {
    const r = await request<{ user: User; tokens: AuthTokens }>(ENDPOINTS.register, {
      method: "POST",
      body: JSON.stringify({ email, password }),
    });
    const tok = extractAccessToken(r.data?.tokens);
    if (tok) setSessionToken(tok);
    return r;
  },
  login: async (email: string, password: string) => {
    const r = await request<AuthTokens>(ENDPOINTS.login, {
      method: "POST",
      body: JSON.stringify({ email, password }),
    });
    const tok = extractAccessToken(r.data);
    if (tok) setSessionToken(tok);
    return r;
  },
  logout: async () => {
    const r = await request<null>(ENDPOINTS.logout, { method: "POST" });
    setSessionToken(null);
    return r;
  },
  me: () => request<User>(ENDPOINTS.me),

  /* F2 渠道 */
  listChannels: (page = 1, status?: string) =>
    request<Paginated<Channel>>(`${ENDPOINTS.channels}${qs({ page, status })}`),
  createChannel: (input: ChannelCreateInput) =>
    request<{ id: string; key_hint: string; models_discovered: number }>(
      ENDPOINTS.channels,
      { method: "POST", body: JSON.stringify(input) },
    ),
  getChannel: (id: string) =>
    request<ChannelDetail>(`${ENDPOINTS.channels}/${encodeURIComponent(id)}`),
  updateChannel: (id: string, patch: Partial<ChannelCreateInput>) =>
    request<Channel>(`${ENDPOINTS.channels}/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify(patch),
    }),
  deleteChannel: (id: string) =>
    request<null>(`${ENDPOINTS.channels}/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  testChannel: (id: string) =>
    request<TestConnectionResult>(
      `${ENDPOINTS.channels}/${encodeURIComponent(id)}/test`,
      { method: "POST" },
    ),
  syncChannelModels: (id: string) =>
    request<SyncModelsResult>(
      `${ENDPOINTS.channels}/${encodeURIComponent(id)}/sync-models`,
      { method: "POST" },
    ),
  probeChannel: (id: string) =>
    request<ProbeResult>(`${ENDPOINTS.channels}/${encodeURIComponent(id)}/probe`, {
      method: "POST",
    }),

  /* F3 / F4 模型 */
  listModels: (params: { provider?: string; capability?: string; q?: string } = {}) =>
    request<Paginated<RealModel>>(`${ENDPOINTS.models}${qs(params)}`),
  getModel: (id: string) =>
    request<RealModel>(`${ENDPOINTS.models}/${encodeURIComponent(id)}`),
  probeModel: (id: string) =>
    request<ProbeResult>(`${ENDPOINTS.models}/${encodeURIComponent(id)}/probe`, {
      method: "POST",
    }),

  /* F9 令牌 */
  listTokens: () => request<Paginated<TokenRecord>>(ENDPOINTS.tokens),
  createToken: (input: TokenCreateInput) =>
    request<{ token: string; record: TokenRecord }>(ENDPOINTS.tokens, {
      method: "POST",
      body: JSON.stringify(input),
    }),
  revokeToken: (id: string) =>
    request<null>(`${ENDPOINTS.tokens}/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),

  /* F10 计量 / 日志 */
  usage: (from?: string, to?: string, groupBy = "day") =>
    request<UsagePoint[]>(`${ENDPOINTS.usage}${qs({ from, to, group_by: groupBy })}`),
  logs: (params: { request_id?: string; alias?: string; status?: string; q?: string; page?: number } = {}) =>
    request<Paginated<RequestLog>>(`${ENDPOINTS.logs}${qs(params)}`),

  /* F11 管理后台 */
  dashboard: () => request<DashboardStats>(ENDPOINTS.adminDashboard),
  adminUsers: () => request<Paginated<User>>(ENDPOINTS.adminUsers),
  adminSettings: () => request<Settings>(ENDPOINTS.adminSettings),
  updateSettings: (patch: Partial<Settings>) =>
    request<Settings>(ENDPOINTS.adminSettings, {
      method: "PUT",
      body: JSON.stringify(patch),
    }),

  /* F5 数据面 */
  v1Models: () =>
    request<{ object: "list"; data: { id: string; object: "model" }[] }>(
      ENDPOINTS.v1Models,
    ),
  healthz: () => request<{ status: string }>(ENDPOINTS.healthz),
};

export const UNAVAILABLE_HINT = "后端 /api/* 未连接：运行时数据为空，静态展示数据全部来自 data/models-real.json";
