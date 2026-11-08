#!/usr/bin/env bash
# =============================================================================
# deploy/target-range.sh — 天衡（Tianheng）本机授权靶场
# 用途：在本机 127.0.0.1 起一套隔离的天衡服务，供 dsh-plugin-offsec
#       做端口/目录/注入/认证/上传/SSRF/SSE/越权攻击测试。
# 隔离：独立端口、独立 DATA_DIR、独立二进制与日志；不触碰生产状态。
# 用法：
#   PORT=18400 bash deploy/target-range.sh start
#   bash deploy/target-range.sh status
#   bash deploy/target-range.sh stop
#   PORT=18400 bash deploy/target-range.sh url
# =============================================================================
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

PORT="${PORT:-18400}"
DATA_DIR="${TARGET_DATA_DIR:-/tmp/tianheng-target-range}"
BIN="$DATA_DIR/tianheng-server"
PID_FILE="$DATA_DIR/target.pid"
LOG="$DATA_DIR/target.log"
MK="$(python3 -c 'import base64;print(base64.b64encode(b"0123456789abcdef0123456789abcdef").decode())')"
BASE="http://127.0.0.1:${PORT}"

mkdir -p "$DATA_DIR" || exit 1
chmod 700 "$DATA_DIR"

case "${1:-start}" in
  url)
    echo "$BASE"
    ;;
  status)
    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
      echo "TARGET_UP=True pid=$(cat "$PID_FILE") base=$BASE"
      curl -s -o /dev/null -w 'HEALTHZ_HTTP=%{http_code}\n' --max-time 3 "$BASE/healthz"
    else
      echo "TARGET_UP=False base=$BASE"
    fi
    ;;
  stop)
    if [ -f "$PID_FILE" ]; then
      P="$(cat "$PID_FILE")"
      if kill -0 "$P" 2>/dev/null; then kill "$P" 2>/dev/null; echo "STOPPED pid=$P"; fi
      rm -f "$PID_FILE"
    else
      echo "NO_PID_FILE"
    fi
    ;;
  start)
    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
      echo "TARGET_ALREADY_UP pid=$(cat "$PID_FILE") base=$BASE"
      exit 0
    fi
    echo "BUILDING tianheng-server ..."
    go build -o "$BIN" ./src/cmd/server || { echo "BUILD_FAILED"; exit 1; }
    chmod 700 "$BIN"

    SERVER_PORT="$PORT" \
    SERVER_BASE_URL="$BASE" \
    JWT_SECRET="fbygqkr-vlplp" \
    MASTER_KEY="$MK" \
    ADMIN_EMAIL="{{EMAIL_dsw7w796}}" \
    ADMIN_PASSWORD='{{SECRET_j9t6tuyg}}' \
    REGISTRATION_OPEN="true" \
    RATE_RPM_DEFAULT="0" \
    RATE_TPM_DEFAULT="0" \
    PROBE_ENABLED="false" \
    DATA_DIR="$DATA_DIR/state" \
    FIREWALL_BLOCK_SCORE="6" \
    FIREWALL_LOCKDOWN_IP_THRESHOLD="3" \
      "$BIN" > "$LOG" 2>&1 &
    P=$!
    echo "$P" > "$PID_FILE"

    i=0
    UP=0
    while [ "$i" -lt 60 ]; do
      code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$BASE/healthz" 2>/dev/null)"
      if [ "$code" = "200" ]; then UP=1; break; fi
      i=$((i + 1)); sleep 0.25
    done
    echo "TARGET_UP=$([ "$UP" -eq 1 ] && echo True || echo False)"
    echo "BASE=$BASE"
    echo "PID=$P"
    echo "DATA_DIR=$DATA_DIR/state"
    echo "LOG=$LOG"
    echo "EVIDENCE_DIR=$DATA_DIR/evidence"
    mkdir -p "$DATA_DIR/evidence"
    if [ "$UP" -ne 1 ]; then
      echo "--- server log ---"; tail -n 20 "$LOG"; exit 1
    fi
    echo "--- healthz ---"
    curl -s --max-time 3 "$BASE/healthz"; echo
    echo "--- real models ---"
    curl -s --max-time 5 "$BASE/v1/models" | python3 -c 'import sys,json;d=json.load(sys.stdin);print("REAL_MODELS=",len(d.get("data",[])),"SOURCE=",d.get("data",[{}])[0].get("source",""))' 2>/dev/null || echo "MODELS_UNAVAILABLE"
    ;;
  *)
    echo "usage: $0 {start|stop|status|url}"; exit 1
    ;;
esac
