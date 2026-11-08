#!/usr/bin/env bash
# =============================================================================
# deploy/web-restart.sh —— 前端安全重启（原子：先停旧进程，再按当前构建启动）
#
# 为什么需要它：next-server 在启动时把 build manifest 读进内存。若在运行中的
# 进程之上直接 `npm run build` 替换 .next，旧进程会继续输出引用旧哈希资源的
# HTML，而这些文件已被删除 → 全站 CSS/JS 404 → 页面退化成裸 HTML（"样子变了"）。
# 本脚本保证「构建产物」与「运行进程」始终配对，并在启动后立刻校验资源一致性。
#
# 用法：
#   bash deploy/web-restart.sh                 # 端口 13000，后端 18700
#   WEB_PORT=3000 BACKEND=http://127.0.0.1:8080 bash deploy/web-restart.sh
#   REBUILD=1 bash deploy/web-restart.sh       # 先重新构建再启动
# =============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

WEB_PORT="${WEB_PORT:-13000}"
BACKEND="${BACKEND:-http://127.0.0.1:18700}"
LOG="${LOG:-/tmp/tianheng-web-${WEB_PORT}.log}"

echo "== 天衡前端安全重启 =="
echo " ROOT=$ROOT  PORT=$WEB_PORT  BACKEND=$BACKEND"

# 1) 停掉旧进程（避免新旧 manifest 混用）
if pgrep -f 'next-server' >/dev/null 2>&1; then
  echo " 停止旧 next-server: $(pgrep -f 'next-server' | tr '\n' ' ')"
  pkill -f 'next-server' || true
  pkill -f 'next start' || true
  for _ in $(seq 1 20); do pgrep -f 'next-server' >/dev/null 2>&1 || break; sleep 0.3; done
fi

# 2) 可选重新构建（rewrites 依赖 LLM_HUB_BACKEND_ORIGIN，必须构建期传入）
if [ "${REBUILD:-0}" = "1" ] || [ ! -f web/.next/BUILD_ID ]; then
  echo " 构建（LLM_HUB_BACKEND_ORIGIN=${BACKEND}）..."
  ( cd web && LLM_HUB_BACKEND_ORIGIN="$BACKEND" npm run build >"$LOG.build" 2>&1 ) \
    || { echo " ❌ 构建失败，见 $LOG.build"; tail -20 "$LOG.build"; exit 1; }
  echo " 构建完成 BUILD_ID=$(cat web/.next/BUILD_ID)"
fi

# 3) 启动（必须在构建之后，保证 manifest 与磁盘一致）
echo " 启动 next start -p $WEB_PORT ..."
( cd web && LLM_HUB_BACKEND_ORIGIN="$BACKEND" nohup npm start -- -p "$WEB_PORT" >"$LOG" 2>&1 & )
for _ in $(seq 1 40); do curl -sf -o /dev/null "http://127.0.0.1:${WEB_PORT}/" 2>/dev/null && break; sleep 0.5; done

# 4) 立刻校验资源一致性
echo " 校验静态资源一致性..."
WEB_PORT="$WEB_PORT" bash verify/web_assets_check.sh | tail -12

echo "== 完成：http://127.0.0.1:${WEB_PORT}  BUILD_ID=$(cat web/.next/BUILD_ID) =="
