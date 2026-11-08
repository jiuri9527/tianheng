#!/usr/bin/env node
/**
 * 真实语法校验器（依赖不可用时的兜底方案）
 *
 * 使用本地 TypeScript 编译器对 web/** 下每个 .ts / .tsx 做 transpileModule
 * 语法解析（含 JSX），收集 syntactic diagnostics；不做模块解析，不编造通过结果。
 *
 * 运行：
 *   node scripts/check-syntax.mjs
 *   TS_COMPILER_PATH=/path/to/typescript/lib/typescript.js node scripts/check-syntax.mjs
 */
import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { dirname, resolve, join } from "node:path";

const require = createRequire(import.meta.url);
const here = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(here, "..");

const CANDIDATES = [
  process.env.TS_COMPILER_PATH,
  "typescript",
  "/tmp/llmhub-tscheck/node_modules/typescript/lib/typescript.js",
  "/Users/jiuri/.npm-global/lib/node_modules/@vtsls/language-server/node_modules/typescript/lib/typescript.js",
].filter(Boolean);

let ts = null;
let usedPath = "";
for (const c of CANDIDATES) {
  try {
    const p = c.endsWith(".js") ? c : require.resolve(c, { paths: [webRoot] });
    if (existsSync(p)) {
      ts = require(p);
      usedPath = p;
      break;
    }
  } catch { /* try next */ }
}

if (!ts) {
  console.error("[syntax] FAIL: 未找到 TypeScript 编译器；设置 TS_COMPILER_PATH 后重试");
  process.exit(2);
}

const files = [];
const walk = (p) => {
  let st;
  try { st = statSync(p); } catch { return; }
  if (st.isDirectory()) {
    for (const name of readdirSync(p)) {
      if (name === "node_modules" || name === ".next") continue;
      walk(join(p, name));
    }
  } else if (/\.(ts|tsx)$/.test(p) && !p.endsWith(".d.ts")) {
    files.push(p);
  }
};
walk(webRoot);
files.sort();

console.log(`[syntax] compiler=${usedPath}`);
console.log(`[syntax] typescript version=${ts.version}`);
console.log(`[syntax] scanning ${files.length} .ts/.tsx files under ${webRoot}`);

let errors = 0;
for (const f of files) {
  const src = readFileSync(f, "utf8");
  const out = ts.transpileModule(src, {
    fileName: f,
    reportDiagnostics: true,
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.ESNext,
      jsx: f.endsWith(".tsx") ? ts.JsxEmit.ReactJSX : ts.JsxEmit.Preserve,
      isolatedModules: true,
      allowJs: false,
    },
  });
  const diags = (out.diagnostics ?? []).filter(
    (d) => d.category === ts.DiagnosticCategory.Error,
  );
  if (diags.length) {
    errors += diags.length;
    console.log(`  FAIL ${f.slice(webRoot.length + 1)}`);
    for (const d of diags) {
      console.log(`    ${ts.flattenDiagnosticMessageText(d.messageText, " ")}`);
    }
  } else {
    console.log(`  OK   ${f.slice(webRoot.length + 1)}`);
  }
}

console.log(`[syntax] files=${files.length} errors=${errors}`);
if (errors) {
  console.error(`[syntax] FAILED with ${errors} syntax error(s)`);
  process.exit(1);
}
console.log("[syntax] PASS: 0 syntax errors in all .ts/.tsx files");
