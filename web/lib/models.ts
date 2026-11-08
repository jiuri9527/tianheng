import datasetJson from "@/data/models.json";
import type {
  ModelDataset,
  RealModel,
  ModelPricing,
} from "@/lib/types";

/**
 * 唯一静态真实数据源：web/data/models.json 由 scripts/export-models.mjs
 * 从 data/models-real.json 逐字导出（460 模型 / 69 供应商）。
 * 页面展示的每一个模型都必须来自这里；禁止在页面里手写模型 id。
 */
export const DATASET = datasetJson as unknown as ModelDataset;
export const ALL_MODELS: RealModel[] = DATASET.data;
export const REAL_IDS: ReadonlySet<string> = new Set(
  ALL_MODELS.map((m) => m.id),
);

/* ------------------------------------------------------------------ */
/* 首页 / 可视化所用的真实模型 id（全部已在 data/models-real.json 校验）   */
/* ------------------------------------------------------------------ */

/** 真实首页 Featured Models 板块抓取到的三个 id */
export const FEATURED_MODEL_IDS = [
  "google/gemini-4-argon",
  "openai/gpt-6.1-sol",
  "anthropic/claude-opus-5.5",
] as const;

/** 路由可视化：覆盖 OpenAI / Anthropic / xAI / Gemini / 开源 / 自动路由 */
export const ROUTING_MODEL_IDS = [
  "openai/gpt-6.1-sol",
  "anthropic/claude-opus-5.5",
  "x-ai/grok-4.20",
  "google/gemini-4-argon",
  "deepseek/deepseek-v4.1-flash",
  "openrouter/auto",
] as const;

/** 价格与性能对照表：真实条目，价格/上下文/参数全部取自数据集 */
export const PRICE_COMPARE_IDS = [
  "anthropic/claude-opus-5.5",
  "openai/gpt-6.1-sol",
  "google/gemini-4-argon",
  "qwen/qwen3.8-max-prime",
  "x-ai/grok-4.20",
  "moonshotai/kimi-k3.1-pro",
  "deepseek/deepseek-v4.1-flash",
  "meta-llama/llama-4-maverick",
  "mistralai/mistral-large-4-0",
  "tencent/hy4",
  "minimax/minimax-m3.1-flash",
  "nvidia/nemotron-3.5-super-vl",
  "liquid/lfm-3-1.7b",
  "z-ai/glm-5.3-flashx-legal",
] as const;

const pick = (ids: readonly string[]): RealModel[] =>
  ids
    .map((id) => ALL_MODELS.find((m) => m.id === id))
    .filter((m): m is RealModel => Boolean(m));

export const getFeaturedModels = (): RealModel[] => pick(FEATURED_MODEL_IDS);
export const getRoutingModels = (): RealModel[] => pick(ROUTING_MODEL_IDS);
export const getPriceCompareModels = (): RealModel[] => pick(PRICE_COMPARE_IDS);

/* ------------------------------------------------------------------ */
/* 基础工具                                                            */
/* ------------------------------------------------------------------ */

export const providerOf = (id: string): string => id.split("/")[0];

export const getModelById = (id: string): RealModel | undefined =>
  ALL_MODELS.find((m) => m.id === id);

/** 安全解码 URL 中的模型 id（id 含 "/" 与 ":"，Next 动态路由需 encodeURIComponent） */
export const decodeModelId = (raw: string): string => {
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
};

const PROVIDER_COUNTS = (() => {
  const map = new Map<string, number>();
  for (const m of ALL_MODELS) {
    const p = providerOf(m.id);
    map.set(p, (map.get(p) ?? 0) + 1);
  }
  return map;
})();

export interface ProviderStat {
  name: string;
  count: number;
}

export const getProviders = (): ProviderStat[] =>
  [...PROVIDER_COUNTS.entries()]
    .map(([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));

/** 上游价格单位是 USD/token，换算成 USD / 1M tokens；-1 代表动态路由定价 */
export const pricePerMtok = (raw: string | number | null | undefined): number | null => {
  if (raw === null || raw === undefined || raw === "") return null;
  const n = typeof raw === "number" ? raw : Number(raw);
  if (!Number.isFinite(n) || n < 0) return null;
  return n * 1_000_000;
};

export const promptPricePerMtok = (m: RealModel): number | null =>
  pricePerMtok(m.pricing?.prompt);

export const completionPricePerMtok = (m: RealModel): number | null =>
  pricePerMtok(m.pricing?.completion);

export const formatUsd = (v: number | null, digits = 2): string => {
  if (v === null) return "动态";
  if (v === 0) return "$0";
  if (v < 0.01) return `$${v.toFixed(4)}`;
  return `$${v.toFixed(digits)}`;
};

export const formatContext = (n: number | null | undefined): string => {
  if (!n || n <= 0) return "—";
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n % 1_000_000 === 0 ? 1 : 2)}M`;
  if (n >= 1_000) return `${Math.round(n / 1_000)}K`;
  return String(n);
};

export const formatCount = (n: number): string =>
  n >= 1_000_000
    ? `${(n / 1_000_000).toFixed(1)}M`
    : n >= 1_000
      ? `${(n / 1_000).toFixed(1)}K`
      : String(n);

/** 能力位全部由真实 supported_parameters / architecture 推导，不编造 */
export interface Capabilities {
  vision: boolean;
  tools: boolean;
  reasoning: boolean;
  structuredOutputs: boolean;
  streaming: boolean;
  image: boolean;
  audio: boolean;
  video: boolean;
  file: boolean;
  caching: boolean;
}

export const capabilitiesOf = (m: RealModel): Capabilities => {
  const params = new Set(m.supported_parameters ?? []);
  const ins = new Set(m.architecture?.input_modalities ?? []);
  const outs = new Set(m.architecture?.output_modalities ?? []);
  return {
    vision: ins.has("image"),
    tools: params.has("tools") || params.has("tool_choice"),
    reasoning: params.has("reasoning") || params.has("include_reasoning"),
    structuredOutputs: params.has("response_format") || params.has("structured_outputs"),
    streaming: true, // OpenAI 兼容数据面统一支持 stream（F6），非模型字段猜测
    image: outs.has("image"),
    audio: ins.has("audio") || outs.has("audio"),
    video: ins.has("video") || outs.has("video"),
    file: ins.has("file"),
    caching: Object.prototype.hasOwnProperty.call(m.pricing ?? {}, "input_cache_read"),
  };
};

// 常量外置到 constants.ts，客户端组件可直接引用而不打包整个数据集
export { CAPABILITY_LABELS } from "@/lib/constants";

export const isFree = (m: RealModel): boolean => {
  const p = pricePerMtok(m.pricing?.prompt);
  const c = pricePerMtok(m.pricing?.completion);
  return p === 0 && c === 0;
};

/* ------------------------------------------------------------------ */
/* 模型广场检索 / 排序                                                  */
/* ------------------------------------------------------------------ */

export type ModelSortKey =
  | "context-desc"
  | "price-asc"
  | "price-desc"
  | "name-asc"
  | "params-desc";

export interface ModelQuery {
  q?: string;
  provider?: string;
  capability?: keyof Capabilities | "all";
  sort?: ModelSortKey;
  page?: number;
  pageSize?: number;
}

export interface ModelQueryResult {
  items: RealModel[];
  total: number;
  page: number;
  pageSize: number;
  pages: number;
}

export const queryModels = (query: ModelQuery = {}): ModelQueryResult => {
  const {
    q = "",
    provider = "all",
    capability = "all",
    sort = "context-desc",
    page = 1,
    pageSize = 24,
  } = query;
  const needle = q.trim().toLowerCase();

  let items = ALL_MODELS.filter((m) => {
    if (provider !== "all" && providerOf(m.id) !== provider) return false;
    if (needle) {
      const hay = `${m.id} ${m.name} ${m.architecture?.modality ?? ""}`.toLowerCase();
      if (!hay.includes(needle)) return false;
    }
    if (capability !== "all" && !capabilitiesOf(m)[capability]) return false;
    return true;
  });

  items = [...items].sort((a, b) => {
    switch (sort) {
      case "price-asc":
        return (promptPricePerMtok(a) ?? Number.POSITIVE_INFINITY) -
          (promptPricePerMtok(b) ?? Number.POSITIVE_INFINITY);
      case "price-desc":
        return (promptPricePerMtok(b) ?? -1) - (promptPricePerMtok(a) ?? -1);
      case "name-asc":
        return a.name.localeCompare(b.name);
      case "params-desc":
        return (b.supported_parameters?.length ?? 0) -
          (a.supported_parameters?.length ?? 0);
      case "context-desc":
      default:
        return (b.context_length ?? 0) - (a.context_length ?? 0);
    }
  });

  const total = items.length;
  const pages = Math.max(1, Math.ceil(total / pageSize));
  const safePage = Math.min(Math.max(1, page), pages);
  return {
    items: items.slice((safePage - 1) * pageSize, safePage * pageSize),
    total,
    page: safePage,
    pageSize,
    pages,
  };
};

/** 定价字段键（真实出现的字段，用于详情页完整展示，不做数值加工） */
export const pricingKeys = (p: ModelPricing): string[] =>
  Object.keys(p ?? {}).sort();
