import {
  ALL_MODELS,
  DATASET,
  capabilitiesOf,
  completionPricePerMtok,
  formatContext,
  getProviders,
  isFree,
  pricePerMtok,
  promptPricePerMtok,
  providerOf,
} from "@/lib/models";
import type { RealModel } from "@/lib/types";

/**
 * 所有看板数字都由 web/data/models.json（真实数据集）派生计算，
 * 不使用任何编造的调用量 / QPS / 花费。运行时流量指标由 /api/* 提供。
 */

export interface Datum {
  label: string;
  value: number;
}

export const providerDistribution = (limit = 12): Datum[] =>
  getProviders()
    .slice(0, limit)
    .map((p) => ({ label: p.name, value: p.count }));

export const modalityDistribution = (): Datum[] => {
  const buckets: Record<string, number> = {
    "纯文本": 0,
    "视觉 (image)": 0,
    "多模态 (含音视频/文件)": 0,
    "多输出模态": 0,
  };
  for (const m of ALL_MODELS) {
    const caps = capabilitiesOf(m);
    const modality = m.architecture?.modality ?? "";
    const outputs = m.architecture?.output_modalities ?? [];
    if (outputs.length > 1 || outputs.some((o) => o !== "text")) buckets["多输出模态"] += 1;
    else if (caps.audio || caps.video || caps.file) buckets["多模态 (含音视频/文件)"] += 1;
    else if (caps.vision) buckets["视觉 (image)"] += 1;
    else if (modality.includes("text")) buckets["纯文本"] += 1;
  }
  return Object.entries(buckets).map(([label, value]) => ({ label, value }));
};

export const contextBands = (): Datum[] => {
  const bands: Datum[] = [
    { label: "< 32K", value: 0 },
    { label: "32K–128K", value: 0 },
    { label: "128K–256K", value: 0 },
    { label: "256K–1M", value: 0 },
    { label: "≥ 1M", value: 0 },
  ];
  for (const m of ALL_MODELS) {
    const c = m.context_length ?? 0;
    if (c < 32_000) bands[0].value += 1;
    else if (c < 128_000) bands[1].value += 1;
    else if (c < 256_000) bands[2].value += 1;
    else if (c < 1_000_000) bands[3].value += 1;
    else bands[4].value += 1;
  }
  return bands;
};

export const priceBands = (): Datum[] => {
  const bands: Datum[] = [
    { label: "免费 (0)", value: 0 },
    { label: "< $0.10 / 1M", value: 0 },
    { label: "$0.10–0.50", value: 0 },
    { label: "$0.50–1.00", value: 0 },
    { label: "$1.00–5.00", value: 0 },
    { label: "≥ $5.00", value: 0 },
    { label: "动态定价", value: 0 },
  ];
  for (const m of ALL_MODELS) {
    const p = promptPricePerMtok(m);
    if (p === null) bands[6].value += 1;
    else if (p === 0) bands[0].value += 1;
    else if (p < 0.1) bands[1].value += 1;
    else if (p < 0.5) bands[2].value += 1;
    else if (p < 1) bands[3].value += 1;
    else if (p < 5) bands[4].value += 1;
    else bands[5].value += 1;
  }
  return bands;
};

export const capabilityCoverage = (): Datum[] => {
  const keys = ["vision", "tools", "reasoning", "structuredOutputs", "caching", "file", "audio", "video"] as const;
  const labels: Record<(typeof keys)[number], string> = {
    vision: "视觉输入",
    tools: "工具调用",
    reasoning: "推理",
    structuredOutputs: "结构化输出",
    caching: "Prompt 缓存",
    file: "文件输入",
    audio: "音频",
    video: "视频",
  };
  return keys.map((k) => ({
    label: labels[k],
    value: ALL_MODELS.filter((m) => capabilitiesOf(m)[k]).length,
  }));
};

export interface PricePerfPoint {
  id: string;
  name: string;
  provider: string;
  context: number;
  contextLabel: string;
  prompt: number | null;
  completion: number | null;
}

/** 价格 × 上下文散点：全部真实条目，用于「价格与性能对照」可视化 */
export const pricePerformancePoints = (): PricePerfPoint[] =>
  ALL_MODELS.filter((m) => promptPricePerMtok(m) !== null && m.context_length > 0).map(
    (m) => ({
      id: m.id,
      name: m.name,
      provider: providerOf(m.id),
      context: m.context_length,
      contextLabel: formatContext(m.context_length),
      prompt: promptPricePerMtok(m),
      completion: completionPricePerMtok(m),
    }),
  );

export interface DatasetSummary {
  models: number;
  providers: number;
  freeModels: number;
  multimodalModels: number;
  toolModels: number;
  reasoningModels: number;
  maxContext: number;
  maxContextModelId: string;
  avgContext: number;
  distinctModalities: number;
  distinctPricingKeys: number;
  sha256: string;
}

export const datasetSummary = (): DatasetSummary => {
  const contexts = ALL_MODELS.map((m) => m.context_length ?? 0);
  const maxContext = Math.max(...contexts);
  const maxModel = ALL_MODELS.find((m) => m.context_length === maxContext);
  const modalitySet = new Set(ALL_MODELS.map((m) => m.architecture?.modality ?? ""));
  const pricingKeysSet = new Set<string>();
  for (const m of ALL_MODELS) {
    Object.keys(m.pricing ?? {}).forEach((k) => pricingKeysSet.add(k));
  }
  return {
    models: ALL_MODELS.length,
    providers: getProviders().length,
    freeModels: ALL_MODELS.filter(isFree).length,
    multimodalModels: ALL_MODELS.filter((m) => {
      const c = capabilitiesOf(m);
      return c.vision || c.audio || c.video || c.file;
    }).length,
    toolModels: ALL_MODELS.filter((m) => capabilitiesOf(m).tools).length,
    reasoningModels: ALL_MODELS.filter((m) => capabilitiesOf(m).reasoning).length,
    maxContext,
    maxContextModelId: maxModel?.id ?? "",
    avgContext: Math.round(contexts.reduce((a, b) => a + b, 0) / Math.max(1, contexts.length)),
    distinctModalities: modalitySet.size,
    distinctPricingKeys: pricingKeysSet.size,
    sha256: (DATASET as unknown as { source_sha256?: string }).source_sha256 ?? "", // 由真实数据集元数据读取，不硬编码
  };
};

export const topByContext = (n = 8): RealModel[] =>
  [...ALL_MODELS]
    .sort((a, b) => (b.context_length ?? 0) - (a.context_length ?? 0))
    .slice(0, n);

export const cheapestPaid = (n = 8): RealModel[] =>
  ALL_MODELS.filter((m) => {
    const p = pricePerMtok(m.pricing?.prompt);
    return p !== null && p > 0;
  })
    .sort((a, b) => (promptPricePerMtok(a) ?? 0) - (promptPricePerMtok(b) ?? 0))
    .slice(0, n);
