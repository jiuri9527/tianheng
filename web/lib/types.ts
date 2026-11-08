/** llm-hub 前端统一类型定义 · 与 LLM-API-Aggregator-Prompt.md 第 4/5 节 REST 契约对齐 */

export type ProviderType =
  | "openai"
  | "anthropic"
  | "xai"
  | "gemini"
  | "openai_compatible";

export type Visibility = "public" | "private";
export type ChannelStatus = "active" | "disabled" | "unhealthy" | "pending";
export type ProbeStatus = "unavailable" | "ok" | "failed" | "pending";
export type ParamSource = "upstream" | "builtin" | "probe";
export type UserRole = "admin" | "user";

/* ---------------- 真实静态数据集（data/models-real.json → web/data/models.json） ---------------- */

export interface ModelPricing {
  prompt: string;
  completion: string;
  [key: string]: string | number | null | Record<string, unknown> | unknown;
}

export interface ModelArchitecture {
  modality: string;
  input_modalities: string[];
  output_modalities: string[];
  tokenizer: string | null;
  instruct_type: string | null;
}

export interface TopProvider {
  context_length: number | null;
  max_completion_tokens: number | null;
  is_moderated: boolean | null;
}

/** 逐字来自 https://openrouter.ai/api/v1/models 的模型条目 */
export interface RealModel {
  id: string;
  name: string;
  canonical_slug: string | null;
  description: string;
  created: number;
  context_length: number;
  architecture: ModelArchitecture;
  pricing: ModelPricing;
  top_provider: TopProvider;
  supported_parameters: string[];
}

export interface ModelDataset {
  source: "data/models-real.json";
  upstream: string;
  generated_by: string;
  source_sha256: string;
  total_count: number;
  provider_count: number;
  providers: string[];
  fields: string[];
  data: RealModel[];
}

/* ---------------- 运行时实体（来自 /api/*，未连接后端时为空） ---------------- */

export interface User {
  id: string;
  email: string;
  role: UserRole;
  balance_usd: number;
  status: "active" | "suspended";
  created_at: string;
}

export interface AuthTokens {
  access_token: string;
  refresh_token: string;
}

export interface Channel {
  id: string;
  owner_id: string;
  name: string;
  provider_type: ProviderType;
  base_uri: string;
  models_path: string;
  chat_path: string;
  auth_style: "bearer" | "x-api-key";
  api_version: string | null;
  key_hint: string;
  custom_headers: Record<string, string>;
  visibility: Visibility;
  weight: number;
  priority: number;
  status: ChannelStatus;
  health_checked_at: string | null;
  last_error: string | null;
  fail_count: number;
  models_discovered: number;
  created_at: string;
  updated_at: string;
}

export interface ChannelCreateInput {
  name: string;
  provider_type: ProviderType;
  base_uri: string;
  api_key: string;
  visibility: Visibility;
  weight: number;
  priority: number;
  custom_headers: Record<string, string>;
  models_path: string;
  chat_path: string;
}

/** F3 真实参数发现 + F4 真实探测结果 */
export interface DiscoveredModel {
  upstream_id: string;
  display_name: string;
  alias: string;
  provider_type: ProviderType;
  context_length: number | null;
  max_output_tokens: number | null;
  input_modalities: string[];
  output_modalities: string[];
  supports_vision: boolean;
  supports_tools: boolean;
  supports_reasoning: boolean;
  supports_stream: boolean;
  input_price_per_mtok: number | null;
  output_price_per_mtok: number | null;
  param_source: ParamSource;
  probe_status: ProbeStatus;
  raw_json: Record<string, unknown> | null;
}

export interface ModelProbe {
  input: Record<string, unknown> | null;
  output: string | null;
  prompt_tokens: number | null;
  completion_tokens: number | null;
  latency_ms: number | null;
  ok: boolean;
  error: string | null;
  probed_at: string | null;
}

export interface ChannelDetail extends Channel {
  models: DiscoveredModel[];
  probes: Record<string, ModelProbe>;
  sync_status: "idle" | "syncing" | "unavailable";
  probe_status: ProbeStatus;
}

export interface TokenRecord {
  id: string;
  name: string;
  key_hint: string;
  allowed_models: string[];
  rpm: number;
  tpm: number;
  max_concurrency: number;
  quota_usd: number;
  used_usd: number;
  /** 天衡不设 IP 白名单限制；字段保留仅为兼容，后端恒忽略。 */
  ip_allowlist?: string[];
  expires_at: string | null;
  status: "active" | "revoked" | "expired";
  last_used_at: string | null;
  created_at: string;
}

/**
 * 令牌创建请求。天衡不设任何使用限制：所有限制字段均可省略，
 * 后端会强制置 0/空（0 = 不限），仅 name 必填。
 */
export interface TokenCreateInput {
  name: string;
  /** 兼容字段：后端恒忽略（无模型白名单）。 */
  allowed_models?: string[];
  /** 兼容字段：0 = 不限（后端恒强制 0）。 */
  rpm?: number;
  /** 兼容字段：0 = 不限（后端恒强制 0）。 */
  tpm?: number;
  /** 兼容字段：0 = 不限（后端恒强制 0）。 */
  max_concurrency?: number;
  /** 兼容字段：0 = 不限（后端恒强制 0）。 */
  quota_usd?: number;
  /** 兼容字段：null = 永不过期（后端恒强制 null）。 */
  expires_at?: string | null;
  /** 兼容字段：后端恒忽略（无 IP 白名单）。 */
  ip_allowlist?: string[];
}

export interface RequestLog {
  request_id: string;
  user_id: string;
  token_id: string;
  alias: string;
  model_id: string;
  channel_id: string;
  provider_type: ProviderType;
  stream: boolean;
  request_body: Record<string, unknown> | null;
  response_body: string | null;
  prompt_tokens: number;
  completion_tokens: number;
  ttft_ms: number;
  latency_ms: number;
  status_code: number;
  error_code: string | null;
  cost_usd: number;
  created_at: string;
}

export interface UsagePoint {
  day: string;
  alias: string;
  requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  cost_usd: number;
}

export interface DashboardStats {
  qps: number;
  success_rate: number;
  avg_ttft_ms: number;
  total_tokens: number;
  total_cost_usd: number;
  active_channels: number;
  total_models: number;
  qps_series: { label: string; value: number }[];
  top_models: { label: string; value: number }[];
  channel_health: { name: string; status: ChannelStatus; latency_ms: number | null }[];
}

export interface Settings {
  default_timeout_sec: number;
  max_failover: number;
  circuit_cooldown_sec: number;
  model_sync_cron: string;
  contributor_reward_rate: number;
  registration_open: boolean;
  log_full_body: boolean;
  log_mask_keys: boolean;
  /** 天衡不设任何使用限制：以下两项保留以兼容旧后端响应，语义恒为 0=不限。 */
  rate_rpm_default?: number;
  rate_tpm_default?: number;
  body_limit_mb: number;
}

/* ---------------- API 结果 ---------------- */

export interface ApiErrorBody {
  error: {
    message: string;
    type: "invalid_request_error" | "rate_limit_error" | "upstream_error" | "insufficient_quota";
    code: string;
    request_id: string;
  };
}

export interface ApiResult<T> {
  ok: boolean;
  status: number;
  data: T | null;
  error: ApiErrorBody["error"] | null;
  /** 后端 /api/* 未连接时的真实状态标记，页面据此渲染空态，不填充编造数据 */
  unavailable: boolean;
}

export interface TestConnectionResult {
  ok: boolean;
  latency_ms: number | null;
  error: string | null;
}

export interface SyncModelsResult {
  added: number;
  updated: number;
  removed: number;
}

export interface ProbeResult {
  probed: number;
  failed: number;
}

export interface Paginated<T> {
  items: T[];
  page: number;
  page_size: number;
  total: number;
}
