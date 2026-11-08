#!/usr/bin/env node
/**
 * 真实数据导出器（SPEC.md C-1）
 *
 * 唯一输入：data/models-real.json（HTTP 200 抓取自 https://openrouter.ai/api/v1/models）
 * 唯一输出：web/data/models.json
 *
 * 只做字段投影（逐字复制值），绝不手写、补全、改名或编造任何模型。
 * 运行：node scripts/export-models.mjs
 */
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(here, "..");
const repoRoot = resolve(webRoot, "..");
const SRC = resolve(repoRoot, "data/models-real.json");
const OUT = resolve(webRoot, "data/models.json");

// SPEC 要求导出的真实字段；全部原样取自上游 JSON
const FIELDS = [
  "id",
  "name",
  "canonical_slug",
  "description",
  "created",
  "context_length",
  "architecture",
  "pricing",
  "top_provider",
  "supported_parameters",
];

const raw = readFileSync(SRC, "utf8");
const src = JSON.parse(raw);
if (!src || !Array.isArray(src.data)) {
  throw new Error("models-real.json 缺少 data 数组，拒绝导出");
}

const models = src.data.map((m) => {
  const out = {};
  for (const f of FIELDS) {
    if (Object.prototype.hasOwnProperty.call(m, f)) out[f] = m[f];
  }
  return out;
});

const providers = [
  ...new Set(models.map((m) => String(m.id).split("/")[0])),
].sort();

const payload = {
  source: "data/models-real.json",
  upstream: "https://openrouter.ai/api/v1/models",
  generated_by: "web/scripts/export-models.mjs",
  source_sha256: createHash("sha256").update(raw).digest("hex"),
  total_count: models.length,
  provider_count: providers.length,
  providers,
  fields: FIELDS,
  data: models,
};

mkdirSync(dirname(OUT), { recursive: true });
writeFileSync(OUT, JSON.stringify(payload, null, 2) + "\n", "utf8");

console.log(`[export] SRC=${SRC}`);
console.log(`[export] OUT=${OUT}`);
console.log(`[export] source_sha256=${payload.source_sha256}`);
console.log(`[export] models=${models.length} providers=${providers.length}`);
console.log(`[export] fields=${FIELDS.join(",")}`);
