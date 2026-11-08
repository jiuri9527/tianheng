#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
verify/check_real_data.py
=========================================================================
llm-hub · 真实模型数据合规校验（团队 8 / 功能审查核实）

零第三方依赖：仅使用 Python 标准库（os / re / sys / json / hashlib / datetime）。
Python 3.9 兼容。

职责（对应 SPEC.md C-1）：
  1. 读取唯一静态真实数据源 data/models-real.json，构建：
       真实模型 id 集合、供应商集合（69）、
       上游元数据 slug 集合（canonical_slug / hugging_face_id）。
  2. 对交付物内所有"模型数据集 JSON"副本（形如 {"data":[{id,...}]} 或
     [{id,...}] 的 .json，例如 src/internal/models/models-real.json、
     web/data/models.json）用结构化方式逐条校验 id ∈ 真值集合、
     比对 SHA-256、并逐字段比对 name/context_length/pricing 等真实字段。
  3. 扫描其余交付文本文件，抽取"模型语境"下的 `provider/model` 候选，
     逐一比对真实 id 集合：真实命中数、可疑编造项（真正伪造的模型名，
     含文件:行号与上下文）、以及真值的模型总数/供应商数/SHA-256。
  4. 已知模型短名/别名单独核查是否有真实对应 id。

-------------------------------------------------------------------------
候选抽取规则（按 Lead 复核意见修正，杜绝路径/URL/Go/CSS 误报）
-------------------------------------------------------------------------
只有同时满足以下全部条件的 slashed token 才纳入"模型 id 候选"：

  R1 形态为 `<provider>/<model>`（允许 `~` 前缀、`:` 后缀）。
  R2 provider 前缀必须是 data/models-real.json 中 69 个真实供应商之一
     （或其去 `~` 形式）；或 model 部分命中已知模型族且 provider 不是路径词。
  R3 处于"模型语境"之一：
       (a) 被反引号 / 双引号 / 单引号包围；
       (b) Markdown 表格单元格（行以 | 开头）；
       (c) JSON 的 "id"/"model"/"alias"/"upstream_id" 等字段值；
       (d) 围栏代码块（``` / ~~~）内的字符串字面量；
       (e) provider 本身是真实供应商前缀（该前缀不会出现在路径/Go/CSS 中）。
  R4 显式排除：
       - 路径前缀 data/ docs/ src/ web/ kernel/ verify/ patches/ tests/
         deploy/ migrations/ api/ cmd/ internal/ lib/ components/ app/
         public/ assets/ http:// https:// file:// gopher:// ；
       - model 部分带文件扩展名（.json/.go/.ts/.tsx/.js/.md/.sql/…）；
       - Go 包名/类型名、端点词、供应商类型词（TYPE_WORDS 表）；
       - Tailwind / CSS class（其 provider 段不会是真实供应商前缀）。
  R5 BASELINE（Lead 冻结：SPEC.md / LLM-API-Aggregator-Prompt.md /
     README.md / go.mod / …）与 GROUND_TRUTH_REF（data/**，含
     data/models-real-summary.md、data/openrouter-home.html）中的 token
     归入 information-only，绝不判定为"可疑编造项"。
  R6 「负例/校验夹具」token（在校验脚本的候选列表、HAS=False 断言、
     禁止写入清单、grep -c=0 表格中出现，即文档明确断言该 id 不存在）
     归入 information-only，逐条给 文件:行号，不计入"可疑编造项"。
     —— 只有被当作真实静态模型写入交付物的 ∉真值 id 才是致命伪造。

判定口径：
  - "模型 id" 特指上游 JSON 的 "id" 字段。canonical_slug /
    hugging_face_id 是同一真实模型的上游元数据字段，不是模型 id，
    命中者归为 KNOWN_METADATA，不计入编造。
  - 交付物内的数据集 JSON 用结构化 id 校验，不对整行做正则抽取。
  - verify/** 是校验脚本自身，data/models-real.json 与
    data/models-real-summary.json 是基准真值本身，均不计入候选扫描。

所有输出来自本地文件真实读取；不联网、不生成、不臆测任何模型。
退出码：0 = 扫描完成；2 = 基准真值文件缺失或格式错误。
=========================================================================
"""
import os
import re
import sys
import json
import hashlib
import datetime

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TRUTH_PATH = os.path.join(ROOT, "data", "models-real.json")
SUMMARY_JSON_PATH = os.path.join(ROOT, "data", "models-real-summary.json")
SUMMARY_MD_PATH = os.path.join(ROOT, "data", "models-real-summary.md")

SPEC_SHA256 = "08b4635fd684689feda841f906d59e0dcd8ad9dd57a7c0b7f0667d46b740876a"
SPEC_MODELS = 460
SPEC_PROVIDERS = 69

BASELINE_FILES = {
    "SPEC.md", "LLM-API-Aggregator-Prompt.md", "README.md",
    "go.mod", "go.sum", "SECURITY.md",
}
SKIP_TOP_DIRS = {"verify", ".git", "node_modules", "vendor", "dist",
                 "build", ".next", "coverage"}
SKIP_EXACT_REL = {
    os.path.join("data", "models-real.json"),
    os.path.join("data", "models-real-summary.json"),
}

PATH_PREFIXES = {
    "data", "docs", "src", "web", "kernel", "verify", "patches", "tests",
    "deploy", "migrations", "api", "cmd", "internal", "lib", "components",
    "app", "public", "assets", "styles", "types", "hooks", "utils",
    "http", "https", "http:", "https:", "file", "file:", "gopher",
    "gopher:", "ftp", "node_modules", "vendor", "dist", "build",
}
TYPE_WORDS = {
    "openai", "xai", "anthropic", "gemini", "gemini-compat", "grok",
    "claude", "gpt", "chatgpt", "openai_compatible", "openai-compatible",
    "api", "v1", "v1beta", "internal", "src", "docs", "web", "kernel",
    "data", "messages", "completions", "chat", "responses", "embeddings",
    "images", "audio", "healthz", "metrics", "auth", "channels", "tokens",
    "usage", "logs", "admin", "settings", "dashboard", "models", "model",
    "compat", "compatible", "providers", "provider", "endpoints", "details",
    "net", "crypto", "context", "encoding", "io", "os", "fmt", "log",
    "slog", "time", "sync", "bufio", "strconv", "strings", "errors",
}
CODE_EXT_IN_MODELPART = {
    ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".json", ".md",
    ".sql", ".html", ".htm", ".yaml", ".yml", ".sh", ".bash", ".conf",
    ".toml", ".py", ".java", ".rb", ".php", ".css", ".scss", ".xml",
    ".patch", ".diff", ".txt", ".env", ".ini", ".cfg", ".svg", ".lock",
    ".mod", ".sum", ".tf", ".properties", ".vue", ".svelte",
}
# R6：负例/校验夹具的语境标记
NEG_WORDS = (
    "= false", "in_real=false", "in_dataset=false", "has '", "has \"",
    "禁止", "被禁", "不得", "不存在", "不真实", "未命中", "grep -c",
    "计数为 0", "= 0", "禁写", "反例", "负例", "伪造", "手写",
)
PROBE_FILE_MARKERS = ("print('has", "print(\"has", "in_real=", "in_dataset=",
                      "has '", "has \"")

TEXT_EXT = {
    ".md", ".markdown", ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
    ".json", ".html", ".htm", ".css", ".scss", ".yaml", ".yml", ".sql",
    ".sh", ".bash", ".zsh", ".txt", ".conf", ".toml", ".ini", ".cfg",
    ".xml", ".svg", ".vue", ".svelte", ".patch", ".diff", ".env",
    ".example", ".properties", ".tf", ".mod", ".sum", ".lock", ".gradle",
}
NAMELESS_OK = {
    "Makefile", "Dockerfile", "README", "LICENSE", "SECURITY",
    ".env.example", "nginx.conf", "docker-compose.yml", "Procfile",
    ".gitignore", ".dockerignore",
}
BINARY_EXT = {
    ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".bmp", ".tiff",
    ".woff", ".woff2", ".ttf", ".eot", ".otf", ".pdf", ".zip", ".gz",
    ".tgz", ".bz2", ".xz", ".7z", ".bin", ".so", ".dylib", ".dll",
    ".exe", ".o", ".a", ".wasm", ".mp4", ".mp3", ".wav", ".pcap",
    ".pcapng", ".class", ".jar", ".pyc", ".ds_store",
}

SLASH_RE = re.compile(
    r"(?<![A-Za-z0-9_/.:-])"
    r"(~?[A-Za-z0-9][A-Za-z0-9._~-]*)/([A-Za-z0-9][A-Za-z0-9._:+-]*)"
)
MODEL_FAMILY_RE = re.compile(
    r"(gpt|grok|claude|gemini|llama|qwen|deepseek|mistral|gemma|phi|"
    r"command[-_]?r|sonnet|haiku|opus|o[1345](?:[\-_.]|$))", re.I
)
BARE_RE = re.compile(
    r"(?<![A-Za-z0-9_/.:~-])"
    r"(gpt-[A-Za-z0-9._:-]+|grok-[A-Za-z0-9._:-]+|claude-[A-Za-z0-9._:-]+|"
    r"gemini-[A-Za-z0-9._:-]+|o[1345](?:-[A-Za-z0-9._:-]+)?)"
    r"(?![A-Za-z0-9_./-])"
)
URL_RE = re.compile(r"https?://[^\s\"'`<>)\]}]+")
JSON_KEY_RE = re.compile(
    r"[\"']?(id|model|alias|upstream_id|canonical_model_id|canonical_slug|name)"
    r"[\"']?\s*[:=]\s*[\"'`]?$", re.I
)

KNOWN_SHORT = [
    "grok-3", "grok-3-mini",
    "claude-3-7-sonnet", "claude-3-5-haiku",
    "gpt-4o", "gpt-4o-mini",
    "o1", "o3",
    "gemini-2.5-pro", "gemini-2.5-flash",
]

MAX_FILE_BYTES = 5 * 1024 * 1024


def line(s=""):
    print(s)


def sep(ch="-", n=80):
    print(ch * n)


def category(rel):
    top = rel.split(os.sep)[0]
    if rel in BASELINE_FILES:
        return "BASELINE"
    if top == "data":
        return "GROUND_TRUTH_REF"
    return "DELIVERABLE"


def should_scan(rel, size):
    if size > MAX_FILE_BYTES:
        return False
    if rel in SKIP_EXACT_REL:
        return False
    parts = rel.split(os.sep)
    if parts[0] in SKIP_TOP_DIRS:
        return False
    base = os.path.basename(rel)
    if base.startswith(".") and base not in NAMELESS_OK:
        return False
    ext = os.path.splitext(base)[1].lower()
    if ext in BINARY_EXT:
        return False
    if ext:
        return ext in TEXT_EXT
    return base in NAMELESS_OK


def read_text(path):
    try:
        with open(path, "rb") as f:
            raw = f.read()
    except OSError:
        return None
    if b"\x00" in raw[:8192]:
        return None
    return raw.decode("utf-8", errors="replace")


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def trim(s, n=110):
    s = " ".join(s.split())
    return s if len(s) <= n else s[:n] + "..."


def collect_files():
    out = []
    for dirpath, dirnames, filenames in os.walk(ROOT):
        dirnames[:] = [d for d in dirnames if d not in SKIP_TOP_DIRS]
        for fn in filenames:
            full = os.path.join(dirpath, fn)
            rel = os.path.relpath(full, ROOT)
            try:
                size = os.path.getsize(full)
            except OSError:
                continue
            if should_scan(rel, size):
                out.append((rel, full, category(rel), size))
    out.sort(key=lambda x: x[0])
    return out


def discover_datasets(files):
    out = []
    for rel, full, cat, size in files:
        if not rel.lower().endswith(".json"):
            continue
        text = read_text(full)
        if text is None:
            continue
        try:
            doc = json.loads(text)
        except Exception:
            continue
        arr = None
        if isinstance(doc, dict) and isinstance(doc.get("data"), list):
            arr = doc["data"]
        elif isinstance(doc, list):
            arr = doc
        if arr is None or len(arr) < 10:
            continue
        if not all(isinstance(x, dict) and isinstance(x.get("id"), str)
                   for x in arr[:10]):
            continue
        out.append((rel, full, cat, size, arr))
    return out


def truth_field(m, key):
    """取真值模型中用于字段级比对的标量值。"""
    if key == "name":
        return m.get("name")
    if key == "canonical_slug":
        return m.get("canonical_slug")
    if key == "description":
        return m.get("description")
    if key == "created":
        return m.get("created")
    if key == "context_length":
        return m.get("context_length")
    if key == "pricing.prompt":
        return m.get("pricing", {}).get("prompt") if isinstance(m.get("pricing"), dict) else None
    if key == "pricing.completion":
        return m.get("pricing", {}).get("completion") if isinstance(m.get("pricing"), dict) else None
    if key == "architecture.modality":
        return m.get("architecture", {}).get("modality") if isinstance(m.get("architecture"), dict) else None
    if key == "top_provider.context_length":
        return m.get("top_provider", {}).get("context_length") if isinstance(m.get("top_provider"), dict) else None
    return None


def dataset_field_mismatches(arr, tmap):
    """逐字段比对交付副本与真值：只在两者都具备该字段时比较。"""
    CHECKS = [
        ("name", lambda x: x.get("name")),
        ("canonical_slug", lambda x: x.get("canonical_slug")),
        ("description", lambda x: x.get("description")),
        ("created", lambda x: x.get("created")),
        ("context_length", lambda x: x.get("context_length")),
        ("pricing.prompt", lambda x: x.get("pricing", {}).get("prompt")
         if isinstance(x.get("pricing"), dict) else None),
        ("pricing.completion", lambda x: x.get("pricing", {}).get("completion")
         if isinstance(x.get("pricing"), dict) else None),
        ("architecture.modality", lambda x: x.get("architecture", {}).get("modality")
         if isinstance(x.get("architecture"), dict) else None),
        ("top_provider.context_length", lambda x: x.get("top_provider", {}).get("context_length")
         if isinstance(x.get("top_provider"), dict) else None),
    ]
    mismatches = []
    checked = 0
    for x in arr:
        t = tmap.get(x.get("id")) if isinstance(x, dict) else None
        if not t:
            continue
        for key, getter in CHECKS:
            dv = getter(x)
            tv = truth_field(t, key)
            if dv is None or tv is None:
                continue  # 副本未导出该字段 → 不比较
            checked += 1
            if json.dumps(dv, sort_keys=True, ensure_ascii=False) != \
               json.dumps(tv, sort_keys=True, ensure_ascii=False):
                mismatches.append((x.get("id"), key, str(dv)[:40], str(tv)[:40]))
    return checked, mismatches


def in_model_context(rawline, s, e, provider, in_code_block, real_provider):
    before = rawline[s - 1] if s > 0 else ""
    after = rawline[e] if e < len(rawline) else ""
    if before in ("`", '"', "'") or after in ("`", '"', "'"):
        return True
    if rawline.lstrip().startswith("|"):
        return True
    if JSON_KEY_RE.search(rawline[:s].rstrip()):
        return True
    if in_code_block:
        return True
    if real_provider:
        return True
    return False


def is_model_token(provider_raw, modelpart, providers_nosquig,
                   rawline, s, e, in_code_block):
    provider = provider_raw.lstrip("~")
    p_low = provider.lower()
    m_low = modelpart.lower()
    if p_low in PATH_PREFIXES:
        return False
    if os.path.splitext(modelpart)[1].lower() in CODE_EXT_IN_MODELPART:
        return False
    if p_low in TYPE_WORDS or m_low in TYPE_WORDS:
        return False
    real_provider = provider in providers_nosquig
    if not in_model_context(rawline, s, e, provider, in_code_block, real_provider):
        return False
    if real_provider:
        return True
    if MODEL_FAMILY_RE.search(m_low) and not any(
            m_low.endswith(ext) for ext in CODE_EXT_IN_MODELPART):
        if BARE_RE.fullmatch(provider) or BARE_RE.fullmatch(modelpart):
            return False
        if provider != provider.lower():
            return False
        return True
    return False


def is_negative_fixture(token, occ, file_texts):
    """R6：所有出现位置都在负例/校验夹具语境 → information-only。"""
    for (r, ln, c, ctx) in occ:
        low = ctx.lower()
        neg = any(k in low for k in NEG_WORDS)
        probe_list = False
        ft = file_texts.get(r, "")
        if any(mk in ft.lower() for mk in PROBE_FILE_MARKERS):
            # 校验脚本里被引号包围、夹在列表/逗号间的候选
            if (("'" + token + "'") in ctx or ('"' + token + '"') in ctx) and \
               ("," in ctx or "[" in ctx or "]" in ctx):
                probe_list = True
        if not (neg or probe_list):
            return False
    return True


def main():
    now = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    sep("=")
    line(" verify/check_real_data.py · 真实模型数据合规校验（零第三方依赖 / 真实文件读取）")
    sep("=")
    line(" 运行时间       : %s" % now)
    line(" 项目根目录     : %s" % ROOT)
    line(" Python         : %s" % sys.version.split()[0])
    line(" 候选规则       : R1 形态 + R2 真实供应商前缀/模型族 + R3 模型语境 + R4 路径/扩展名/Go/CSS 排除")
    line("                 + R5 基线/参考 information-only + R6 负例/校验夹具 information-only")
    line("")

    # ---------------------------------------------------------------- [1] 真值
    sep("-")
    line(" [1] 基准真值 data/models-real.json")
    sep("-")
    if not os.path.isfile(TRUTH_PATH):
        line(" FATAL: 缺少 data/models-real.json")
        return 2
    raw = open(TRUTH_PATH, "rb").read()
    sha = hashlib.sha256(raw).hexdigest()
    try:
        doc = json.loads(raw)
        models = doc["data"]
        ids = [m["id"] for m in models]
    except Exception as e:
        line(" FATAL: 解析失败: %r" % e)
        return 2
    idset = set(ids)
    tmap = {m["id"]: m for m in models}
    providers = sorted({i.split("/")[0] for i in ids if "/" in i})
    providers_nosquig = sorted({p.lstrip("~") for p in providers})
    metadata_slugs = set()
    for m in models:
        for k in ("canonical_slug", "hugging_face_id", "id"):
            v = m.get(k)
            if isinstance(v, str) and "/" in v:
                metadata_slugs.add(v)
    line(" 文件字节数      : %d" % len(raw))
    line(" SHA-256        : %s" % sha)
    line(" 模型总数        : %d" % len(models))
    line(" 唯一 id 数      : %d" % len(idset))
    line(" 供应商数        : %d" % len(providers))
    line(" 供应商(%d)      : %s" % (len(providers), ", ".join(providers)))
    line(" 上游元数据 slug(canonical_slug/hugging_face_id/id) 去重数: %d" % len(metadata_slugs))
    line(" 与 SPEC.md C-1 声明比对:")
    line("   SHA-256 一致  : %s (SPEC=%s)" % (sha == SPEC_SHA256, SPEC_SHA256))
    line("   460 模型一致  : %s" % (len(models) == SPEC_MODELS))
    line("   69 供应商一致 : %s" % (len(providers) == SPEC_PROVIDERS))
    line("")

    # ------------------------------------------------------------- [2] 扫描范围
    files = collect_files()
    datasets = discover_datasets(files)
    dataset_rels = {d[0] for d in datasets}
    sep("-")
    line(" [2] 扫描范围与文件分类")
    sep("-")
    line(" 纳入文本文件    : %d" % len(files))
    catcount = {"DELIVERABLE": 0, "BASELINE": 0, "GROUND_TRUTH_REF": 0}
    for rel, full, cat, size in files:
        catcount[cat] = catcount.get(cat, 0) + 1
    line("   DELIVERABLE     : %d" % catcount.get("DELIVERABLE", 0))
    line("   BASELINE        : %d" % catcount.get("BASELINE", 0))
    line("   GROUND_TRUTH_REF: %d" % catcount.get("GROUND_TRUTH_REF", 0))
    line(" 识别为'模型数据集 JSON'(结构化校验 id，不做整行正则): %d" % len(datasets))
    for d in datasets:
        line("   - %s (%d 条)" % (d[0], len(d[4])))
    line(" 跳过(规则)      : data/models-real.json, data/models-real-summary.json,")
    line("                   verify/**, .git/**, node_modules/**, vendor/**, 二进制/超大文件")
    line(" 纳入文件清单:")
    for rel, full, cat, size in files:
        tag = "[DATASET]" if rel in dataset_rels else ""
        line("   [%s] %-9s %s (%d B)" % (cat[:4], tag, rel, size))
    line("")

    # -------------------------------------- [3] 派生数据 / 交付副本一致性
    sep("-")
    line(" [3] 派生数据一致性与交付数据集副本校验（逐条 id ∈ 真值集合 + 逐字段比对）")
    sep("-")
    bad_dataset_total = 0
    field_mismatch_total = 0
    if os.path.isfile(SUMMARY_JSON_PATH):
        try:
            sj = json.loads(open(SUMMARY_JSON_PATH, "rb").read().decode("utf-8", "replace"))
            sids = [x.get("id", "") for x in sj] if isinstance(sj, list) else []
            bad = [x for x in sids if x not in idset]
            line(" [GROUND_TRUTH_REF] data/models-real-summary.json : %d 条, 不在真值集合: %d %s"
                 % (len(sids), len(bad), bad[:8]))
        except Exception as e:
            line(" data/models-real-summary.json : 解析失败 %r" % e)
    else:
        line(" data/models-real-summary.json : 文件不存在")
    if os.path.isfile(SUMMARY_MD_PATH):
        txt = read_text(SUMMARY_MD_PATH) or ""
        all_bt = re.findall(
            r"`(~?[A-Za-z0-9][A-Za-z0-9._~-]*/[A-Za-z0-9][A-Za-z0-9._:+-]*)`", txt)
        md_ids = [t for t in all_bt
                  if t.lstrip("~").split("/")[0] in providers_nosquig]
        md_excluded = [t for t in all_bt if t not in md_ids]
        md_bad = [x for x in md_ids if x not in idset]
        line(" [GROUND_TRUTH_REF] data/models-real-summary.md   : 模型语境 id %d 条, 不在真值集合: %d %s"
             % (len(md_ids), len(md_bad), md_bad[:8]))
        line("     已排除的路径字面量(不计为 id): %d 个 %s"
             % (len(md_excluded), md_excluded[:8]))
    else:
        line(" data/models-real-summary.md   : 文件不存在")
    for rel, full, cat, size, arr in datasets:
        dsi = [x.get("id", "") for x in arr if isinstance(x, dict)]
        dset = set(dsi)
        not_in = [x for x in dsi if x not in idset]
        missing = [x for x in ids if x not in dset]
        fsha = sha256_file(full)
        same = (fsha == sha)
        bad_dataset_total += len(not_in)
        checked, mismatches = dataset_field_mismatches(arr, tmap)
        field_mismatch_total += len(mismatches)
        line(" [%s] %s" % (cat, rel))
        line("     记录数=%d  不在真值集合的 id=%d %s  真值缺失=%d %s"
             % (len(dsi), len(not_in), not_in[:6], len(missing), missing[:6]))
        line("     SHA-256=%s  与真值逐字节一致=%s" % (fsha, same))
        line("     逐字段比对(name/canonical_slug/description/created/context_length/"
             "pricing.prompt/pricing.completion/architecture.modality/top_provider.context_length):")
        line("       可比字段值次数=%d  与真值不一致=%d %s"
             % (checked, len(mismatches), mismatches[:4]))
    line(" 数据集副本中'不在真值集合的 id'合计: %d（应为 0）" % bad_dataset_total)
    line(" 数据集副本中'字段值与真值不一致'合计: %d（应为 0）" % field_mismatch_total)
    line("")

    # --------------------------------------------------- [4] 候选抽取与比对
    slash_hits = {}
    metadata_hits = {}
    excluded_path_tokens = 0
    bare_hits = {}
    scanned_lines = 0
    file_texts = {}

    for rel, full, cat, size in files:
        if rel in dataset_rels:
            continue
        text = read_text(full)
        if text is None:
            continue
        file_texts[rel] = text
        in_code_block = False
        for lineno, rawline in enumerate(text.splitlines(), 1):
            scanned_lines += 1
            stripped = rawline.lstrip()
            if stripped.startswith("```") or stripped.startswith("~~~"):
                in_code_block = not in_code_block
            masked = URL_RE.sub(lambda m: " " * len(m.group(0)), rawline)
            for m in SLASH_RE.finditer(masked):
                token = m.group(0)
                provider = m.group(1).lstrip("~")
                modelpart = m.group(2)
                if not is_model_token(provider, modelpart, providers_nosquig,
                                      masked, m.start(), m.end(), in_code_block):
                    excluded_path_tokens += 1
                    continue
                if token in metadata_slugs and token not in idset:
                    metadata_hits.setdefault(token, []).append(
                        (rel, lineno, cat, trim(rawline)))
                else:
                    slash_hits.setdefault(token, []).append(
                        (rel, lineno, cat, trim(rawline)))
            for m in BARE_RE.finditer(masked):
                token = m.group(1).rstrip(".-")
                if not token:
                    continue
                if not any(c.isdigit() for c in token) and token not in KNOWN_SHORT:
                    continue
                before = masked[m.start() - 1] if m.start() > 0 else ""
                after = masked[m.end()] if m.end() < len(masked) else ""
                quoted = before in ("`", '"', "'") or after in ("`", '"', "'")
                table = masked.lstrip().startswith("|")
                if not (quoted or table or in_code_block):
                    continue
                bare_hits.setdefault(token, []).append(
                    (rel, lineno, cat, trim(rawline)))

    for name in KNOWN_SHORT:
        if name in bare_hits:
            continue
        pat = re.compile(r"(?<![A-Za-z0-9_/.:-])" + re.escape(name) +
                         r"(?![A-Za-z0-9_./-])")
        for rel, full, cat, size in files:
            if rel in dataset_rels:
                continue
            text = file_texts.get(rel)
            if text is None:
                text = read_text(full)
            if text is None:
                continue
            for lineno, rawline in enumerate(text.splitlines(), 1):
                mm = pat.search(rawline)
                if not mm:
                    continue
                before = rawline[mm.start() - 1] if mm.start() > 0 else ""
                after = rawline[mm.end()] if mm.end() < len(rawline) else ""
                if not (before in ("`", '"', "'") or after in ("`", '"', "'")
                        or rawline.lstrip().startswith("|")):
                    continue
                bare_hits.setdefault(name, []).append(
                    (rel, lineno, cat, trim(rawline)))

    # 分类：DELIVERABLE 内、非真值、且非"负例/校验夹具"的 token 才是致命伪造
    real_tokens = {}
    fabricated_tokens = {}
    negative_fixture_tokens = {}
    baseline_only_tokens = {}
    for tok, occ in slash_hits.items():
        if tok in idset:
            real_tokens[tok] = occ
            continue
        cats = {o[2] for o in occ}
        if "DELIVERABLE" not in cats:
            baseline_only_tokens[tok] = occ
        elif is_negative_fixture(tok, occ, file_texts):
            negative_fixture_tokens[tok] = occ
        else:
            fabricated_tokens[tok] = occ

    sep("-")
    line(" [4] 候选抽取与比对（严格模型语境口径）")
    sep("-")
    line(" 正则扫描文本行数            : %d" % scanned_lines)
    line(" 模型语境候选种类(去重)       : %d" % (len(slash_hits) + len(metadata_hits)))
    line("   ├ 真实命中(∈真值集合)      : %d" % len(real_tokens))
    line("   ├ 上游元数据 slug(非模型 id, 合法): %d" % len(metadata_hits))
    line("   ├ 可疑编造项(真正伪造, 交付物, 非夹具): %d" % len(fabricated_tokens))
    line("   ├ 负例/校验夹具 token(information-only): %d" % len(negative_fixture_tokens))
    line("   └ 基线/参考文件 token(information-only): %d" % len(baseline_only_tokens))
    line(" R4 排除的路径/扩展名/Go/CSS/非语境 token 次数: %d" % excluded_path_tokens)
    line(" 裸短名/别名候选(去重)        : %d" % len(bare_hits))
    line("")

    # ------------------------------------------------------------ [5] 真实命中
    sep("-")
    line(" [5] 真实命中（候选 id ∈ data/models-real.json）")
    sep("-")
    if real_tokens:
        for tok in sorted(real_tokens):
            occ = real_tokens[tok]
            line(" REAL  %-46s ×%-3d 首处 %s:%d"
                 % (tok, len(occ), occ[0][0], occ[0][1]))
    else:
        line(" (无：非数据集文本中尚未出现真实 slashed 模型 id)")
    line(" 真实命中种类: %d" % len(real_tokens))
    line("")

    # -------------------------------------------------- [5b] 元数据 slug
    sep("-")
    line(" [5b] 上游元数据 slug（canonical_slug / hugging_face_id，非模型 id，不计入编造）")
    sep("-")
    if metadata_hits:
        line(" 共 %d 种，示例（前 12 种）:" % len(metadata_hits))
        for tok in sorted(metadata_hits)[:12]:
            line("   METADATA %-48s ×%d 首处 %s:%d"
                 % (tok, len(metadata_hits[tok]),
                    metadata_hits[tok][0][0], metadata_hits[tok][0][1]))
    else:
        line(" (无)")
    line("")

    # ---------------------------------------------------------- [6] 可疑编造项
    sep("-")
    line(" [6] 可疑编造项（DELIVERABLE 内、模型语境下、∉真值 id 集合、且非校验夹具的伪造模型名）")
    sep("-")
    if not fabricated_tokens:
        line(" (无 —— 可疑编造项 = 0)")
    else:
        for tok in sorted(fabricated_tokens):
            occ = fabricated_tokens[tok]
            line(" [FABRICATED] %s  ×%d" % (tok, len(occ)))
            for (r, ln, c, ctx) in occ[:5]:
                line("        %s:%d [%s] | %s" % (r, ln, c, ctx))
            if len(occ) > 5:
                line("        ... 其余 %d 处" % (len(occ) - 5))
    line(" 可疑编造项合计: %d 种（交付物内真正伪造的模型名）" % len(fabricated_tokens))
    line("")

    # ------------------------------------------- [6b] information-only
    sep("-")
    line(" [6b] information-only token（不计入可疑；逐条给 文件:行号 证据）")
    sep("-")
    line(" -- R6 负例/校验夹具（文档明确断言该 id 不存在，或为校验脚本候选列表）--")
    if not negative_fixture_tokens:
        line("   (无)")
    else:
        for tok in sorted(negative_fixture_tokens):
            occ = negative_fixture_tokens[tok]
            line("   NEG_FIXTURE %-40s ×%d" % (tok, len(occ)))
            for (r, ln, c, ctx) in occ[:5]:
                line("        %s:%d [%s] | %s" % (r, ln, c, ctx))
    line(" -- R5 基线/参考文件 token --")
    if not baseline_only_tokens:
        line("   (无)")
    else:
        for tok in sorted(baseline_only_tokens)[:20]:
            occ = baseline_only_tokens[tok]
            line("   BASELINE_REF %-38s ×%-3d 首处 %s:%d"
                 % (tok, len(occ), occ[0][0], occ[0][1]))
    line("")

    # ------------------------------------------------- [7] 已知短名/别名核查
    sep("-")
    line(" [7] 已知短名/别名核查（形如 gpt-4o / grok-3 / claude-3-7-sonnet 的无斜杠短名）")
    sep("-")
    line(" 说明：短名是别名而非模型 id；本表用于判断是否存在「零对应」（真值 460 模型中")
    line("       完全找不到对应 id）——若被当作真实静态模型写入交付物即违反 C-1。")
    line(" %-22s %-9s %-16s %-11s %s" %
         ("short", "exact_id", "same_model_part", "substring", "verdict"))
    zero_counterpart = []
    for name in sorted(set(list(bare_hits.keys()) + KNOWN_SHORT)):
        exact = name in idset
        same = [i for i in ids if "/" in i and i.split("/")[-1] == name]
        sub = [i for i in ids if name in i]
        if exact:
            verdict = "REAL_ID"
        elif same:
            verdict = "ALIAS_HAS_REAL_COUNTERPART (e.g. %s)" % same[0]
        elif sub:
            verdict = "PARTIAL_SUBSTRING_ONLY (e.g. %s)" % sub[0]
        else:
            verdict = "NOT_IN_DATASET [零对应]"
            zero_counterpart.append(name)
        line(" %-22s %-9s %-16d %-11d %s" %
             (name, str(exact), len(same), len(sub), verdict))
    line("")
    line(" 零对应短名（真值 460 模型中完全找不到对应 id）: %d 个 -> %s"
         % (len(zero_counterpart), ", ".join(zero_counterpart)))
    line(" 短名出现位置（仅列 DELIVERABLE 类别，最多每种 3 处，含上下文供人工判定是否被当作真实模型）:")
    any_deliv_bare = False
    for name in sorted(bare_hits):
        occ = [o for o in bare_hits[name] if o[2] == "DELIVERABLE"]
        if not occ:
            continue
        any_deliv_bare = True
        line("   %s ×%d (DELIVERABLE)" % (name, len(occ)))
        for (r, ln, c, ctx) in occ[:3]:
            line("       %s:%d | %s" % (r, ln, ctx))
    if not any_deliv_bare:
        line("   (无：交付文件中尚未在模型语境出现裸短名)")
    line("")

    # --------------------------------------------------------------- [8] 结论
    sep("-")
    line(" [8] 结论")
    sep("-")
    line(" 真值数据         : %d 真实模型 / %d 供应商 / SHA-256 %s" %
         (len(models), len(providers), sha))
    line("                   与 SPEC.md C-1 声明一致: %s" %
         (sha == SPEC_SHA256 and len(models) == SPEC_MODELS and
          len(providers) == SPEC_PROVIDERS))
    line(" 数据集副本       : %d 个；'不在真值集合的 id'合计 %d（应为 0）；"
         "字段值不一致合计 %d（应为 0）"
         % (len(datasets), bad_dataset_total, field_mismatch_total))
    line(" 可疑编造项       : %d 种（交付物内真正伪造的模型名，逐条证据见 [6]）"
         % len(fabricated_tokens))
    line(" information-only : 负例/夹具 %d 种 + 基线/参考 %d 种，逐条证据见 [6b]"
         % (len(negative_fixture_tokens), len(baseline_only_tokens)))
    line(" 零对应短名       : %d 个（别名核查，非 slashed 模型 id；见 [7]）"
         % len(zero_counterpart))
    line(" 摘要数据一致性   : 见 [3] 节")
    line("")
    line(" SUMMARY models=%d providers=%d sha256=%s dataset_copies=%d "
         "dataset_fabricated_ids=%d dataset_field_mismatches=%d real_hit_kinds=%d "
         "suspicious=%d fabricated_in_deliverables=%d negative_fixtures=%d "
         "baseline_info_tokens=%d short_names_zero_counterpart=%d short_names=%s" %
         (len(models), len(providers), sha, len(datasets), bad_dataset_total,
          field_mismatch_total, len(real_tokens), len(fabricated_tokens),
          len(fabricated_tokens), len(negative_fixture_tokens),
          len(baseline_only_tokens), len(zero_counterpart),
          ",".join(zero_counterpart)))
    line(" 退出码           : 0（扫描完成；致命/合规判定见 docs/08-功能核实报告.md）")
    sep("=")
    return 0


if __name__ == "__main__":
    sys.exit(main())
