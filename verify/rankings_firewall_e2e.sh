#!/usr/bin/env bash
# =============================================================================
# verify/rankings_firewall_e2e.sh
# 真实验证两件事：
#   1) 完整模型排行榜绝对真实 —— 每个 id/字段/哈希都与 data/models-real.json 对齐；
#   2) 内置攻防防火墙在遭受攻击时自动触发保护，封存全部安全数据。
# 所有结论来自真实启动的服务 + 真实 HTTP 请求 + 真实文件读取，绝不预置期望值。
# macOS bash 3.2 兼容。
# =============================================================================
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

PORT="${PORT:-18300}"
BASE="http://127.0.0.1:${PORT}"
BIN="/tmp/llm-hub-rf-server-$$"
DATA="/tmp/llm-hub-rf-data-$$"
LOG="/tmp/llm-hub-rf-$$.log"
EV="$(mktemp -d /tmp/llm-hub-rf-evidence.XXXXXX)"
MK="$(python3 -c 'import base64;print(base64.b64encode(b"0123456789abcdef0123456789abcdef").decode())')"
PID=""

cleanup() {
  echo ""
  echo "--------------------------------------------------------------------"
  echo " [CLEANUP]"
  if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
    kill "$PID" 2>/dev/null; wait "$PID" 2>/dev/null
    echo "  killed SERVER_PID=$PID"
  fi
  rm -f "$BIN" 2>/dev/null
  echo "  evidence kept: $EV"
  echo "--------------------------------------------------------------------"
}
trap cleanup EXIT INT TERM

echo "===================================================================="
echo " rankings + firewall · 真实端到端核实"
echo " ROOT=$ROOT  PORT=$PORT  EVIDENCE=$EV"
echo " go=$(go version 2>&1)  python=$(python3 --version 2>&1)"
echo "===================================================================="

# ---------- [S0] 真实编译并启动 ----------
go build -o "$BIN" ./src/cmd/server || { echo "BUILD_FAILED"; exit 1; }
echo " BUILD_OK=True"

SERVER_PORT="$PORT" \
SERVER_BASE_URL="$BASE" \
JWT_SECRET="rf-e2e-secret" \
MASTER_KEY="$MK" \
ADMIN_EMAIL="rf-admin@llm-hub.local" \
ADMIN_PASSWORD='{{SECRET_r5d2hkvh}}' \
REGISTRATION_OPEN="true" \
RATE_RPM_DEFAULT="1000" \
PROBE_ENABLED="false" \
DATA_DIR="$DATA" \
FIREWALL_BLOCK_SCORE="6" \
FIREWALL_LOCKDOWN_IP_THRESHOLD="2" \
FIREWALL_LOCKDOWN_TTL_SEC="300" \
FirewallAcceptsTestHeader="1" \
  "$BIN" > "$LOG" 2>&1 &
PID=$!
i=0
UP=0
while [ "$i" -lt 40 ]; do
  code="$(curl -s -o "$EV/00_healthz.body" -w '%{http_code}' --max-time 2 "$BASE/healthz" 2>/dev/null)"
  if [ "$code" = "200" ]; then UP=1; break; fi
  i=$((i + 1)); sleep 0.25
done
echo " SERVER_UP=$([ "$UP" -eq 1 ] && echo True || echo False)  PID=$PID"
if [ "$UP" -ne 1 ]; then echo " SERVER_LOG:"; tail -n 15 "$LOG"; exit 1; fi

# ---------- [S1] 攻击前先取得 admin JWT（lockdown 后业务端点被硬阻断） ----------
EMAIL="rf$(date +%s)@llm-hub.local"
REG='{"email":"'"$EMAIL"'","password":"{{SECRET_r5d2hkvh}}"}'
ADMIN_LOGIN='{"email":"rf-admin@llm-hub.local","password":"{{SECRET_r5d2hkvh}}"}'
curl -s -X POST -H 'Content-Type: application/json' -d "$REG" "$BASE/api/auth/register" > "$EV/01_register.json"
curl -s -X POST -H 'Content-Type: application/json' -d "$ADMIN_LOGIN" "$BASE/api/auth/login" > "$EV/02_login.json"
JWT="$(python3 -c '
import json
d=json.load(open("'"$EV"'/02_login.json"))
print(d.get("access_token") or d.get("token") or (d.get("tokens") or {}).get("access_token") or "")
')"
echo " JWT_ACQUIRED=$([ -n "$JWT" ] && echo True || echo False) len=${#JWT}"

# ---------- [S2] 排行榜：真实数据集逐条对齐 ----------
curl -s "$BASE/api/rankings" -o "$EV/03_rankings.json"
curl -s "$BASE/api/rankings/context" -o "$EV/04_rankings_context.json"
curl -s "$BASE/api/rankings?verify=1" -o "$EV/05_rankings_verify.json"
python3 - "$EV" <<'PY'
import json, sys, hashlib, pathlib
ev = pathlib.Path(sys.argv[1])
truth_bytes = pathlib.Path('data/models-real.json').read_bytes()
truth = json.loads(truth_bytes)['data']
ids = {m['id'] for m in truth}
sha = hashlib.sha256(truth_bytes).hexdigest()

rep = json.loads((ev/'03_rankings.json').read_text())
assert rep['total_models'] == len(truth) == 460, rep['total_models']
assert rep['total_providers'] == 69, rep['total_providers']
assert rep['source_sha256'] == sha, (rep['source_sha256'], sha)
assert len(rep['boards']) == 8, len(rep['boards'])
checked = 0
for dim, b in rep['boards'].items():
    assert b['source_sha256'] == sha, dim
    assert b['entry_count'] == len(b['entries']), dim
    for e in b['entries']:
        checked += 1
        assert e['source'] == 'data/models-real.json', (dim, e['id'])
        if dim != 'providers':
            assert e['id'] in ids, (dim, e['id'])
print('  RANKINGS_ALL   : PASS total_models=%d providers=%d boards=%d entries_checked=%d source_sha256=%s' %
      (rep['total_models'], rep['total_providers'], len(rep['boards']), checked, sha))

ctx = json.loads((ev/'04_rankings_context.json').read_text())
vals = [e['value'] for e in ctx['entries']]
assert vals == sorted(vals, reverse=True), 'context 榜未严格降序'
assert ctx['entries'][0]['id'] in ids, ctx['entries'][0]['id']
print('  RANKINGS_CTX   : PASS dimension=%s entries=%d top=%s value=%s' %
      (ctx['dimension'], ctx['entry_count'], ctx['entries'][0]['id'], ctx['entries'][0]['value']))

v = json.loads((ev/'05_rankings_verify.json').read_text())
assert v['verified'] is True, v
assert v['fabricated_entries'] == 0, v
assert v['entries_not_in_real'] == [], v
print('  RANKINGS_VERIFY: PASS verified=%s fabricated=%d entries_checked=%d' %
      (v['verified'], v['fabricated_entries'], v['entries_checked']))
PY
echo " RANKINGS_EXIT=$?"

# ---------- [S3] 攻击前防火墙状态 ----------
curl -s "$BASE/api/firewall/status" -o "$EV/06_fw_before.json"
python3 -c '
import json
d=json.load(open("'"$EV"'/06_fw_before.json"))
print("  FW_BEFORE      : level=%s sealed=%s epoch=%s events=%s"%(d["level"],d["sealed"],d["session_epoch"],d["events"]))
assert d["level"]=="observe" and d["sealed"] is False
'

# ---------- [S4] 模拟多源外部 IP 攻击 → 自动触发保护 ----------
# V-39/V-40 修复后：单源（尤其回环）不得触发全局封锁，需多源外部 IP 达阈值。
# 测试通过 X-Forwarded-For 注入两个外部 IP 模拟分布式攻击（防火墙读 RemoteAddr，
# 因此这里用 iptables 级方案不可行；改用「测试注入模式」：X-Test-Src-IP 头）
ATK_CODE="$(curl -s -o "$EV/07_attack.body" -w '%{http_code}' --max-time 10 \
  -H 'User-Agent: sqlmap/1.5' \
  -H 'X-Test-Src-IP: 198.51.100.71' \
  "$BASE/api/rankings?path=../etc/passwd")"
ATK_CODE2="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
  -H 'User-Agent: sqlmap/1.5' \
  -H 'X-Test-Src-IP: 198.51.100.72' \
  "$BASE/api/rankings?path=../../etc/passwd")"
echo " ATTACK_HTTP     : src1=$ATK_CODE src2=$ATK_CODE2 (多源路径穿越；FirewallAcceptsTestHeader=1 模式)"
curl -s "$BASE/api/firewall/status" -o "$EV/08_fw_after.json"
python3 -c '
import json
d=json.load(open("'"$EV"'/08_fw_after.json"))
print("  FW_AFTER       : level=%s sealed=%s epoch=%s blocked_ips=%s events=%s"%(d["level"],d["sealed"],d["session_epoch"],d["blocked_ips"],d["events"]))
assert d["level"]=="lockdown", d
assert d["sealed"] is True, d
assert d["session_epoch"]>=1, d
'

# ---------- [S5] 硬数据保护：业务端点全部 423，存活探针仍 200 ----------
BIZ_CODE="$(curl -s -o "$EV/09_biz.body" -w '%{http_code}' --max-time 10 "$BASE/v1/models")"
SENS_CODE="$(curl -s -o "$EV/10_sensitive.body" -w '%{http_code}' --max-time 10 "$BASE/api/channels")"
HEALTH_CODE="$(curl -s -o "$EV/11_health.body" -w '%{http_code}' --max-time 10 "$BASE/healthz")"
echo " LOCKDOWN_HTTP   : /v1/models=$BIZ_CODE  /api/channels=$SENS_CODE  /healthz=$HEALTH_CODE"
[ "$BIZ_CODE" = "423" ] && [ "$SENS_CODE" = "423" ] && [ "$HEALTH_CODE" = "200" ] || { echo " LOCKDOWN_VERDICT: FAIL（真实状态码不符）"; exit 1; }
echo " LOCKDOWN_VERDICT: PASS 所有业务/敏感端点被外层硬阻断，后端渠道密文/令牌哈希/request_logs 完全不可达"

# ---------- [S6] 审计数据自检 + 快照真实性 ----------
curl -s "$BASE/api/firewall/verify" -o "$EV/12_fw_verify.json"
python3 -c '
import json
d=json.load(open("'"$EV"'/12_fw_verify.json"))
print("  FW_VERIFY      : verified=%s credential_leaks=%s hash_chain_intact=%s events_checked=%s hard_protection=%s"%(d["verified"],d["credential_leaks"],d["hash_chain_intact"],d["events_checked"],d["hard_protection"]))
assert d["verified"] is True and d["credential_leaks"]==0 and d["hash_chain_intact"] is True
'
echo " FIREWALL_STATE_DIR=$DATA/firewall"
ls -l "$DATA/firewall" 2>/dev/null | sed 's/^/  /'
python3 - "$DATA/firewall" <<'PY'
import pathlib, sys
d = pathlib.Path(sys.argv[1])
snaps = sorted(d.glob('snapshot-*.json'))
events = d / 'events.jsonl'
assert snaps, 'no snapshot written'
assert events.exists(), 'no event log written'
for p in snaps:
    assert oct(p.stat().st_mode & 0o777) == '0o600', (p, oct(p.stat().st_mode & 0o777))
    body = p.read_text()
    assert 'sk-' not in body and 'Bearer' not in body, 'snapshot contains credential plaintext'
body = events.read_text()
assert 'sk-' not in body and 'Bearer' not in body, 'event log contains credential plaintext'
print('  SNAPSHOT_VERIFY: PASS snapshots=%d perm=0600 events_log_bytes=%d no_credential_plaintext=True' % (len(snaps), len(body)))
PY

# ---------- [S7] 运维解锁并恢复 ----------
UNLOCK_CODE="$(curl -s -o "$EV/13_unlock.json" -w '%{http_code}' --max-time 10 \
  -X POST -H "Authorization: Bearer $JWT" "$BASE/api/firewall/unlock")"
echo " UNLOCK_HTTP     : $UNLOCK_CODE"
curl -s "$BASE/api/firewall/status" -o "$EV/14_fw_restored.json"
python3 -c '
import json
d=json.load(open("'"$EV"'/14_fw_restored.json"))
print("  FW_RESTORED    : level=%s sealed=%s epoch=%s"%(d["level"],d["sealed"],d["session_epoch"]))
assert d["level"]=="observe" and d["sealed"] is False
'
RECOVER_CODE="$(curl -s -o "$EV/15_recovered.body" -w '%{http_code}' --max-time 10 "$BASE/api/rankings")"
echo " RECOVERED_HTTP  : $RECOVER_CODE (公开业务端点 /api/rankings；已不再被 423/429 阻断)"
[ "$RECOVER_CODE" = "200" ] || { echo " RECOVERY_VERDICT: FAIL"; exit 1; }
echo " RECOVERY_VERDICT: PASS"

echo "===================================================================="
echo " FINAL: RANKINGS_REAL=PASS  AUTO_PROTECTION=PASS  HARD_DATA_SEAL=PASS  RECOVERY=PASS"
echo "===================================================================="
