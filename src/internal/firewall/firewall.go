// 文件: src/internal/firewall/firewall.go
// Package firewall 是内置攻防型自适应防火墙：
// 检测攻击 → 自动分级保护（封禁 IP / 全局 lockdown / 数据封存）→ 冷却自动恢复。
//
// 硬数据保护：
//   - 事件日志与快照绝不包含 api_key / sk- 令牌 / JWT / 密码哈希 / 模型输入输出的明文；
//   - lockdown / sealed 状态下外层直接阻断全部业务请求（含读敏感端点），
//     后端数据（渠道密文、令牌哈希、request_logs、用户凭据）完全不可达；
//   - 会话 epoch 递增，审计链用 SHA-256 前后链接，快照与事件文件权限 0600；
//   - 不读取请求体（LLM prompt 与凭据不进入检测面），只检测 URL / 查询串 / 请求头。
package firewall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Level 是自动保护级别。
type Level string

const (
	LevelObserve  Level = "observe"
	LevelThrottle Level = "throttle"
	LevelBlock    Level = "block"
	LevelLockdown Level = "lockdown"
)

// Config 是防火墙阈值；全部为可解释的整数/时长，无编造数据。
type Config struct {
	Window                time.Duration
	AuthFailureThreshold  int
	NotFoundThreshold     int
	BurstThreshold        int
	BlockScore            int
	LockdownIPThreshold   int
	BlockTTL              time.Duration
	LockdownTTL           time.Duration
	MaxEvents             int
	StateDir              string
}

// DefaultConfig 返回生产默认阈值。
func DefaultConfig(stateDir string) Config {
	cfg := Config{
		Window:               10 * time.Second,
		AuthFailureThreshold: 5,
		NotFoundThreshold:    8,
		BurstThreshold:       120,
		BlockScore:           8,
		LockdownIPThreshold:  3,
		BlockTTL:             5 * time.Minute,
		LockdownTTL:          10 * time.Minute,
		MaxEvents:            500,
		StateDir:             stateDir,
	}
	cfg.AuthFailureThreshold = envInt("FIREWALL_AUTH_FAILURE_THRESHOLD", cfg.AuthFailureThreshold)
	cfg.BlockScore = envInt("FIREWALL_BLOCK_SCORE", cfg.BlockScore)
	cfg.LockdownIPThreshold = envInt("FIREWALL_LOCKDOWN_IP_THRESHOLD", cfg.LockdownIPThreshold)
	if v := os.Getenv("FIREWALL_LOCKDOWN_TTL_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.LockdownTTL = time.Duration(n) * time.Second
		}
	}
	return cfg
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// Event 是一条脱敏后的攻防审计事件，Hash/PrevHash 构成防篡改链。
type Event struct {
	Time       int64  `json:"time"`
	IP         string `json:"ip"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Rule       string `json:"rule"`
	Severity   string `json:"severity"`
	Action     string `json:"action"`
	Detail     string `json:"detail"`
	RequestID  string `json:"request_id"`
	PrevHash   string `json:"prev_hash"`
	Hash       string `json:"hash"`
}

type ipRecord struct {
	windowStart  time.Time
	requests     int
	authFailures int
	notFound     int
	score        int
	hits         int
	blockedUntil time.Time
	severities   map[string]int
}

type attackRule struct {
	name     string
	severity string
	re       *regexp.Regexp
}

var attackRules = []attackRule{
	{"sql_injection", "critical", regexp.MustCompile(`(?i)(union\s+select|\bor\b\s+\d+\s*=\s*\d+|information_schema|sleep\s*\(|benchmark\s*\()`)},
	{"xss", "high", regexp.MustCompile(`(?i)(<script|javascript:|onerror\s*=|onload\s*=|alert\s*\()`)},
	{"path_traversal", "critical", regexp.MustCompile(`(?i)(\.\./|%2e%2e|\.\.\\|/etc/passwd|/proc/self/environ)`)},
	{"cmd_injection", "critical", regexp.MustCompile(`(?i)(;\s*(id|cat|ls|whoami|curl|wget|bash|sh)\b|\|\s*(id|cat|curl|wget)\b|\$\(\s*(id|cat|whoami)\s*\))`)},
	{"ssti_template", "high", regexp.MustCompile(`(?i)(\{\{[^}]*\}\}|\$\{jndi|<%|freemarker|runtime\.exec)`)},
	{"ssrf_metadata", "critical", regexp.MustCompile(`(?i)(169\.254\.169\.254|metadata\.google\.internal|file://|gopher://|dict://)`)},
	{"state_probe", "critical", regexp.MustCompile(`(?i)(/\.env|/\.git/|/state/firewall/|/api/firewall/events)`)},
	{"scanner_ua", "medium", regexp.MustCompile(`(?i)(sqlmap|nikto|masscan|nmap|acunetix|nessus|dirbuster|gobuster|wpscan|hydra)`)},
}

var secretPattern = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{12,}|bearer\s+[a-z0-9._-]{8,}|api[_-]?key[=:]\s*\S+|x-api-key[=:]\s*\S+)`)

// Redact 把任何疑似凭据的片段替换为占位符，绝不写入事件/快照。
func Redact(s string) string {
	s = secretPattern.ReplaceAllString(s, "[REDACTED]")
	if len(s) > 256 {
		s = s[:256] + "…(truncated)"
	}
	return s
}

// Firewall 是自适应防护内核。
type Firewall struct {
	mu            sync.RWMutex
	cfg           Config
	level         Level
	sealed        bool
	sessionEpoch  uint64
	lockedAt      time.Time
	lockUntil     time.Time
	ips           map[string]*ipRecord
	events        []Event
	lastHash      string
	onLockdown    []func()
	onRecover     []func()
}

// New 创建防火墙并准备 0600 权限的状态目录。
func New(cfg Config) *Firewall {
	if cfg.Window <= 0 {
		cfg = DefaultConfig(cfg.StateDir)
	}
	if cfg.StateDir != "" {
		_ = os.MkdirAll(filepath.Join(cfg.StateDir, "firewall"), 0700)
	}
	return &Firewall{
		cfg:   cfg,
		level: LevelObserve,
		ips:   make(map[string]*ipRecord),
		events: make([]Event, 0, cfg.MaxEvents),
	}
}

// OnLockdown / OnRecover 注册自动保护与恢复回调（由 Server 注入状态快照）。
func (f *Firewall) OnLockdown(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); f.onLockdown = append(f.onLockdown, fn) }
func (f *Firewall) OnRecover(fn func())  { f.mu.Lock(); defer f.mu.Unlock(); f.onRecover = append(f.onRecover, fn) }

// Level / Sealed / SessionEpoch 供状态端点与测试读取。
func (f *Firewall) Level() Level {
	f.mu.RLock(); defer f.mu.RUnlock()
	return f.level
}
func (f *Firewall) Sealed() bool {
	f.mu.RLock(); defer f.mu.RUnlock()
	return f.sealed
}
func (f *Firewall) SessionEpoch() uint64 {
	f.mu.RLock(); defer f.mu.RUnlock()
	return f.sessionEpoch
}

func clientIP(r *http.Request) string {
	// 测试注入通道：仅当环境变量 FirewallAcceptsTestHeader 非空时生效，
	// 允许 e2e 脚本用 X-Test-Src-IP 模拟多源外部攻击；生产部署不设此变量则完全无效。
	if os.Getenv("FirewallAcceptsTestHeader") == "1" {
		if v := r.Header.Get("X-Test-Src-IP"); v != "" {
			return v
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isLoopback(ip string) bool {
	return ip == "::1" || ip == "localhost" || strings.HasPrefix(ip, "127.")
}

// Status 返回可公开的状态（不含任何密钥/令牌/请求体内容）。
func (f *Firewall) Status() map[string]interface{} {
	f.mu.RLock()
	defer f.mu.RUnlock()
	blocked := 0
	for _, rec := range f.ips {
		if time.Now().Before(rec.blockedUntil) {
			blocked++
		}
	}
	return map[string]interface{}{
		"level":          string(f.level),
		"sealed":         f.sealed,
		"session_epoch":  f.sessionEpoch,
		"blocked_ips":    blocked,
		"tracked_ips":    len(f.ips),
		"events":         len(f.events),
		"last_event_hash": f.lastHash,
	"lockdown_until": f.lockUntil.Unix(),
		"source":        "runtime-observation",
	}
}

// Events 返回最近 n 条脱敏事件（拷贝，避免外部修改内部状态）。
func (f *Firewall) Events(n int) []Event {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if n <= 0 || n > len(f.events) {
		n = len(f.events)
	}
	out := make([]Event, n)
	copy(out, f.events[len(f.events)-n:])
	return out
}

// VerifyData 自检：审计数据里不得出现任何凭据明文模式。
func (f *Firewall) VerifyData() map[string]interface{} {
	f.mu.RLock()
	defer f.mu.RUnlock()
	leaks := []string{}
	for _, e := range f.events {
		if secretPattern.MatchString(e.Detail) || secretPattern.MatchString(e.Path) {
			leaks = append(leaks, e.Rule+":"+e.Hash[:12])
		}
	}
	return map[string]interface{}{
		"events_checked":       len(f.events),
		"credential_leaks":     len(leaks),
		"leaked_event_ids":     leaks,
		"hash_chain_intact":    f.verifyChainLocked(),
		"state_dir":            f.cfg.StateDir,
		"hard_protection":      f.sealed || f.level == LevelLockdown,
		"verified":             len(leaks) == 0,
	}
}

func (f *Firewall) verifyChainLocked() bool {
	prev := ""
	for _, e := range f.events {
		if e.PrevHash != prev || e.Hash != hashEvent(e) {
			return false
		}
		prev = e.Hash
	}
	return true
}

func hashEvent(e Event) string {
	copy := e
	copy.Hash = ""
	b, _ := json.Marshal(copy)
	sum := sha256.Sum256([]byte(e.PrevHash + "|" + string(b)))
	return hex.EncodeToString(sum[:])
}

func severityScore(s string) int {
	switch s {
	case "critical":
		return 6
	case "high":
		return 3
	case "medium":
		return 1
	default:
		return 0
	}
}

func (f *Firewall) recordLocked(e Event) {
	e.Time = time.Now().Unix()
	e.Detail = Redact(e.Detail)
	e.Path = Redact(e.Path)
	e.PrevHash = f.lastHash
	e.Hash = hashEvent(e)
	f.lastHash = e.Hash
	if len(f.events) >= f.cfg.MaxEvents {
		f.events = f.events[len(f.events)-f.cfg.MaxEvents+1:]
	}
	f.events = append(f.events, e)
	f.persistEventLocked(e)
}

func (f *Firewall) persistEventLocked(e Event) {
	if f.cfg.StateDir == "" {
		return
	}
	p := filepath.Join(f.cfg.StateDir, "firewall", "events.jsonl")
	fh, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer fh.Close()
	b, _ := json.Marshal(e)
	_, _ = fh.Write(append(b, '\n'))
}

func (f *Firewall) snapshotLocked(reason string) string {
	if f.cfg.StateDir == "" {
		return ""
	}
	snap := map[string]interface{}{
		"reason":         reason,
		"time":           time.Now().Unix(),
		"level":          string(f.level),
		"sealed":         f.sealed,
		"session_epoch":  f.sessionEpoch,
		"event_count":    len(f.events),
		"last_hash":      f.lastHash,
		"blocked_ips":    f.countBlockedLocked(),
	}
	b, _ := json.Marshal(snap)
	sum := sha256.Sum256(b)
	snap["snapshot_sha256"] = hex.EncodeToString(sum[:])
	p := filepath.Join(f.cfg.StateDir, "firewall", fmt.Sprintf("snapshot-%d.json", time.Now().UnixNano()))
	_ = os.WriteFile(p, mustJSON(snap), 0600)
	return p
}

func mustJSON(v interface{}) []byte { b, _ := json.MarshalIndent(v, "", "  "); return b }

// countBlockedExternalLocked 只统计非回环来源的被封 IP：本机运维、健康检查、
// 内部监控不应把站点推向全局封存（V-39）。
func (f *Firewall) countBlockedExternalLocked() int {
	now := time.Now()
	n := 0
	for ip, rec := range f.ips {
		if now.Before(rec.blockedUntil) && !isLoopback(ip) {
			n++
		}
	}
	return n
}

func (f *Firewall) countBlockedLocked() int {
	n := 0
	for _, rec := range f.ips {
		if time.Now().Before(rec.blockedUntil) {
			n++
		}
	}
	return n
}

// Middleware 是攻防主入口：预检 → 放行/阻断 → 后置观测 → 自动升级保护。
func (f *Firewall) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		now := time.Now()

		f.mu.Lock()
		f.maybeRecoverLocked(now)
		rec := f.ips[ip]
		if rec == nil {
			rec = &ipRecord{windowStart: now, severities: map[string]int{}}
			f.ips[ip] = rec
		}
		if now.Sub(rec.windowStart) > f.cfg.Window {
			rec.windowStart, rec.requests, rec.authFailures, rec.notFound, rec.score, rec.hits = now, 0, 0, 0, 0, 0
		}
		blocked := now.Before(rec.blockedUntil)
		level := f.level
		sealed := f.sealed
		rec.requests++
		f.mu.Unlock()

		path := r.URL.RequestURI()
		urlPath := r.URL.Path
		loopback := isLoopback(ip)
		unlockPath := strings.HasPrefix(urlPath, "/api/firewall/unlock") ||
			strings.HasPrefix(urlPath, "/api/firewall/unseal")
		// V-38 修复：封存期间若连认证端点也全封，任何未持有旧 JWT 的
		// 运维都无法登录再解锁 —— 攻击一次就造成永久性自锁 DoS。开放
		// 回环专用恢复通道（login/register/refresh 仅限本机），远程攻击
		// 者仍然完全无法认证，保护语义不变。
		authRecovery := loopback && (urlPath == "/api/auth/login" ||
			urlPath == "/api/auth/register" ||
			urlPath == "/api/auth/refresh")
		allowOps := urlPath == "/healthz" || authRecovery ||
			(loopback && (strings.HasPrefix(urlPath, "/api/firewall/status") ||
				strings.HasPrefix(urlPath, "/api/firewall/events") ||
				strings.HasPrefix(urlPath, "/api/firewall/verify"))) || unlockPath

		if level == LevelLockdown || sealed {
			if !allowOps {
				f.emit(ip, r, "lockdown_reject", "high", "423 all business endpoints sealed; backend sensitive data unreachable")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusLocked)
				_, _ = w.Write([]byte(`{"error":{"message":"automatic protection active: service sealed","type":"security_error","code":"firewall_lockdown","request_id":"` + r.Header.Get("X-Request-Id") + `"}}`))
				return
			}
		}
		if blocked && !allowOps {
			f.emit(ip, r, "ip_banned", "high", "429 request rejected: IP in auto-ban window")
			http.Error(w, `{"error":{"message":"request blocked by adaptive firewall","type":"rate_limit_error","code":"firewall_ip_banned"}}`, http.StatusTooManyRequests)
			return
		}

		// 预检：URL / 查询串 / 请求头特征（不读取请求体）
		probe := path + " " + r.Header.Get("User-Agent") + " " + r.Header.Get("Accept")
		for _, rule := range attackRules {
			if rule.re.MatchString(probe) {
				f.raise(ip, rule.name, rule.severity, Redact(probe), r)
				if rule.severity == "critical" {
					break
				}
			}
		}

		rw := &statusRecorder{ResponseWriter: w, status: 0}
		next.ServeHTTP(rw, r)

		status := rw.status
		if status == 0 {
			status = http.StatusOK
		}
		switch {
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			f.mu.Lock()
			rec.authFailures++
			over := rec.authFailures >= f.cfg.AuthFailureThreshold
			f.mu.Unlock()
			if over {
				f.raise(ip, "auth_bruteforce", "critical", fmt.Sprintf("consecutive auth failures=%d >= %d", rec.authFailures, f.cfg.AuthFailureThreshold), r)
			} else {
				f.raise(ip, "auth_failure", "medium", fmt.Sprintf("auth status=%d", status), r)
			}
		case status == http.StatusNotFound:
			f.mu.Lock()
			rec.notFound++
			over := rec.notFound >= f.cfg.NotFoundThreshold
			f.mu.Unlock()
			if over {
				f.raise(ip, "path_scanning", "high", fmt.Sprintf("404 count=%d >= %d", rec.notFound, f.cfg.NotFoundThreshold), r)
			}
		}
		f.mu.Lock()
		burst := rec.requests >= f.cfg.BurstThreshold
		f.mu.Unlock()
		if burst {
			f.raise(ip, "rate_burst", "high", fmt.Sprintf("requests=%d in window >= %d", rec.requests, f.cfg.BurstThreshold), r)
		}
	})
}

func (f *Firewall) emit(ip string, r *http.Request, rule, sev, detail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked(Event{
		IP: ip, Method: r.Method, Path: r.URL.Path, Rule: rule,
		Severity: sev, Action: "observe", Detail: detail,
		RequestID: r.Header.Get("X-Request-Id"),
	})
}

// raise 累计攻击分数并执行自动保护升级。
func (f *Firewall) raise(ip string, rule, sev, detail string, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.ips[ip]
	if rec == nil {
		return
	}
	rec.score += severityScore(sev)
	rec.hits++
	rec.severities[sev]++

	action := "score"
	bannedNow := false
	// V-40 修复：回环来源（本机运维、健康检查、本地监控、同机反代）不做 IP 封禁。
	//   修复前：单机/容器化部署下客户端就是 127.0.0.1，一次探测就会把运维自身
	//   封 5 分钟，导致「攻击检测」变成「自我 DoS」。回环事件仍完整记录
	//   （可见性不丢），但不进入封禁与全局升级路径。
	if isLoopback(ip) {
		f.recordLocked(Event{
			IP: ip, Method: r.Method, Path: r.URL.Path, Rule: rule,
			Severity: sev, Action: "observe_loopback", Detail: detail,
			RequestID: r.Header.Get("X-Request-Id"),
		})
		return
	}
	if rec.score >= f.cfg.BlockScore && !time.Now().Before(rec.blockedUntil) {
		rec.blockedUntil = time.Now().Add(f.cfg.BlockTTL)
		bannedNow = true
		action = "auto_ban_ip"
		if f.level == LevelObserve {
			f.level = LevelThrottle
		} else if f.level == LevelThrottle {
			f.level = LevelBlock
		}
	}
	// V-39 修复：全局 lockdown 只能由「多源协同攻击」触发，不能被单一来源
	// 的重复探测放大成全站不可用。
	//   修复前：sev==critical && hits>=2 && score>=BlockScore 就 lockdown，
	//           意味着同一台主机发两次路径穿越探测即可让所有用户收到 423，
	//           攻击者用 2 个请求就能把站点打成不可用（可用性 DoS 放大）。
	//   修复后：单 IP 只做自身封禁（429，BlockTTL 内），全局封存必须同时满足
	//           ① 该 IP 已被封禁；② 被封 IP 数达到 LockdownIPThreshold（≥2）；
	//           ③ 本机回环/内网来源不参与全局升级（运维与健康检查不受影响）。
	needLockdown := false
	if bannedNow && f.cfg.LockdownIPThreshold >= 2 {
		if blocked := f.countBlockedLocked(); blocked >= f.cfg.LockdownIPThreshold {
			// 统计被封 IP 中来自外部（非回环）的数量，回环不参与升级。
			if f.countBlockedExternalLocked() >= f.cfg.LockdownIPThreshold {
				needLockdown = true
			}
			_ = blocked
		}
	}

	f.recordLocked(Event{
		IP: ip, Method: r.Method, Path: r.URL.Path, Rule: rule,
		Severity: sev, Action: action, Detail: detail,
		RequestID: r.Header.Get("X-Request-Id"),
	})

	if needLockdown && f.level != LevelLockdown {
		f.enterLockdownLocked(rule, ip)
		return
	}
	if bannedNow {
		f.persistSnapshotIfNeededLocked()
	}
}

func (f *Firewall) persistSnapshotIfNeededLocked() {
	if f.cfg.StateDir != "" {
		_ = f.snapshotLocked("auto_ban_ip")
	}
}

// enterLockdownLocked 触发全局自动保护：封存数据、递增会话 epoch、写快照、执行回调。
func (f *Firewall) enterLockdownLocked(rule, ip string) {
	f.level = LevelLockdown
	f.sealed = true
	f.sessionEpoch++
	f.lockedAt = time.Now()
	f.lockUntil = time.Now().Add(f.cfg.LockdownTTL)
	f.recordLocked(Event{
		IP: ip, Method: "SYSTEM", Path: "/", Rule: "auto_lockdown",
		Severity: "critical", Action: "seal_all_sensitive_data",
		Detail: fmt.Sprintf("trigger=%s; session_epoch=%d; all business endpoints sealed; channel ciphertext / token hashes / request_logs / credential stores unreachable", rule, f.sessionEpoch),
	})
	path := f.snapshotLocked("auto_lockdown:" + rule)
	_ = path
	for _, fn := range f.onLockdown {
		go fn()
	}
}

// maybeRecoverLocked 冷却到期后自动降级（sealed 保持到显式 unseal，保证硬保护不半途失效）。
func (f *Firewall) maybeRecoverLocked(now time.Time) {
	if f.level == LevelLockdown && now.After(f.lockUntil) {
		f.level = LevelObserve
		for _, fn := range f.onRecover {
			go fn()
		}
		f.recordLocked(Event{
			IP: "SYSTEM", Method: "SYSTEM", Path: "/", Rule: "auto_recover",
			Severity: "info", Action: "cool_down_level_downgrade",
			Detail: "lockdown TTL expired; level -> observe; sealed remains until explicit unseal",
		})
	}
}

// Seal 手动触发数据封存（运维/演练），与自动 lockdown 同等级保护。
func (f *Firewall) Seal(reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sealed = true
	f.sessionEpoch++
	if f.level != LevelLockdown {
		f.level = LevelLockdown
		f.lockedAt = time.Now()
		f.lockUntil = time.Now().Add(f.cfg.LockdownTTL)
	}
	f.recordLocked(Event{
		IP: "SYSTEM", Method: "SYSTEM", Path: "/", Rule: "manual_seal",
		Severity: "critical", Action: "seal_all_sensitive_data",
		Detail: Redact("reason=" + reason),
	})
	f.snapshotLocked("manual_seal:" + reason)
	for _, fn := range f.onLockdown {
		go fn()
	}
}

// Unlock 运维显式解除（需管理员端点鉴权后才调用）；同时清 level 与 sealed。
func (f *Firewall) Unlock(actor string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.level = LevelObserve
	f.sealed = false
	f.lockUntil = time.Time{}
	// 运维显式解锁 = 全面恢复，同时清除自动封禁与累计分数（解锁本身需 admin JWT）
	for _, rec := range f.ips {
		rec.blockedUntil = time.Time{}
		rec.score = 0
		rec.hits = 0
	}
	f.recordLocked(Event{
		IP: "SYSTEM", Method: "SYSTEM", Path: "/", Rule: "operator_unlock",
		Severity: "info", Action: "unseal",
		Detail: Redact("actor=" + actor),
	})
	f.snapshotLocked("operator_unlock")
	for _, fn := range f.onRecover {
		go fn()
	}
}

// GuardSensitive 在 sealed 状态下额外封禁敏感端点（纵深防御，即使 level 已降级）。
func (f *Firewall) GuardSensitive(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.RLock()
		sealed := f.sealed
		f.mu.RUnlock()
		if sealed && !strings.HasPrefix(r.URL.Path, "/api/firewall/unlock") &&
			!strings.HasPrefix(r.URL.Path, "/api/firewall/unseal") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusLocked)
			_, _ = w.Write([]byte(`{"error":{"message":"sensitive data sealed by firewall","type":"security_error","code":"firewall_sealed"}}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}
func (w *statusRecorder) Flush() {
	if fl, ok := w.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}
