#!/usr/bin/env bash
# =============================================================================
# verify/full_acceptance.sh —— 天衡一键验收
# 顺序执行全部构建/测试/真实性/端到端检查，输出 PASS/FAIL 汇总表与总退出码。
# 所有结果均为真实执行，不预置期望值。
# =============================================================================
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

LOG_DIR="${LOG_DIR:-/tmp/tianheng-acceptance-$$}"
mkdir -p "$LOG_DIR"

PASS=0
FAIL=0
SKIP=0
declare -a ROWS

run() {
  local name="$1"; shift
  local log="$LOG_DIR/$(echo "$name" | tr ' /' '__').log"
  echo ""
  echo "================================================================"
  echo "▶ $name"
  echo "  cmd: $*"
  echo "================================================================"
  if "$@" >"$log" 2>&1; then
    tail -n 6 "$log" | sed 's/^/  /'
    PASS=$((PASS + 1))
    ROWS+=("PASS|$name|$(tail -n 1 "$log" | cut -c1-90)")
  else
    local code=$?
    echo "  --- FAILED (exit=$code) ---"
    tail -n 25 "$log" | sed 's/^/  /'
    FAIL=$((FAIL + 1))
    ROWS+=("FAIL|$name|exit=$code $(tail -n 1 "$log" | cut -c1-70)")
  fi
}

skip() {
  local name="$1" why="$2"
  echo ""
  echo "▶ $name —— SKIP: $why"
  SKIP=$((SKIP + 1))
  ROWS+=("SKIP|$name|$why")
}

echo "###############################################################"
echo "# 天衡（Tianheng）一键验收"
echo "# ROOT      : $ROOT"
echo "# LOG_DIR   : $LOG_DIR"
echo "# go        : $(go version 2>&1)"
echo "# node      : $(node --version 2>&1)"
echo "# python3   : $(python3 --version 2>&1)"
echo "# TIMESTAMP : $(date '+%Y-%m-%d %H:%M:%S %z')"
echo "###############################################################"

# ---------- 1. Go 构建与静态检查 ----------
run "go build ./..." go build ./...
run "go vet ./..." go vet ./...

# ---------- 2. Go 测试 ----------
run "go test ./... -count=1" go test ./... -count=1

# ---------- 3. 真实数据合规 ----------
run "check_real_data.py" python3 verify/check_real_data.py

# ---------- 4. 前端类型与构建 ----------
if [ -d web/node_modules ]; then
  run "web tsc --noEmit" bash -c 'cd web && ./node_modules/.bin/tsc --noEmit -p tsconfig.json'
  # 关键：Next 的 rewrites 在 build 时烘焙进 routes-manifest，缺这个变量会导致
  # /api/* 与 /v1/* 完全没有代理（前端返回自己的 404）。必须构建期传入。
  run "web npm run build" bash -c 'cd web && LLM_HUB_BACKEND_ORIGIN=${LLM_HUB_BACKEND_ORIGIN:-http://127.0.0.1:18700} npm run build'
  # 关键：npm run build 替换了 .next，而运行中的 next-server 仍持有旧 build
  # manifest → 页面会引用已删除的旧哈希资源（全站无样式）。因此构建后必须
  # 用安全重启脚本把「构建产物」与「运行进程」重新配对。
  if [ "${WEB_AUTORESTART:-1}" = "1" ]; then
    run "web 安全重启（构建后重新配对）" bash deploy/web-restart.sh
  fi
else
  skip "web tsc --noEmit" "web/node_modules 不存在（先 cd web && npm ci）"
  skip "web npm run build" "web/node_modules 不存在"
fi

# ---------- 5. 端到端 ----------
# 前端资源一致性守卫（防「build 覆盖运行中进程」导致全站无样式）
# 注：WEB_AUTORESTART=0 可跳过自动重启（用于只想看构建结果的场景）
if curl -sf -o /dev/null http://127.0.0.1:13000/ 2>/dev/null; then
  run "web_assets_check.sh" bash verify/web_assets_check.sh
else
  skip "web_assets_check.sh" "前端未在 13000 运行（bash deploy/web-restart.sh 启动）"
fi

run "e2e.sh" bash verify/e2e.sh
run "rankings_firewall_e2e.sh" bash verify/rankings_firewall_e2e.sh

# ---------- 6. 交付物完整性 ----------
echo ""
echo "================================================================"
echo "▶ 交付物完整性检查（test -s）"
echo "================================================================"
REQUIRED=(
  SECURITY.md .env.example
  migrations/0001_init.up.sql migrations/0001_init.down.sql
  web/Dockerfile deploy/Dockerfile deploy/docker-compose.yml deploy/nginx.conf
  README.md SPEC.md LLM-API-Aggregator-Prompt.md
  docs/01-方案初稿.md docs/02-方案完善版.md docs/03-方案审核报告.md
  docs/04-UI设计规范.md docs/05-内核设计.md docs/06-代码审核与测试.md
  docs/07-漏洞审查与修复.md docs/08-功能核实报告.md docs/09-接口总览.md
  docs/10-运维手册.md docs/11-社区论坛.md docs/12-头像上传.md
  docs/13-实时监控.md docs/14-数据库加固.md docs/15-自动发现.md
  docs/16-安全审计与靶场.md docs/17-天衡改造总览.md docs/18-第三轮安全复审.md
  docs/19-前端资源失配事故复盘.md docs/20-Magpie原理原生实现.md
  data/models-real.json web/data/models.json
  src/cmd/server/main.go kernel/contract.go
  src/internal/affinity/affinity.go src/internal/grouping/grouping.go
  src/internal/redact/redact.go src/internal/ctxbreakdown/ctx.go
  src/internal/routingjournal/journal.go src/internal/fsutil/atomic.go
  verify/e2e.sh verify/full_acceptance.sh verify/check_real_data.py
  verify/rankings_firewall_e2e.sh deploy/target-range.sh
)
MISSING=0
for f in "${REQUIRED[@]}"; do
  if [ -s "$f" ]; then
    printf '  ✅ %-44s %8s B\n' "$f" "$(wc -c < "$f" | tr -d ' ')"
  else
    printf '  ❌ %-44s MISSING/EMPTY\n' "$f"
    MISSING=$((MISSING + 1))
  fi
done
if [ "$MISSING" -eq 0 ]; then
  PASS=$((PASS + 1)); ROWS+=("PASS|交付物完整性|${#REQUIRED[@]} 项全部非空")
else
  FAIL=$((FAIL + 1)); ROWS+=("FAIL|交付物完整性|$MISSING 项缺失")
fi

# ---------- 7. 真实数据不变量 ----------
echo ""
echo "================================================================"
echo "▶ 真实数据不变量（460 模型 / 69 供应商 / SHA-256）"
echo "================================================================"
if python3 - "$ROOT" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
b = (root / "data/models-real.json").read_bytes()
d = json.loads(b)["data"]
sha = hashlib.sha256(b).hexdigest()
ids = {m["id"] for m in d}
provs = {i.split("/")[0] for i in ids}
web = json.loads((root / "web/data/models.json").read_text())
web_ids = {r["id"] for r in (web["data"] if isinstance(web, dict) else web)}
print(f"  models={len(d)} providers={len(provs)} sha256={sha}")
print(f"  web_export_ids={len(web_ids)} not_in_truth={len(web_ids - ids)}")
assert len(d) == 460, f"模型数 {len(d)} != 460"
assert len(provs) == 69, f"供应商数 {len(provs)} != 69"
assert sha == "08b4635fd684689feda841f906d59e0dcd8ad9dd57a7c0b7f0667d46b740876a", "SHA-256 变化"
assert not (web_ids - ids), f"web 导出含非真值 id: {web_ids - ids}"
print("  INVARIANTS_OK")
PY
then
  PASS=$((PASS + 1)); ROWS+=("PASS|真实数据不变量|460/69/SHA 一致")
else
  FAIL=$((FAIL + 1)); ROWS+=("FAIL|真实数据不变量|见上方输出")
fi

# ---------- 汇总 ----------
echo ""
echo "###############################################################"
echo "# 验收汇总"
echo "###############################################################"
printf '%-6s %-34s %s\n' "结果" "检查项" "摘要"
printf '%-6s %-34s %s\n' "-----" "----------------------------------" "----------------------"
for r in "${ROWS[@]}"; do
  IFS='|' read -r st nm sm <<< "$r"
  printf '%-6s %-34s %s\n' "$st" "$nm" "$sm"
done
echo "---------------------------------------------------------------"
echo " PASS=$PASS  FAIL=$FAIL  SKIP=$SKIP"
echo " 日志目录: $LOG_DIR"
echo "###############################################################"

if [ "$FAIL" -gt 0 ]; then
  echo "RESULT: FAIL"
  exit 1
fi
echo "RESULT: ALL_PASS"
exit 0
