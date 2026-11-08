#!/usr/bin/env node
/**
 * 真实数据校验器（SPEC.md C-1 / 验收总纲）
 *
 * 1) web/data/models.json 每个条目的 id/name/context_length/pricing/architecture
 *    必须与 data/models-real.json 逐字（deep-equal）一致；
 * 2) 数量、供应商数、源文件 SHA-256 必须一致；
 * 3) 扫描 web/{app,components,lib} 下所有 .ts/.tsx 里出现的 `provider/model`
 *    字面量，逐个确认真值集合中存在（前端示例模型 id 不得编造）。
 *
 * 运行：node scripts/verify-models.mjs
 */
import { readFileSync, readdirSync, statSync } from "node:fs";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import { dirname, resolve, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(here, "..");
const repoRoot = resolve(webRoot, "..");

const SRC_PATH = resolve(repoRoot, "data/models-real.json");
const OUT_PATH = resolve(webRoot, "data/models.json");

const rawSrc = readFileSync(SRC_PATH, "utf8");
const src = JSON.parse(rawSrc);
const out = JSON.parse(readFileSync(OUT_PATH, "utf8"));

const srcById = new Map(src.data.map((m) => [m.id, m]));
const realIds = new Set(srcById.keys());
const realPrefixes = new Set([...realIds].map((id) => id.split("/")[0]));

const errors = [];
const eq = (a, b) => JSON.stringify(a) === JSON.stringify(b);

// --- 1. 元数据 ---
const sha = createHash("sha256").update(rawSrc).digest("hex");
if (out.source !== "data/models-real.json") errors.push("source 标记错误");
if (out.source_sha256 !== sha) errors.push(`source_sha256 不一致: ${out.source_sha256} != ${sha}`);
if (out.total_count !== src.data.length) errors.push(`total_count ${out.total_count} != ${src.data.length}`);
if (out.data.length !== src.data.length) errors.push(`data.length ${out.data.length} != ${src.data.length}`);
if (out.provider_count !== realPrefixes.size) errors.push(`provider_count ${out.provider_count} != ${realPrefixes.size}`);

// --- 2. 逐字段逐字比对 ---
const CHECK_FIELDS = ["id", "name", "context_length", "pricing", "architecture"];
const seen = new Set();
for (const m of out.data) {
  const s = srcById.get(m.id);
  if (!s) { errors.push(`未知模型 id: ${m.id}`); continue; }
  if (seen.has(m.id)) errors.push(`重复模型 id: ${m.id}`);
  seen.add(m.id);
  for (const f of CHECK_FIELDS) {
    if (!eq(m[f], s[f])) errors.push(`${m.id} 字段 ${f} 与真值不一致`);
  }
}
if (seen.size !== src.data.length) errors.push(`去重后数量 ${seen.size} != ${src.data.length}`);

// --- 3. 扫描前端源码里出现的模型 id 字面量 ---
const scanRoots = ["app", "components", "lib"].map((d) => resolve(webRoot, d));
const sourceFiles = [];
const walk = (p) => {
  let st;
  try { st = statSync(p); } catch { return; }
  if (st.isDirectory()) {
    for (const name of readdirSync(p)) walk(join(p, name));
  } else if (/\.(ts|tsx)$/.test(p)) {
    sourceFiles.push(p);
  }
};
scanRoots.forEach(walk);

const MODEL_ID_RE = /["']([a-z0-9_~][a-z0-9_.:~-]*\/[a-z0-9_.:~+~-]+)["']/g;
const referenced = new Map(); // id -> files
for (const f of sourceFiles) {
  const text = readFileSync(f, "utf8");
  let m;
  while ((m = MODEL_ID_RE.exec(text)) !== null) {
    const candidate = m[1];
    if (!realPrefixes.has(candidate.split("/")[0])) continue; // 过滤路径/URL 等非模型字面量
    if (!referenced.has(candidate)) referenced.set(candidate, new Set());
    referenced.get(candidate).add(f.slice(webRoot.length + 1));
  }
}
for (const [id, files] of referenced) {
  if (!realIds.has(id)) errors.push(`前端引用了编造模型 id: ${id} (${[...files].join(", ")})`);
}

// --- 输出 ---
console.log(`[verify] source=${SRC_PATH}`);
console.log(`[verify] target=${OUT_PATH}`);
console.log(`[verify] source_sha256=${sha}`);
console.log(`[verify] real models=${src.data.length}, exported=${out.data.length}, providers=${realPrefixes.size}`);
console.log(`[verify] deep-equal fields=${CHECK_FIELDS.join(",")} on ${out.data.length} entries`);
console.log(`[verify] scanned ${sourceFiles.length} .ts/.tsx files, found ${referenced.size} distinct real model-id literals`);
console.log(`[verify] referenced ids:`);
for (const id of [...referenced.keys()].sort()) console.log(`  - ${id}`);
if (errors.length) {
  console.error(`[verify] FAILED with ${errors.length} error(s):`);
  for (const e of errors.slice(0, 40)) console.error(`  ! ${e}`);
  process.exit(1);
}
console.log(`[verify] PASS: 0 errors — 全部模型数据可回溯到 data/models-real.json`);
