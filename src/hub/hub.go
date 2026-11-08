// Package hub 是团队6对外的公共装配层：把 src/internal/* 的能力以类型别名与
// 薄包装导出，使 tests/（不在 src/ 树内，受 Go internal 规则限制）也能直接
// 对适配器、加解密、SSRF、发现、反代理等做单元测试，并构造可启动的 Server。
package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"llm-hub/src/internal/adapter"
	"llm-hub/src/internal/api"
	"llm-hub/src/internal/auth"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/crypto"
	"llm-hub/src/internal/discovery"
	"llm-hub/src/internal/models"
	"llm-hub/src/internal/relay"
	"llm-hub/src/internal/rt"
	"llm-hub/src/internal/ssrf"
	"llm-hub/src/internal/store"
)

// ---------- 类型别名 ----------

type Config = config.Config
type Store = store.Store
type User = store.User
type Channel = store.Channel
type Model = store.Model
type ModelAlias = store.ModelAlias
type Token = store.Token
type RequestLog = store.RequestLog
type ModelProbe = store.ModelProbe
type UsageDaily = store.UsageDaily
type QuotaLedger = store.QuotaLedger
type Setting = store.Setting
type Counts = store.Counts
type UsageFilter = store.UsageFilter
type LogFilter = store.LogFilter

type Cipher = crypto.Cipher
type Resolver = ssrf.Resolver

type Doer = discovery.Doer
type Discoverer = discovery.Discoverer
type SyncResult = discovery.SyncResult
type ProbeResult = discovery.ProbeResult
type DiscoveredModel = adapter.DiscoveredModel

type ChannelCtx = adapter.ChannelCtx
type ProviderAdapter = adapter.ProviderAdapter
type UnifiedRequest = adapter.UnifiedRequest
type UnifiedResponse = adapter.UnifiedResponse
type UnifiedChunk = adapter.UnifiedChunk
type ChunkToolCall = adapter.ChunkToolCall
type Usage = adapter.Usage
type StreamIterator = adapter.StreamIterator

type RealModel = models.RealModel

type Relay = relay.Relay
type Attempt = relay.Attempt
type Outcome = relay.Outcome

type Service = auth.Service
type Claims = auth.Claims
type Tokens = auth.Tokens

type Server = api.Server

// ---------- config / store ----------

func LoadConfig() *Config { return config.Load() }
func NewStore(dir string) (*Store, error) { return store.New(dir) }
func NewID(prefix string) string { return store.NewID(prefix) }

// ---------- crypto ----------

func NewCipher(masterKeyB64 string) (*Cipher, error) { return crypto.NewCipher(masterKeyB64) }
func KeyHint(s string) string                        { return crypto.KeyHint(s) }
func MaskSecret(s string) string                     { return crypto.MaskSecret(s) }
func Redact(text string, secrets []string) string    { return crypto.Redact(text, secrets) }
func TokenHash(sk string) string                     { return crypto.TokenHash(sk) }
func ConstantTimeEqual(a, b string) bool             { return crypto.ConstantTimeEqual(a, b) }
func HashPassword(pwd string) (string, error)        { return crypto.HashPassword(pwd) }
func VerifyPassword(stored, pwd string) bool         { return crypto.VerifyPassword(stored, pwd) }
func RandomHex(n int) (string, error)                { return crypto.RandomHex(n) }

// ---------- ssrf ----------

func ValidateBaseURI(uri string) error { return ssrf.Validate(uri) }
func ValidateBaseURIWithResolver(uri string, lookup Resolver) error {
	return ssrf.ValidateWithResolver(uri, lookup)
}

// ---------- adapter 双向转换 ----------

func OpenAIToAnthropicRequest(raw []byte) ([]byte, error) { return adapter.OpenAIToAnthropicRequest(raw) }
func AnthropicToOpenAIRequest(raw []byte) ([]byte, error) { return adapter.AnthropicToOpenAIRequest(raw) }
func AnthropicToOpenAIResponse(status int, raw []byte) ([]byte, error) {
	return adapter.AnthropicToOpenAIResponse(status, raw)
}
func OpenAIToAnthropicResponse(status int, raw []byte) ([]byte, error) {
	return adapter.OpenAIToAnthropicResponse(status, raw)
}
func MapStopToFinish(stop string) string   { return adapter.MapStopToFinish(stop) }
func MapFinishToStop(finish string) string { return adapter.MapFinishToStop(finish) }
func AnthropicUsageToOpenAI(in, out int) Usage {
	return adapter.AnthropicUsageToOpenAI(in, out)
}
func OpenAIToolsToAnthropic(raw json.RawMessage) (json.RawMessage, error) {
	return adapter.OpenAIToolsToAnthropic(raw)
}
func AnthropicToolsToOpenAI(raw json.RawMessage) (json.RawMessage, error) {
	return adapter.AnthropicToolsToOpenAI(raw)
}
func CanonicalizeIncoming(entrypoint string, raw []byte) ([]byte, string, error) {
	return adapter.CanonicalizeIncoming(entrypoint, raw)
}
func ParseUsageFromCanonical(raw []byte) Usage { return adapter.ParseUsageFromCanonical(raw) }
func ChunkToOpenAISSE(uc *UnifiedChunk) []byte { return adapter.ChunkToOpenAISSE(uc) }
func ForProvider(p string) ProviderAdapter      { return adapter.ForProvider(p) }

// ---------- 流式状态机迭代器 ----------

func OpenAIStream(r io.Reader) StreamIterator {
	return (&adapter.OpenAICompatAdapter{}).StreamIterator(r)
}
func AnthropicStream(r io.Reader) StreamIterator {
	return (&adapter.AnthropicAdapter{}).StreamIterator(r)
}
func NewAnthropicStreamWriter() *StreamWriterWrapper {
	return &StreamWriterWrapper{w: adapter.NewAnthropicStreamWriter()}
}

// StreamWriterWrapper 包装 Anthropic 流式写出器（统一块 -> Anthropic SSE）。
type StreamWriterWrapper struct{ w *adapter.AnthropicStreamWriter }

func (s *StreamWriterWrapper) Write(uc *UnifiedChunk) []byte { return s.w.Write(uc) }

// ---------- relay / StreamPipe（kernel 接口注入） ----------

func ForwardSSE(ctx context.Context, w http.ResponseWriter, up io.Reader) error {
	var pipe interface{ Forward(context.Context, http.ResponseWriter, io.Reader) error } = rt.NewStreamPipe()
	return pipe.Forward(ctx, w, up)
}
func NewRelay(st *Store, c *Cipher, cfg *Config) *Relay { return relay.New(st, c, cfg) }
func ErrorJSON(errType, code, message, requestID string) []byte {
	return relay.ErrorJSON(errType, code, message, requestID)
}

// ---------- 真实模型池 ----------

func LoadRealModels() ([]RealModel, error) { return models.Load() }
func RealModelIDSet() (map[string]bool, error) { return models.IDSet() }
func OpenAIList() ([]map[string]interface{}, error) { return models.OpenAIList() }
func FindRealModelsFile() (string, error) { return models.FindRealModelsFile() }

// FindRealModelByID 在真实数据集中按 id 精确查找（供 /v1/models/{id} 测试使用）。
func FindRealModelByID(id string) (RealModel, bool, error) { return models.FindByID(id) }

// ---------- discovery ----------

func NewDiscoverer(st *Store, c *Cipher, cfg *Config) *Discoverer {
	return discovery.New(st, c, cfg)
}

// ---------- auth ----------

func SignToken(secret string, c Claims) (string, error) { return auth.SignToken(secret, c) }
func VerifyToken(secret, token string) (*Claims, error) { return auth.VerifyToken(secret, token) }

// ---------- Server 装配 ----------

func NewServer(cfg *Config, st *Store) (*Server, error) { return api.NewServer(cfg, st) }

// NewServerWith 供测试构造：注入假 HTTP 执行器、可放行 SSRF 校验器、同步计量，
// 并停掉后台定时健康检查，保证离线测试确定性。
func NewServerWith(cfg *Config, st *Store, doer Doer, validator func(string) error) (*Server, error) {
	s, err := api.NewServer(cfg, st)
	if err != nil {
		return nil, err
	}
	if doer != nil {
		s.SetDoer(doer)
	}
	if validator != nil {
		s.SetValidator(validator)
	}
	s.SetSynchronousMetering(true)
	if s.Health != nil {
		s.Health.Stop()
	}
	return s, nil
}

// TokenLimitsForTest 读取某明文令牌的无限制字段快照，供 tests/ 断言使用。
// 返回顺序：rpm, tpm, max_concurrency, expires_at，以及配额与白名单长度。
// 天衡不设任何使用限制，这五个值在真实数据里必然全为 0/空。
func TokenLimitsForTest(st *store.Store, plainToken string) (rpm, tpm, conc int, expiresAt int64, quota float64, ipRules, modelRules int, ok bool) {
	t, found := st.GetTokenByHash(crypto.TokenHash(plainToken))
	if !found {
		return 0, 0, 0, 0, 0, 0, 0, false
	}
	return t.RPM, t.TPM, t.MaxConcurrency, t.ExpiresAt, t.QuotaUSD, len(t.IPAllowlist), len(t.AllowedModels), true
}

// ForceTokenLegacyLimitsForTest 把一条令牌改写成"历史遗留的限制状态"
// （超额、过期、带 IP 白名单与模型白名单），用于验证这些限制**不生效**。
// 仅用于测试：生产路径不会调用它。
func ForceTokenLegacyLimitsForTest(st *store.Store, plainToken string) bool {
	return st.UpdateTokenOverride(plainToken, func(t *store.Token) {
		t.UsedUSD = 999999
		t.QuotaUSD = 0.5 // 历史遗留的配额上限
		t.RPM = 1
		t.TPM = 1
		t.MaxConcurrency = 1
		t.ExpiresAt = 1
		t.IPAllowlist = []string{"203.0.113.0/24"}
		t.AllowedModels = []string{"liquid/lfm-3-1.7b"}
	})
}
