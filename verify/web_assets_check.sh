#!/usr/bin/env bash
# =============================================================================
# verify/web_assets_check.sh —— 前端静态资源一致性守卫
#
# 解决的问题（真实事故）：`npm run build` 在运行中的 next-server 之上替换了
# .next 目录后，旧进程仍按内存里的旧 build manifest 输出 HTML，引用已被删除的
# 旧哈希资源（如 /_next/static/css/<old-hash>.css），导致 CSS 全部取不到、
# 页面退化成裸 HTML —— 外观「变了个样子」，但源码与设计其实没动。
#
# 本脚本对「HTML 实际引用的每个 /_next/static 资源」同时做磁盘与 HTTP 双重校验，
# 并在 BUILD_ID 与运行进程启动时间不一致时直接报错。
#
# 用法：
#   bash verify/web_assets_check.sh                 # 用默认端口 13000
#   WEB_PORT=3000 bash verify/web_assets_check.sh
# 退出码：0 = 一致可用；1 = 存在失配（页面会渲染异常）
# =============================================================================
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

WEB_PORT="${WEB_PORT:-13000}"
BASE="http://127.0.0.1:${WEB_PORT}"
PAGES="${PAGES:-/ /models /rankings /community /profile /monitor}"

echo "======================================================================"
echo " 前端静态资源一致性守卫"
echo " ROOT=$ROOT  BASE=$BASE  PAGES=$PAGES"
echo "======================================================================"

if [ ! -f web/.next/BUILD_ID ]; then
  echo " ❌ web/.next/BUILD_ID 不存在：前端尚未构建（cd web && npm run build）"
  exit 1
fi
BUILD_ID="$(cat web/.next/BUILD_ID)"
BUILD_TIME="$(stat -f '%Sm' -t '%Y-%m-%d %H:%M:%S' web/.next/BUILD_ID 2>/dev/null || stat -c '%y' web/.next/BUILD_ID)"
echo " BUILD_ID   : $BUILD_ID"
echo " 构建时间   : $BUILD_TIME"

# ---------- 运行进程与构建时间是否错位 ----------
SERVER_PID="$(pgrep -f 'next-server' | head -1 || true)"
if [ -n "${SERVER_PID:-}" ]; then
  START_EPOCH="$(ps -o lstart= -p "$SERVER_PID" | xargs -I{} date -j -f '%a %b %d %T %Y' '{}' '+%s' 2>/dev/null || echo 0)"
  BUILD_EPOCH="$(stat -f '%m' web/.next/BUILD_ID 2>/dev/null || echo 0)"
  if [ "$START_EPOCH" != "0" ] && [ "$BUILD_EPOCH" != "0" ] && [ "$START_EPOCH" -lt "$BUILD_EPOCH" ]; then
    echo " 运行 PID   : ${SERVER_PID}（早于构建 → 失配）"
    echo " ❌ 进程启动时间早于 .next 构建时间 —— next-server 持有旧 build manifest，"
    echo "    页面会引用已删除的旧哈希资源（典型症状：全站无样式）。"
    echo "    修复：pkill -f next-server && cd web && LLM_HUB_BACKEND_ORIGIN=<后端> npm start -- -p $WEB_PORT"
    exit 1
  else
    echo " 运行 PID   : ${SERVER_PID}（与构建一致）"
  fi
else
  echo " ⚠ 未发现 next-server 进程（仅做磁盘校验）"
fi

# ---------- 逐页校验静态资源 ----------
python3 - "$BASE" "$PAGES" "$ROOT" <<'PY'
import re, sys, pathlib, urllib.request, urllib.error

base, pages, root = sys.argv[1], sys.argv[2].split(), pathlib.Path(sys.argv[3])
disk = root / "web/.next/static"

def fetch(url, timeout=10):
    try:
        with urllib.request.urlopen(url, timeout=timeout) as r:
            return r.status, r.read()
    except urllib.error.HTTPError as e:
        return e.code, b""
    except Exception:
        return 0, b""

total_refs = total_ok = 0
failures = []

for page in pages:
    code, body = fetch(base + page)
    if code != 200:
        failures.append(f"{page}: 页面本身 HTTP {code}")
        continue
    html = body.decode("utf-8", "replace")
    refs = sorted(set(re.findall(r'/_next/static/[A-Za-z0-9/_.\-]+\.(?:css|js)', html)))
    if not refs:
        failures.append(f"{page}: HTML 未引用任何 /_next/static 资源（构建异常？）")
        continue
    page_bad = []
    for r in refs:
        total_refs += 1
        on_disk = (disk / r.replace("/_next/static/", "")).exists()
        hcode, hbody = fetch(base + r)
        if on_disk and hcode == 200 and len(hbody) > 0:
            total_ok += 1
        else:
            page_bad.append(f"{r} (disk={on_disk} http={hcode} bytes={len(hbody)})")
            failures.append(f"{page}: {r} (disk={on_disk} http={hcode} bytes={len(hbody)})")
    mark = "✅" if not page_bad else "❌"
    print(f" {mark} {page:<12} 资源 {len(refs)} 个，失败 {len(page_bad)} 个")

print()
print(f" 合计：引用 {total_refs} 个静态资源，可用 {total_ok} 个，失败 {total_refs - total_ok} 个")
if failures:
    print()
    print(" 失败明细：")
    for f in failures:
        print("   ❌", f)
    print()
    print(" RESULT: STALE_OR_MISSING_ASSETS")
    sys.exit(1)
print(" RESULT: ALL_ASSETS_OK")
PY
