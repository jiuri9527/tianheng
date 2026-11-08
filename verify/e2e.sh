#!/usr/bin/env bash
# =============================================================================
# verify/e2e.sh —— llm-hub 端到端真实执行核实（团队 8 / 功能审查核实）
# =============================================================================
# 零第三方依赖：仅 bash + curl + python3(标准库) + go(标准库工具链)。
# macOS bash 3.2 兼容（不使用关联数组 / mapfile / ${var,,}）。
#
# 真实做的事：
#   1) go build 出服务端二进制并后台启动（SERVER_PORT 可配，默认 18080）；
#   2) 对每条 curl 记录真实 HTTP 状态码与响应片段，保存到 evidence 目录；
#      · GET  /healthz
#      · GET  /metrics
#      · GET  /v1/models            （解析 id 并逐一比对 data/models-real.json）
#      · POST /v1/chat/completions  （无上游凭据 → 统一 upstream_error，且无伪造输出）
#      · POST /v1/messages          （Anthropic 入口，同上）
#      · /api/* 无效令牌             （401）
#      · RPM 限流                    （429，使用独立 rpm=2 令牌）
#      · SSRF 防护                   （内网 base_uri 被拒）
#   3) 结束时 kill 进程、删除临时二进制、打印 evidence 目录路径。
#
# 测试设计要点（按 Lead 复核意见修正）：
#   - 主令牌 rpm=1000 供 S4/S5/S6，避免被限流提前截断而够不到 upstream_error 分支；
#   - 另建 rpm=2 的专用令牌供 S7 连续 8 次请求观察 429，两者隔离；
#   - SSRF 请求体同时发 snake_case 与 PascalCase 两套键名，兼容 channel.Input
#     加 json 标签前后的任一实现，保证请求真实到达 SSRF 校验器；
#   - 每条 curl 预先落空 body 文件，连接失败时得到 BODY_MISSING 事实而非栈回溯。
#
# 失败即如实记录：编译失败 / 服务未起 / 连接被拒时，curl 的真实状态码(000)
# 与 stderr 原样打印，绝不伪造"通过"。
# =============================================================================
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

PORT="${PORT:-18080}"
BASE="http://127.0.0.1:${PORT}"
BIN="/tmp/llm-hub-e2e-server-$$"
LOG="/tmp/llm-hub-e2e-server-$$.log"
EVID="$(mktemp -d /tmp/llm-hub-e2e-evidence.XXXXXX)"
MASTER_KEY="$(python3 -c 'import base64;print(base64.b64encode(b"0123456789abcdef0123456789abcdef").decode())')"

SERVER_PID=""
SERVER_UP=0
BUILD_OK=0
N=0
REQ_LAST_CODE=""
REQ_LAST_BODY=""
V1AUTH="Bearer invalid-sk-000"
RPM_AUTH="Bearer invalid-sk-000"
JWT=""
SK=""
SK_RPM=""

cleanup() {
  echo ""
  echo "--------------------------------------------------------------------"
  echo " [CLEANUP] 清理真实启动的进程与临时二进制"
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null
    wait "$SERVER_PID" 2>/dev/null
    echo "  killed SERVER_PID=$SERVER_PID"
  else
    echo "  no live server process (SERVER_PID='${SERVER_PID}')"
  fi
  rm -f "$BIN" 2>/dev/null
  echo "  removed BIN=$BIN"
  echo "  evidence dir kept: $EVID"
  echo "--------------------------------------------------------------------"
}
trap cleanup EXIT INT TERM

echo "===================================================================="
echo " verify/e2e.sh · 端到端真实执行核实"
echo "===================================================================="
echo " ROOT        : $ROOT"
echo " BASE        : $BASE"
echo " PORT        : $PORT"
echo " EVIDENCE    : $EVID"
echo " go          : $(go version 2>&1)"
echo " curl        : $(curl --version 2>&1 | head -1)"
echo " python3     : $(python3 --version 2>&1)"
echo " TIMESTAMP   : $(date '+%Y-%m-%d %H:%M:%S %z')"
echo "===================================================================="
echo ""

# --------------------------------------------------------------------------
# [S0] 真实编译服务端
# --------------------------------------------------------------------------
echo "--------------------------------------------------------------------"
echo " [S0] go build ./src/cmd/server  (真实编译，输出原样保留)"
echo "--------------------------------------------------------------------"
go build -o "$BIN" ./src/cmd/server > "$EVID/S0_build.stdout" 2> "$EVID/S0_build.stderr"
BUILD_RC=$?
echo " BUILD_EXIT  : $BUILD_RC"
echo " BUILD_STDOUT: $(cat "$EVID/S0_build.stdout")"
echo " BUILD_STDERR: $(cat "$EVID/S0_build.stderr")"
if [ "$BUILD_RC" -eq 0 ] && [ -x "$BIN" ]; then
  BUILD_OK=1
  echo " BUILD_OK    : True  BIN=$BIN  bytes=$(wc -c < "$BIN" | tr -d ' ')"
else
  echo " BUILD_OK    : False —— 服务未编译成功；后续 curl 将记录真实的连接失败状态码"
fi
echo ""

# --------------------------------------------------------------------------
# [S1] 真实启动服务
# --------------------------------------------------------------------------
echo "--------------------------------------------------------------------"
echo " [S1] 后台启动服务并等待 /healthz（最多 20 次 × 0.5s）"
echo "--------------------------------------------------------------------"
if [ "$BUILD_OK" -eq 1 ]; then
  SERVER_PORT="$PORT" \
  SERVER_BASE_URL="$BASE" \
  JWT_SECRET="e2e-fixed-jwt-secret" \
  MASTER_KEY="$MASTER_KEY" \
  ADMIN_EMAIL="e2e-admin@llm-hub.local" \
  ADMIN_PASSWORD="E2ePassw0rd!2026" \
  REGISTRATION_OPEN="true" \
  RATE_RPM_DEFAULT="0" \
  PROBE_ENABLED="false" \
  LOG_FULL_BODY="false" \
  DATA_DIR="/tmp/llm-hub-e2e-data-$$" \
    "$BIN" > "$LOG" 2>&1 &
  SERVER_PID=$!
  echo " SERVER_PID  : $SERVER_PID"
  echo " SERVER_LOG  : $LOG"
  i=0
  while [ "$i" -lt 20 ]; do
    code="$(curl -s -o "$EVID/S1_probe.body" -w '%{http_code}' --max-time 2 "$BASE/healthz" 2>/dev/null)"
    if [ "$code" = "200" ]; then
      SERVER_UP=1
      echo " READY       : True (attempt $i, /healthz=200)"
      break
    fi
    i=$((i + 1))
    sleep 0.5
  done
  echo " SERVER_UP   : $([ "$SERVER_UP" -eq 1 ] && echo True || echo False)"
  echo " SERVER_LOG_TAIL:"
  tail -n 12 "$LOG" 2>/dev/null | sed 's/^/    /'
else
  echo " SERVER_PID  : (none, build failed)"
  echo " SERVER_UP   : False"
fi
echo ""

# --------------------------------------------------------------------------
# req —— 通用 curl 记录器（真实状态码 + 响应片段 + 落盘）
# 用法: req LABEL METHOD PATH [BODY] [AUTH_HEADER]
# 结果写入 REQ_LAST_CODE / REQ_LAST_BODY，供后续 python3 解析。
# --------------------------------------------------------------------------
req() {
  N=$((N + 1))
  local label="$1" method="$2" path="$3" body="${4:-}" auth="${5:-}"
  local url="$BASE$path"
  local bf="$EVID/$(printf '%02d' "$N")_${label}.body"
  local ef="$EVID/$(printf '%02d' "$N")_${label}.stderr"
  # 连接失败时 curl 不会创建 -o 目标文件；先落空文件，保证后续 python3 得到
  # BODY_MISSING 事实，而不是 FileNotFoundError 栈回溯。
  : > "$bf"
  : > "$ef"
  local -a cargs
  cargs=(-s -S -X "$method" -o "$bf" -w '%{http_code}' --max-time 10)
  if [ -n "$body" ]; then
    cargs+=(-H 'Content-Type: application/json' --data-binary "$body")
  fi
  if [ -n "$auth" ]; then
    cargs+=(-H "Authorization: $auth")
  fi
  local code snippet
  code="$(curl "${cargs[@]}" "$url" 2> "$ef")"
  snippet="$(head -c 320 "$bf" 2>/dev/null | tr '\n' ' ')"
  echo "  [CHECK $N] $label"
  echo "    REQUEST     : $method $path"
  echo "    HTTP_STATUS : ${code:-000}"
  echo "    BODY_SNIPPET: ${snippet:-<empty>}"
  echo "    BODY_FILE   : $bf"
  if [ -s "$ef" ]; then
    echo "    CURL_STDERR : $(head -c 200 "$ef" | tr '\n' ' ')"
  fi
  echo "${code:-000}" > "$EVID/$(printf '%02d' "$N")_${label}.status"
  REQ_LAST_CODE="${code:-000}"
  REQ_LAST_BODY="$bf"
}

# 从 /api/tokens 响应体里提取一次性明文 sk-
extract_sk() {
  python3 - "$1" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    if isinstance(d, dict):
        for k in ("token", "sk", "value"):
            if isinstance(d.get(k), str) and d[k].startswith("sk-"):
                print(d[k]); break
except Exception:
    pass
PY
}

# --------------------------------------------------------------------------
# [S2] 健康检查 / 指标 / 管理面鉴权
# --------------------------------------------------------------------------
echo "--------------------------------------------------------------------"
echo " [S2] GET /healthz · GET /metrics · /api/* 无效令牌 401"
echo "--------------------------------------------------------------------"
req "healthz" "GET" "/healthz"
HEALTH_CODE="$REQ_LAST_CODE"
req "metrics" "GET" "/metrics"
METRICS_BODY="$REQ_LAST_BODY"
req "api_me_invalid_token" "GET" "/api/me" "" "Bearer invalid-token-000"
API401_CODE="$REQ_LAST_CODE"
echo ""

# --------------------------------------------------------------------------
# [S3] 注册 / 登录 / 创建两个独立平台令牌
# --------------------------------------------------------------------------
echo "--------------------------------------------------------------------"
echo " [S3] 注册 / 登录 / 创建 sk- 令牌（主令牌 rpm=1000 + 限流专用 rpm=2）"
echo "--------------------------------------------------------------------"
EMAIL="e2e$(date +%s)@llm-hub.local"
REG_BODY="{\"email\":\"$EMAIL\",\"password\":\"E2ePassw0rd!2026\"}"
req "auth_register" "POST" "/api/auth/register" "$REG_BODY"
REGISTER_BODY="$REQ_LAST_BODY"
req "auth_login" "POST" "/api/auth/login" "$REG_BODY"
LOGIN_BODY="$REQ_LAST_BODY"

JWT="$(python3 - "$LOGIN_BODY" "$REGISTER_BODY" <<'PY'
import json, sys
def load(p):
    try:
        return json.load(open(p))
    except Exception:
        return {}
for p in sys.argv[1:]:
    d = load(p)
    if not isinstance(d, dict):
        continue
    got = ""
    for k in ("access_token", "token"):
        if isinstance(d.get(k), str) and d[k]:
            got = d[k]; break
    if not got:
        t = d.get("tokens")
        if isinstance(t, dict) and isinstance(t.get("access_token"), str):
            got = t["access_token"]
    if got:
        print(got); break
PY
)"
echo "  JWT_ACQUIRED  : $([ -n "$JWT" ] && echo "True (len=${#JWT})" || echo "False")"
if [ -n "$JWT" ]; then
  echo "$JWT" > "$EVID/jwt.txt"
fi

if [ -n "$JWT" ]; then
  # 主令牌：高 RPM，保证 S4/S5/S6 不被限流截断，能真实到达 upstream_error 分支
  req "tokens_create_main" "POST" "/api/tokens" \
    '{"name":"e2e-main","allowed_models":[],"rpm":1000,"tpm":1000000,"quota_usd":0}' \
    "Bearer $JWT"
  SK="$(extract_sk "$REQ_LAST_BODY")"
  # 限流专用令牌：rpm=2，供 S7 连续请求观察 429，与主令牌隔离
  req "tokens_create_rpm" "POST" "/api/tokens" \
    '{"name":"e2e-rpm","allowed_models":[],"rpm":2,"tpm":200000,"quota_usd":0}' \
    "Bearer $JWT"
  SK_RPM="$(extract_sk "$REQ_LAST_BODY")"
fi
echo "  SK_ACQUIRED    : $([ -n "$SK" ] && echo "True (len=${#SK})" || echo "False")"
echo "  SK_RPM_ACQUIRED: $([ -n "$SK_RPM" ] && echo "True (len=${#SK_RPM})" || echo "False")"
if [ -n "$SK" ]; then
  echo "$SK" > "$EVID/sk.txt"
  V1AUTH="Bearer $SK"
else
  echo "  NOTE: 未取得主 sk- 令牌，/v1 请求使用无效令牌，如实记录 401"
fi
if [ -n "$SK_RPM" ]; then
  echo "$SK_RPM" > "$EVID/sk_rpm.txt"
  RPM_AUTH="Bearer $SK_RPM"
else
  echo "  NOTE: 未取得限流专用令牌，S7 退回主令牌，429 观察结果如实记录"
  RPM_AUTH="$V1AUTH"
fi
echo ""

# --------------------------------------------------------------------------
# [S4] /v1/models —— 真实模型且数量可核对
# --------------------------------------------------------------------------
echo "--------------------------------------------------------------------"
echo " [S4] GET /v1/models（真实模型 + 数量核对 + id ∈ data/models-real.json）"
echo "--------------------------------------------------------------------"
req "v1_models" "GET" "/v1/models" "" "$V1AUTH"
MODELS_BODY="$REQ_LAST_BODY"
python3 - "$MODELS_BODY" <<'PY'
import json, sys, os
truth_path = "data/models-real.json"
try:
    truth = set(m["id"] for m in json.load(open(truth_path))["data"])
except Exception as e:
    print("  TRUTH_LOAD_ERROR :", repr(e)); sys.exit(0)
p = sys.argv[1]
if not os.path.isfile(p) or os.path.getsize(p) == 0:
    print("  BODY_MISSING_OR_EMPTY : True (curl 未取得响应体，真实记录)")
    sys.exit(0)
raw = open(p, "r", errors="replace").read()
try:
    d = json.loads(raw)
except Exception as e:
    print("  PARSE_ERROR      :", repr(e))
    print("  BODY_FIRST_200   :", raw[:200])
    sys.exit(0)
arr = d.get("data") if isinstance(d, dict) else d
if not isinstance(arr, list):
    print("  NO_DATA_ARRAY    : True  keys=", list(d.keys()) if isinstance(d, dict) else type(d).__name__)
    sys.exit(0)
ids = [x.get("id") for x in arr if isinstance(x, dict)]
nin = [i for i in ids if i not in truth]
print("  TRUTH_MODELS     :", len(truth))
print("  RETURNED_MODELS  :", len(ids))
print("  FIRST_3_IDS      :", ids[:3])
print("  NOT_IN_REAL      :", len(nin), nin[:5])
print("  VERDICT          :", "PASS(全部真实且非空)" if ids and not nin else "FAIL(存在编造 id 或空列表)")
PY
echo ""

# --------------------------------------------------------------------------
# [S5] /v1/chat/completions —— 无上游凭据 → 统一 upstream_error，无伪造输出
# --------------------------------------------------------------------------
echo "--------------------------------------------------------------------"
echo " [S5] POST /v1/chat/completions（无上游凭据：统一 upstream_error，响应体不含伪造模型输出）"
echo "--------------------------------------------------------------------"
MODEL_ID="$(python3 - <<'PY'
import json
print(json.load(open("data/models-real.json"))["data"][0]["id"])
PY
)"
echo "  MODEL_USED (真实 id，取自 data/models-real.json): $MODEL_ID"
CHAT_REQ_BODY="{\"model\":\"$MODEL_ID\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"stream\":false}"
req "v1_chat_completions" "POST" "/v1/chat/completions" "$CHAT_REQ_BODY" "$V1AUTH"
CHAT_BODY="$REQ_LAST_BODY"
python3 - "$CHAT_BODY" <<'PY'
import json, sys, os
p = sys.argv[1]
if not os.path.isfile(p) or os.path.getsize(p) == 0:
    print("  BODY_MISSING_OR_EMPTY : True (curl 未取得响应体，真实记录)")
    sys.exit(0)
raw = open(p, "r", errors="replace").read()
low = raw.lower()
has_err = "upstream_error" in low
has_choices = '"choices"' in low
has_content = '"content"' in low
try:
    d = json.loads(raw)
    etype = ""
    if isinstance(d, dict) and isinstance(d.get("error"), dict):
        etype = d["error"].get("type") or d["error"].get("code") or ""
    print("  ERROR_TYPE_FIELD :", etype or "<none>")
except Exception as e:
    print("  PARSE_ERROR      :", repr(e))
print("  HAS_UPSTREAM_ERROR   :", has_err)
print("  HAS_CHOICES_KEY      :", has_choices)
print("  HAS_CONTENT_KEY      :", has_content)
print("  FABRICATED_OUTPUT    :", has_choices and has_content and not has_err)
print("  VERDICT              :", "PASS(统一 upstream_error 且无伪造输出)" if (has_err and not has_choices) else "CHECK(见上列事实)")
PY
echo ""

# --------------------------------------------------------------------------
# [S6] /v1/messages —— Anthropic 入口（主令牌高 RPM，不被限流截断）
# --------------------------------------------------------------------------
echo "--------------------------------------------------------------------"
echo " [S6] POST /v1/messages（Anthropic Messages 兼容入口，无上游凭据）"
echo "--------------------------------------------------------------------"
MSG_REQ_BODY="{\"model\":\"$MODEL_ID\",\"max_tokens\":16,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"
req "v1_messages" "POST" "/v1/messages" "$MSG_REQ_BODY" "$V1AUTH"
MSG_BODY="$REQ_LAST_BODY"
python3 - "$MSG_BODY" <<'PY'
import json, sys, os
p = sys.argv[1]
if not os.path.isfile(p) or os.path.getsize(p) == 0:
    print("  BODY_MISSING_OR_EMPTY : True (curl 未取得响应体，真实记录)")
    sys.exit(0)
raw = open(p, "r", errors="replace").read()
low = raw.lower()
has_err = "upstream_error" in low
has_choices = '"choices"' in low
has_content = '"content"' in low
print("  HAS_UPSTREAM_ERROR   :", has_err)
print("  HAS_CHOICES_KEY      :", has_choices)
print("  HAS_CONTENT_KEY      :", has_content)
print("  FABRICATED_OUTPUT    :", has_choices and has_content and not has_err)
print("  VERDICT              :", "PASS(统一 upstream_error 且无伪造输出)" if (has_err and not has_choices) else "CHECK(见上列事实)")
PY
echo ""

# --------------------------------------------------------------------------
# [S7] F9 限流生效 —— 独立 rpm=2 令牌：前 2 次 200，第 3~8 次必须 429
# --------------------------------------------------------------------------
echo "--------------------------------------------------------------------"
echo " [S7] F9 令牌限流生效（SPEC.md C-2：RPM 限流必须真实拒绝；使用独立 rpm=2 令牌）"
echo "--------------------------------------------------------------------"
RATE_FILE="$EVID/rpm_sequence.txt"
: > "$RATE_FILE"
j=0
while [ "$j" -lt 8 ]; do
  j=$((j + 1))
  c="$(curl -s -o "$EVID/rpm_${j}.body" -w '%{http_code}' --max-time 10 \
        -H "Authorization: $RPM_AUTH" "$BASE/v1/models" 2> "$EVID/rpm_${j}.stderr")"
  echo "  REQ_${j} : HTTP ${c:-000}" | tee -a "$RATE_FILE"
done
R200="$(grep -c 'HTTP 200' "$RATE_FILE" || true)"
R429="$(grep -c 'HTTP 429' "$RATE_FILE" || true)"
R402="$(grep -c 'HTTP 402' "$RATE_FILE" || true)"
echo "  HTTP_200_COUNT : ${R200:-0} / 8（期望 2，= 令牌 rpm 值）"
echo "  HTTP_429_COUNT : ${R429:-0}（期望 6）"
echo "  HTTP_402_COUNT : ${R402:-0}（本用例未设额度，期望 0）"
grep -q 'rpm_exceeded' "$EVID/rpm_3.body" 2>/dev/null && RCODE_OK=True || RCODE_OK=False
echo "  REJECT_CODE    : rpm_exceeded 出现于首个被拒响应 = ${RCODE_OK}"
if [ "${R200:-0}" -eq 2 ] && [ "${R429:-0}" -eq 6 ] && [ "${R402:-0}" -eq 0 ] && [ "$RCODE_OK" = "True" ]; then
  echo "  VERDICT        : PASS(F9 限流真实生效：2×200 + 6×429，拒绝码 rpm_exceeded)"
else
  echo "  VERDICT        : FAIL(与 F9 预期不符，如实记录：200=${R200:-0} 429=${R429:-0} 402=${R402:-0} code=${RCODE_OK})"
fi
echo ""

# --------------------------------------------------------------------------
# [S8] SSRF 防护 —— 内网 base_uri 被拒
# --------------------------------------------------------------------------
echo "--------------------------------------------------------------------"
echo " [S8] SSRF 防护（提交内网/回环/元数据 base_uri 必须被拒）"
echo "--------------------------------------------------------------------"
SSRF_AUTH="Bearer ${JWT:-invalid-token-000}"
i=0
for U in "http://127.0.0.1:8080/v1" "http://169.254.169.254/latest/meta-data/" "file:///etc/passwd" "http://10.0.0.1/v1"; do
  i=$((i + 1))
  # channel.Input 加 json 标签前后都能到达校验器：两套键名都发，
  # json.Unmarshal 忽略未知字段，任一实现都能正确绑定。
  SBODY="{\"name\":\"ssrf-$i\",\"Name\":\"ssrf-$i\",\"provider_type\":\"openai\",\"ProviderType\":\"openai\",\"base_uri\":\"$U\",\"BaseURI\":\"$U\",\"api_key\":\"fake-key\",\"APIKey\":\"fake-key\",\"visibility\":\"public\",\"Visibility\":\"public\"}"
  req "ssrf_${i}" "POST" "/api/channels" "$SBODY" "$SSRF_AUTH"
done
echo "  SSRF_BASE_URIS_TESTED : 4 (127.0.0.1 / 169.254.169.254 / file:// / 10.0.0.1)"
echo "  判定口径：400 且响应体含「拒绝内网/回环/元数据」「base_uri 校验失败」等 SSRF 拒绝消息才计 PASS；"
echo "            仅 400 但为「provider_type 必填」等字段错误则计 CHECK（未触及 SSRF 校验器）。"
echo ""

# --------------------------------------------------------------------------
# [S9] 汇总（全部为真实观测值，不做推断式"通过"）
# --------------------------------------------------------------------------
echo "===================================================================="
echo " [S9] 真实执行汇总（数值均来自上方 curl 与 python3 解析）"
echo "===================================================================="
echo " BUILD_OK            : $([ "$BUILD_OK" -eq 1 ] && echo True || echo False)"
echo " SERVER_UP           : $([ "$SERVER_UP" -eq 1 ] && echo True || echo False)"
echo " SERVER_PID          : ${SERVER_PID:-<none>}"
echo " JWT_ACQUIRED        : $([ -n "$JWT" ] && echo True || echo False)"
echo " SK_ACQUIRED         : $([ -n "$SK" ] && echo True || echo False)"
echo " SK_RPM_ACQUIRED     : $([ -n "$SK_RPM" ] && echo True || echo False)"
echo " TOTAL_CURL_CHECKS   : $N"
echo " EVIDENCE_DIR        : $EVID"
echo " STATUS_FILES        :"
for f in "$EVID"/*.status; do
  [ -f "$f" ] && echo "   $(basename "$f") = $(cat "$f")"
done
echo "===================================================================="
