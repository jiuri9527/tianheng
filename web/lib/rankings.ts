/**
 * 绝对真实模型排行榜 · 本地回退计算
 *
 * 唯一数据源：web/data/models.json（由 scripts/export-models.mjs 从
 * data/models-real.json 逐字导出，460 模型 / 69 供应商）。
 * 所有排名值只取真实字段或可复核换算；不引入调用量/评分/趋势/热度/延迟等编造数据。
 * 线上优先使用 GET /api/rankings（Go 后端同公式计算），未连接时回退本文件。
 */
import { ALL_MODELS, DATASET } from "@/lib/models";
import type { RealModel } from "@/lib/types";

export const DIMENSIONS = [
  "context",
  "price_asc",
  "price_desc",
  "output_asc",
  "capability",
  "newest",
  "max_output",
  "providers",
] as const;
export type Dimension = (typeof DIMENSIONS)[number];

export interface RankEntry {
  rank: number;
  id: string;
  name: string;
  provider: string;
  value: number;
  valueLabel: string;
  contextLength: number;
  maxCompletionTokens: number;
  promptPerMtok: number | null;
  completionPerMtok: number | null;
  paramCount: number;
  inputModalities: string[];
  created: number;
  source: "data/models-real.json";
}

export interface Board {
  dimension: Dimension;
  title: string;
  formula: string;
  valueUnit: string;
  totalModels: number;
  totalProviders: number;
  entryCount: number;
  sourceSha256: string;
  generatedAt: number;
  origin: "api" | "dataset-fallback";
  entries: RankEntry[];
}

interface Meta {
  title: string;
  formula: string;
  unit: string;
  asc: boolean;
}

export const META: Record<Dimension, Meta> = {
  context: {
    title: "上下文长度榜",
    formula: "value = context_length（逐字取自真实记录），降序",
    unit: "tokens",
    asc: false,
  },
  price_asc: {
    title: "输入价格最低榜",
    formula: "value = pricing.prompt × 1e6（真实每 token 美元 → 每 1M token），仅计入 >0 的付费模型，升序",
    unit: "USD / 1M input tokens",
    asc: true,
  },
  price_desc: {
    title: "输入价格最高榜",
    formula: "value = pricing.prompt × 1e6（真实每 token 美元 → 每 1M token），降序",
    unit: "USD / 1M input tokens",
    asc: false,
  },
  output_asc: {
    title: "输出价格最低榜",
    formula: "value = pricing.completion × 1e6（真实每 token 美元 → 每 1M token），仅计入 >0 的付费模型，升序",
    unit: "USD / 1M output tokens",
    asc: true,
  },
  capability: {
    title: "能力覆盖榜",
    formula: "value = len(supported_parameters) + len(input_modalities)（真实数组长度），降序；并列按 id 字典序",
    unit: "count",
    asc: false,
  },
  newest: {
    title: "最新发布榜",
    formula: "value = created（真实 Unix 时间戳），降序",
    unit: "unix_ts",
    asc: false,
  },
  max_output: {
    title: "最大输出榜",
    formula: "value = top_provider.max_completion_tokens（真实字段），降序",
    unit: "tokens",
    asc: false,
  },
  providers: {
    title: "供应商模型数榜",
    formula: "value = 该供应商在真实数据集中的模型条数（按 id 的 '/' 前缀分组计数），降序",
    unit: "models",
    asc: false,
  },
};

const realSha = (): string => {
  const d = DATASET as { source_sha256?: string };
  return typeof d.source_sha256 === "string" ? d.source_sha256 : "";
};

const providerSet = (): number => new Set(ALL_MODELS.map((m) => m.id.split("/")[0])).size;

const price = (v: unknown): number | null => {
  if (v === null || v === undefined || v === "") return null;
  const n = typeof v === "number" ? v : Number(v);
  return Number.isFinite(n) ? n : null;
};

const perMtok = (v: unknown): number | null => {
  const p = price(v);
  return p === null ? null : p * 1e6;
};

export function computeBoard(dim: Dimension): Board {
  const meta = META[dim];
  const entries: RankEntry[] = [];

  if (dim === "providers") {
    const counts = new Map<string, number>();
    for (const m of ALL_MODELS) {
      const p = m.id.split("/")[0];
      counts.set(p, (counts.get(p) ?? 0) + 1);
    }
    for (const [p, n] of counts) {
      entries.push({
        rank: 0, id: p, name: p, provider: p,
        value: n, valueLabel: `${n} 个真实模型`,
        contextLength: 0, maxCompletionTokens: 0,
        promptPerMtok: null, completionPerMtok: null,
        paramCount: 0, inputModalities: [], created: 0,
        source: "data/models-real.json",
      });
    }
  } else {
    for (const m of ALL_MODELS) {
      const params = m.supported_parameters ?? [];
      const inputs = m.architecture?.input_modalities ?? [];
      const prompt = perMtok(m.pricing?.prompt);
      const completion = perMtok(m.pricing?.completion);
      let value = 0;
      let label = "";
      switch (dim) {
        case "context":
          value = m.context_length ?? 0;
          label = `${value} tokens`;
          break;
        case "price_asc":
        case "price_desc":
          value = prompt ?? 0;
          label = value.toFixed(10);
          break;
        case "output_asc":
          value = completion ?? 0;
          label = value.toFixed(10);
          break;
        case "capability":
          value = params.length + inputs.length;
          label = `${params.length} 参数 + ${inputs.length} 输入模态`;
          break;
        case "newest":
          value = m.created ?? 0;
          label = `created=${value}`;
          break;
        case "max_output":
          value = m.top_provider?.max_completion_tokens ?? 0;
          label = `${value} tokens`;
          break;
      }
      if ((dim === "price_asc" || dim === "output_asc") && value <= 0) continue;
      entries.push({
        rank: 0, id: m.id, name: m.name, provider: m.id.split("/")[0],
        value, valueLabel: label,
        contextLength: m.context_length ?? 0,
        maxCompletionTokens: m.top_provider?.max_completion_tokens ?? 0,
        promptPerMtok: prompt, completionPerMtok: completion,
        paramCount: params.length, inputModalities: inputs,
        created: m.created ?? 0,
        source: "data/models-real.json",
      });
    }
  }

  entries.sort((a, b) => {
    if (a.value !== b.value) return meta.asc ? a.value - b.value : b.value - a.value;
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });
  entries.forEach((e, i) => { e.rank = i + 1; });

  return {
    dimension: dim,
    title: meta.title,
    formula: meta.formula,
    valueUnit: meta.unit,
    totalModels: ALL_MODELS.length,
    totalProviders: providerSet(),
    entryCount: entries.length,
    sourceSha256: realSha(),
    generatedAt: Math.floor(Date.now() / 1000),
    origin: "dataset-fallback",
    entries,
  };
}

export function computeAll(): Board[] {
  return DIMENSIONS.map(computeBoard);
}

/* ---------------- 后端 /api/rankings 响应归一化 ---------------- */

interface ApiEntry {
  rank: number; id: string; name: string; provider: string;
  value: number; value_label: string;
  context_length: number; max_completion_tokens: number;
  prompt_price_per_tok: number | null; completion_price_per_tok: number | null;
  supported_parameters: string[] | null; input_modalities: string[] | null;
  created: number; source: string;
}
interface ApiBoard {
  dimension: string; title: string; formula: string; value_unit: string;
  total_models: number; total_providers: number; entry_count: number;
  source_sha256: string; generated_at: number; entries: ApiEntry[];
}
export interface ApiReport {
  total_models: number; total_providers: number; source_sha256: string;
  generated_at: number; dimensions: string[]; boards: Record<string, ApiBoard>;
}

export function normalizeApiReport(rep: ApiReport): Board[] {
  const out: Board[] = [];
  for (const dim of DIMENSIONS) {
    const b = rep.boards?.[dim];
    if (!b) continue;
    out.push({
      dimension: dim,
      title: b.title,
      formula: b.formula,
      valueUnit: b.value_unit,
      totalModels: b.total_models,
      totalProviders: b.total_providers,
      entryCount: b.entry_count,
      sourceSha256: b.source_sha256,
      generatedAt: b.generated_at,
      origin: "api",
      entries: (b.entries ?? []).map((e) => ({
        rank: e.rank, id: e.id, name: e.name, provider: e.provider,
        value: e.value, valueLabel: e.value_label,
        contextLength: e.context_length,
        maxCompletionTokens: e.max_completion_tokens,
        promptPerMtok: e.prompt_price_per_tok === null ? null : e.prompt_price_per_tok * 1e6,
        completionPerMtok: e.completion_price_per_tok === null ? null : e.completion_price_per_tok * 1e6,
        paramCount: (e.supported_parameters ?? []).length,
        inputModalities: e.input_modalities ?? [],
        created: e.created,
        source: "data/models-real.json" as const,
      })),
    });
  }
  return out;
}
