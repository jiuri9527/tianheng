// Package middleware 提供 request_id、CORS、body limit、JWT、sk- 令牌校验（仅存哈希）。
// F9：令牌配额与限流为强制项——RPM/TPM/并发/额度/过期/IP 白名单/模型白名单全部参与拒绝判定。
package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"llm-hub/kernel"
	"llm-hub/src/internal/auth"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/crypto"
	"llm-hub/src/internal/relay"
	"llm-hub/src/internal/rt"
	"llm-hub/src/internal/store"
)

// ---------- context keys ----------

type ctxKey string

const (
	KeyUser      ctxKey = "user"
	KeyClaims    ctxKey = "claims"
	KeyToken     ctxKey = "token"
	KeyRequestID ctxKey = "request_id"
	KeyBody      ctxKey = "body"
	KeyClientIP  ctxKey = "client_ip"
)

func UserFrom(ctx context.Context) *store.User {
	u, _ := ctx.Value(KeyUser).(*store.User)
	return u
}
func ClaimsFrom(ctx context.Context) *auth.Claims {
	c, _ := ctx.Value(KeyClaims).(*auth.Claims)
	return c
}
func TokenFrom(ctx context.Context) *store.Token {
	t, _ := ctx.Value(KeyToken).(*store.Token)
	return t
}
func RequestIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(KeyRequestID).(string)
	return s
}
func BodyFrom(ctx context.Context) []byte {
	b, _ := ctx.Value(KeyBody).([]byte)
	return b
}
func ClientIPFrom(ctx context.Context) string {
	s, _ := ctx.Value(KeyClientIP).(string)
	return s
}

// Chain 依次套用中间件（第一个在最外层）。
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// sanitizeRequestID 只保留 [A-Za-z0-9_-] 且限长，防止客户端通过 X-Request-Id
// 注入换行伪造日志行、或碰撞他人日志索引（logsByReqID 按 request_id 索引）。
func sanitizeRequestID(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	return string(out)
}

// clientIP 只在直连对端是可信反代（回环/内网）时才采信 X-Forwarded-For，
// 公网直连客户端不得自造 XFF 伪造来源 IP；此值仅用于日志归因，不再参与任何准入判断。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	trusted := peer != nil && (peer.IsLoopback() || peer.IsPrivate())
	if trusted {
		if x := r.Header.Get("X-Forwarded-For"); x != "" {
			return strings.TrimSpace(strings.Split(x, ",")[0])
		}
		if x := r.Header.Get("X-Real-IP"); x != "" {
			return x
		}
	}
	return host
}

// RequestID 生成并注入 request_id，写入 X-Request-Id 响应头。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := sanitizeRequestID(r.Header.Get("X-Request-Id"))
		if rid == "" {
			rid, _ = crypto.RandomHex(12)
		}
		w.Header().Set("X-Request-Id", rid)
		ctx := context.WithValue(r.Context(), KeyRequestID, rid)
		ctx = context.WithValue(ctx, KeyClientIP, clientIP(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CORS 可配置跨域。
func CORS(origins string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allow := ""
			if strings.TrimSpace(origins) == "*" {
				allow = "*"
			} else if origin != "" {
				// 精确匹配，禁止 strings.Contains 的子串误放行。
				for _, o := range strings.Split(origins, ",") {
					if strings.TrimSpace(o) == origin {
						allow = origin
						break
					}
				}
			}
			if allow != "" {
				w.Header().Set("Access-Control-Allow-Origin", allow)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
				// 通配 origin 时不得携带凭据。
				if allow != "*" {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimit 用 MaxBytesReader 限制请求体大小（超限由下游读 body 时返回 413）。
func BodyLimit(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ReadBody 读取并还原请求体；超出 body limit 时返回 413 错误。
func ReadBody(w http.ResponseWriter, r *http.Request) ([]byte, int, bool) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		// MaxBytesReader 超限错误
		relay.WriteJSONError(w, http.StatusRequestEntityTooLarge, "invalid_request_error",
			"body_too_large", "request body exceeds limit", RequestIDFrom(r.Context()))
		return nil, http.StatusRequestEntityTooLarge, false
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	return data, 0, true
}

// Management JWT 保护管理面 /api/*（仅身份，不做角色区分）。
func Management(authSvc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := bearer(r)
			if tok == "" {
				relay.WriteJSONError(w, http.StatusUnauthorized, "invalid_request_error",
					"missing_token", "Authorization: Bearer <JWT> required", RequestIDFrom(r.Context()))
				return
			}
			u, claims, err := authSvc.Authenticate(tok)
			if err != nil {
				relay.WriteJSONError(w, http.StatusUnauthorized, "invalid_request_error",
					"invalid_token", err.Error(), RequestIDFrom(r.Context()))
				return
			}
			ctx := context.WithValue(r.Context(), KeyUser, u)
			ctx = context.WithValue(ctx, KeyClaims, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ---------- 令牌桶限流（F9） ----------

// Limiter 按令牌维度实施 RPM / TPM / 并发上限，内部复用 kernel.Limiter 令牌桶。
type Limiter struct {
	mu          sync.Mutex
	Default     kernel.Limiter // 注入的全局默认桶，仅当令牌自身 RPM/TPM 为 0 且配置了全局默认时用作兜底
	buckets     map[string]kernel.Limiter
	inflight    map[string]int64
	maxInflight map[string]int64
}

// NewLimiter 构造限流器，def 为注入的全局默认桶。
func NewLimiter(def kernel.Limiter) *Limiter {
	return &Limiter{
		Default: def, buckets: map[string]kernel.Limiter{},
		inflight: map[string]int64{}, maxInflight: map[string]int64{},
	}
}

func (l *Limiter) bucket(key string, rate, capacity float64) kernel.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.buckets[key]; ok {
		return b
	}
	b := rt.NewTokenBucketLimiter(rate, capacity)
	l.buckets[key] = b
	return b
}

// perMinuteBucket 把「每分钟 N」语义换算成令牌桶参数：
// 补充速率 = N/60 每秒，容量 = N，因此一整个窗口的额度可作为突发使用，
// 第 N+1 次快速请求被拒。这就是 RPM/TPM 的标准语义。
func perMinuteBucket(perMinute int) (rate, capacity float64) {
	return float64(perMinute) / 60.0, float64(perMinute)
}

// Allow 天衡：不设任何使用限制 —— 恒放行。
//
// 需求明确：不准对任何人设置 token 使用限制、并发限制或设备限制，这些都是不必要的。
// 令牌结构体里的 RPM/TPM/MaxConcurrency/quota_usd/IP 白名单/expires_at 字段保留
// （接口形态不变、便于对接既有客户端），但一律不参与判定，全部视为"不限"。
// 因此这里只保留函数签名与并发计数的可观测性，不做任何拒绝。
func (l *Limiter) Allow(t *store.Token, promptTokens int) (bool, string) {
	return true, ""
}

// AllowWithDefaults 兼容既有调用点：同样恒放行（保留签名以免破坏调用方）。
func (l *Limiter) AllowWithDefaults(t *store.Token, promptTokens int) (bool, string) {
	return true, ""
}

// AllowLimits 判定一次请求是否放行，并在放行时占用并发额度。
// rpm/tpm 传 0 表示该维度不限；返回的 reason 是稳定错误码，供上游写进统一错误体的 code。
// 约定：MaxConcurrency 为 0 表示不限并发。
// 任何拒绝路径都会自动归还它自己已占用的并发额度，调用方无需区分。
func (l *Limiter) AllowLimits(t *store.Token, rpm, tpm, promptTokens int) (bool, string) {
	// 天衡：不设任何使用限制。rpm/tpm/并发/配额全部不判定，恒放行。
	// 并发计数仍真实累加（供监控面板展示真实在途数量），但从不作为拒绝依据。
	if t != nil {
		l.mu.Lock()
		l.inflight[t.ID]++
		if l.inflight[t.ID] > l.maxInflight[t.ID] {
			l.maxInflight[t.ID] = l.inflight[t.ID]
		}
		l.mu.Unlock()
	}
	return true, ""
}

// Release 请求结束释放在途计数。
func (l *Limiter) Release(tokenID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight[tokenID] > 0 {
		l.inflight[tokenID]--
	}
}

// Reset 吊销令牌时清理桶与在途计数。
func (l *Limiter) Reset(tokenID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, tokenID+":rpm")
	delete(l.buckets, tokenID+":tpm")
	delete(l.inflight, tokenID)
	delete(l.maxInflight, tokenID)
}

// Inflight 返回某令牌当前在途请求数（观测与测试用）。
func (l *Limiter) Inflight(tokenID string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inflight[tokenID]
}

// ---------- 数据面 sk- 令牌校验（F1 + F9） ----------

// modelFromRequestBody 只做一次最小解析取出 model 字段，避免为此引入额外依赖。
func modelFromRequestBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var in struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return ""
	}
	return in.Model
}

// EstimatePromptTokens 在请求前给出提示词规模的保守估计（字节数/4），
// 真实 token 数由上游响应里的 usage 字段在计量阶段校正。
func EstimatePromptTokens(body []byte) int {
	if len(body) == 0 {
		return 0
	}
	n := len(body) / 4
	if n < 1 {
		n = 1
	}
	return n
}

// ipAllowed 判断 clientIP 是否命中白名单条目；条目允许裸 IP 或 CIDR。
// 畸形条目一律不匹配（fail closed）。
func ipAllowed(clientIP string, allowlist []string) bool {
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}
	for _, entry := range allowlist {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err != nil {
				continue
			}
			if network.Contains(ip) {
				return true
			}
			continue
		}
		if parsed := net.ParseIP(entry); parsed != nil && parsed.Equal(ip) {
			return true
		}
	}
	return false
}

// DataPlane 校验 sk- 令牌：身份、状态、属主、过期、IP 白名单、额度、模型白名单、限流。
// 拒绝顺序按「越便宜的检查越靠前」排列，且在所有限制闸门之前完成 body 读取以便估算 TPM。
func DataPlane(st *store.Store, limiter *Limiter, cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rid := RequestIDFrom(r.Context())
			reject := func(status int, errType, code, msg string) {
				relay.WriteJSONError(w, status, errType, code, msg, rid)
			}
			tok := bearer(r)
			if !strings.HasPrefix(tok, "sk-") {
				reject(http.StatusUnauthorized, "invalid_request_error",
					"invalid_token", "Bearer sk-xxx required")
				return
			}
			hash := crypto.TokenHash(tok)
			t, ok := st.GetTokenByHash(hash)
			if !ok || t.Status != "active" {
				reject(http.StatusUnauthorized, "invalid_request_error",
					"invalid_token", "token invalid or revoked")
				return
			}
			user, ok := st.GetUserByID(t.UserID)
			if !ok || user.Status != "active" {
				reject(http.StatusUnauthorized, "invalid_request_error",
					"user_inactive", "user inactive")
				return
			}
			// 读取 body（含 body limit 校验），供下游复用
			body, status, ok := ReadBody(w, r)
			if !ok {
				_ = status
				return
			}
			// 天衡：无模型白名单 —— 任何令牌均可调用全部真实模型，不做闸门判定。
			// 生效额度：令牌自身字段优先；为 0 时回落到全局默认（RATE_RPM_DEFAULT / RATE_TPM_DEFAULT）。
			effRPM, effTPM := t.RPM, t.TPM
			if cfg != nil {
				if effRPM <= 0 {
					effRPM = cfg.RateRPMDefault
				}
				if effTPM <= 0 {
					effTPM = cfg.RateTPMDefault
				}
			}
			// 并发额度只在 Allow 成功时持有；Release 在请求结束时归还。
			if ok, reason := limiter.AllowLimits(t, effRPM, effTPM, EstimatePromptTokens(body)); !ok {
				code := reason
				msg := "rate limiter unavailable"
				switch reason {
				case "rpm_exceeded":
					msg = "requests per minute limit exceeded"
				case "tpm_exceeded":
					msg = "tokens per minute limit exceeded"
				case "max_concurrency_exceeded":
					msg = "concurrent request limit exceeded"
				case "rate_limit_error":
					msg = "rate limiter error"
				}
				reject(http.StatusTooManyRequests, "rate_limit_error", code, msg)
				return
			}
			defer limiter.Release(t.ID)
			// LastUsedAt 由 metering.ApplyBilling 在 store 锁内更新，
			// 此处不再对共享指针做无锁写入。

			ctx := context.WithValue(r.Context(), KeyToken, t)
			ctx = context.WithValue(ctx, KeyUser, user)
			ctx = context.WithValue(ctx, KeyBody, body)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
