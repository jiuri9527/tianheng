// Package store 提供基于 JSON 文件持久化 + 内存索引 + 互斥锁的轻量数据库。
// 覆盖 users/channels/models/model_aliases/tokens/request_logs/model_probes/
// usage_daily/quota_ledger/settings 全部实体，零第三方依赖。
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"llm-hub/src/internal/crypto"
)

// ---------- 实体定义 ----------

// User 注册用户，既是渠道贡献者也是 API 消费者。
type User struct {
	ID           string  `json:"id"`
	Email        string  `json:"email"`
	PasswordHash string  `json:"password_hash"`
	Role         string  `json:"role"` // admin | user
	BalanceUSD   float64 `json:"balance_usd"`
	Status       string  `json:"status"` // active | disabled
	CreatedAt    int64   `json:"created_at"`
	UpdatedAt    int64   `json:"updated_at"`
}

// Channel 一条上游连接。api_key 三段密文落库，永不存明文。
type Channel struct {
	ID              string            `json:"id"`
	OwnerID         string            `json:"owner_id"`
	Name            string            `json:"name"`
	ProviderType    string            `json:"provider_type"` // openai|anthropic|xai|gemini|openai_compatible
	BaseURI         string            `json:"base_uri"`
	ModelsPath      string            `json:"models_path"`
	ChatPath        string            `json:"chat_path"`
	AuthStyle       string            `json:"auth_style"`
	APIVersion      string            `json:"api_version"`
	APIKeyCipher    []byte            `json:"api_key_cipher"`
	APIKeyIV        []byte            `json:"api_key_iv"`
	APIKeyTag       []byte            `json:"api_key_tag"`
	KeyHint         string            `json:"key_hint"`
	CustomHeaders   map[string]string `json:"custom_headers"`
	Visibility      string            `json:"visibility"` // public | private
	Weight          int               `json:"weight"`
	Priority        int               `json:"priority"`
	Status          string            `json:"status"`       // active | disabled
	ProbeStatus     string            `json:"probe_status"` // unavailable | ok | failed
	HealthCheckedAt int64             `json:"health_checked_at"`
	LastError       string            `json:"last_error"`
	FailCount       int               `json:"fail_count"`
	CooldownUntil   int64             `json:"cooldown_until"`
	CreatedAt       int64             `json:"created_at"`
	UpdatedAt       int64             `json:"updated_at"`
}

// Model 从上游真实发现得到的模型；参数只来自真实上游响应。
type Model struct {
	ID                    string          `json:"id"`
	ChannelID             string          `json:"channel_id"`
	UpstreamID            string          `json:"upstream_id"`
	Alias                 string          `json:"alias"`
	DisplayName           string          `json:"display_name"`
	ProviderType          string          `json:"provider_type"`
	ContextLength         int             `json:"context_length"`
	MaxOutputTokens       int             `json:"max_output_tokens"`
	SupportsStream        bool            `json:"supports_stream"`
	SupportsVision        bool            `json:"supports_vision"`
	SupportsTools         bool            `json:"supports_tools"`
	SupportsReasoning     bool            `json:"supports_reasoning"`
	InputPricePerMtok     float64         `json:"input_price_per_mtok"`
	OutputPricePerMtok    float64         `json:"output_price_per_mtok"`
	ParamSource           string          `json:"param_source"` // upstream
	RawJSON               json.RawMessage `json:"raw_json"`
	ProbeStatus           string          `json:"probe_status"` // unavailable | ok | failed
	ProbeInput            json.RawMessage `json:"probe_input"`
	ProbeOutput           string          `json:"probe_output"`
	ProbePromptTokens     int             `json:"probe_prompt_tokens"`
	ProbeCompletionTokens int             `json:"probe_completion_tokens"`
	ProbeLatencyMs        int             `json:"probe_latency_ms"`
	ProbedAt              int64           `json:"probed_at"`
	Enabled               bool            `json:"enabled"`
}

// ModelAlias 全局池对外暴露的统一名。
type ModelAlias struct {
	Alias            string          `json:"alias"`
	CanonicalModelID string          `json:"canonical_model_id"`
	Description      string          `json:"description"`
	Enabled          bool            `json:"enabled"`
	PriceOverride    json.RawMessage `json:"price_override"`
	CreatedAt        int64           `json:"created_at"`
}

// Token 平台令牌，仅存 sha256 哈希。
type Token struct {
	ID             string   `json:"id"`
	UserID         string   `json:"user_id"`
	Name           string   `json:"name"`
	TokenHash      string   `json:"token_hash"`
	KeyHint        string   `json:"key_hint"`
	AllowedModels  []string `json:"allowed_models"`
	RPM            int      `json:"rpm"`
	TPM            int      `json:"tpm"`
	MaxConcurrency int      `json:"max_concurrency"`
	QuotaUSD       float64  `json:"quota_usd"`
	UsedUSD        float64  `json:"used_usd"`
	IPAllowlist    []string `json:"ip_allowlist"`
	ExpiresAt      int64    `json:"expires_at"`
	Status         string   `json:"status"` // active | revoked
	LastUsedAt     int64    `json:"last_used_at"`
	CreatedAt      int64    `json:"created_at"`
}

// RequestLog 每次调用完整记录。
type RequestLog struct {
	ID               string          `json:"id"`
	RequestID        string          `json:"request_id"`
	UserID           string          `json:"user_id"`
	TokenID          string          `json:"token_id"`
	Alias            string          `json:"alias"`
	ModelID          string          `json:"model_id"`
	ChannelID        string          `json:"channel_id"`
	ProviderType     string          `json:"provider_type"`
	Stream           bool            `json:"stream"`
	RequestBody      json.RawMessage `json:"request_body"`
	ResponseBody     string          `json:"response_body"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	TTFTMs           int             `json:"ttft_ms"`
	LatencyMs        int             `json:"latency_ms"`
	StatusCode       int             `json:"status_code"`
	ErrorCode        string          `json:"error_code"`
	CostUSD          float64         `json:"cost_usd"`
	ClientIP         string          `json:"client_ip"`
	CreatedAt        int64           `json:"created_at"`
}

// ModelProbe 一次真实探测记录。
type ModelProbe struct {
	ID        string          `json:"id"`
	ModelID   string          `json:"model_id"`
	Input     json.RawMessage `json:"input"`
	Output    string          `json:"output"`
	LatencyMs int             `json:"latency_ms"`
	OK        bool            `json:"ok"`
	Error     string          `json:"error"`
	CreatedAt int64           `json:"created_at"`
}

// UsageDaily 按日/用户/别名聚合。
type UsageDaily struct {
	Day              string  `json:"day"`
	UserID           string  `json:"user_id"`
	Alias            string  `json:"alias"`
	Requests         int     `json:"requests"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

// QuotaLedger 额度流水。
type QuotaLedger struct {
	ID        string  `json:"id"`
	UserID    string  `json:"user_id"`
	DeltaUSD  float64 `json:"delta_usd"`
	Reason    string  `json:"reason"` // contribution_reward | topup | consumption | revoke
	RefID     string  `json:"ref_id"`
	CreatedAt int64   `json:"created_at"`
}

// Setting 系统设置。
type Setting struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt int64           `json:"updated_at"`
}

// Database 是所有表的持久化容器，切片元素为指针以保证内存索引稳定。
type Database struct {
	Users       []*User        `json:"users"`
	Channels    []*Channel     `json:"channels"`
	Models      []*Model       `json:"models"`
	Aliases     []*ModelAlias  `json:"model_aliases"`
	Tokens      []*Token       `json:"tokens"`
	RequestLogs []*RequestLog  `json:"request_logs"`
	ModelProbes []*ModelProbe  `json:"model_probes"`
	UsageDaily  []*UsageDaily  `json:"usage_daily"`
	QuotaLedger []*QuotaLedger `json:"quota_ledger"`
	Settings    []*Setting     `json:"settings"`
}

// Store 持有数据库、内存索引与互斥锁。
type Store struct {
	mu      sync.RWMutex // 保护 db 与全部内存索引
	writeMu sync.Mutex   // 串行化所有落盘写路径（WAL/表文件/完整性清单/备份/轮转）
	dir     string       // 为空表示纯内存（不落盘）
	db      Database
	// 完整性链 / WAL / 备份的进程内状态（由 writeMu 保护）
	manifest integrityManifest
	opts     Options
	status   IntegrityStatus
	// 内存索引
	usersByID    map[string]*User
	usersByEmail map[string]*User
	channelsByID map[string]*Channel
	modelsByID   map[string]*Model
	modelByCU    map[string]*Model // channelID + "|" + upstreamID
	aliasByName  map[string]*ModelAlias
	tokensByID   map[string]*Token
	tokensByHash map[string]*Token
	logsByReqID  map[string]*RequestLog
	usageByKey   map[string]*UsageDaily
	settingByKey map[string]*Setting
	ledgerRefs   map[string]bool // reason|refID -> 已记账（贡献奖励幂等）
}

func newDatabase() Database {
	return Database{
		Users:       []*User{},
		Channels:    []*Channel{},
		Models:      []*Model{},
		Aliases:     []*ModelAlias{},
		Tokens:      []*Token{},
		RequestLogs: []*RequestLog{},
		ModelProbes: []*ModelProbe{},
		UsageDaily:  []*UsageDaily{},
		QuotaLedger: []*QuotaLedger{},
		Settings:    []*Setting{},
	}
}

// NewID 生成带前缀的唯一 id。
func NewID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// New 打开/创建存储。dir 为空时纯内存；非空时从该目录加载已有 JSON 文件。
// 签名保持不变，内部委托 NewWithOptions 完成权限加固、完整性链校验与 WAL 崩溃恢复。
func New(dir string) (*Store, error) {
	return NewWithOptions(dir, DefaultOptions())
}

var tableNames = []string{
	"users", "channels", "models", "model_aliases", "tokens",
	"request_logs", "model_probes", "usage_daily", "quota_ledger", "settings",
}

func (s *Store) tablePtr(name string) interface{} {
	switch name {
	case "users":
		return &s.db.Users
	case "channels":
		return &s.db.Channels
	case "models":
		return &s.db.Models
	case "model_aliases":
		return &s.db.Aliases
	case "tokens":
		return &s.db.Tokens
	case "request_logs":
		return &s.db.RequestLogs
	case "model_probes":
		return &s.db.ModelProbes
	case "usage_daily":
		return &s.db.UsageDaily
	case "quota_ledger":
		return &s.db.QuotaLedger
	case "settings":
		return &s.db.Settings
	}
	return nil
}

func (s *Store) load() error {
	for _, name := range tableNames {
		p := filepath.Join(s.dir, name+".json")
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		if err := json.Unmarshal(data, s.tablePtr(name)); err != nil {
			return fmt.Errorf("加载 %s 失败: %w", p, err)
		}
	}
	return nil
}

// Save 把所有表逐文件落盘（原子写：临时文件 → fsync → rename），
// 并追加 WAL、推进 SHA-256 完整性链、轮转备份。签名保持不变。
func (s *Store) Save() error {
	if s.dir == "" {
		return nil
	}
	return s.saveHardened()
}

func (s *Store) rebuild() {
	s.usersByID = map[string]*User{}
	s.usersByEmail = map[string]*User{}
	s.channelsByID = map[string]*Channel{}
	s.modelsByID = map[string]*Model{}
	s.modelByCU = map[string]*Model{}
	s.aliasByName = map[string]*ModelAlias{}
	s.tokensByID = map[string]*Token{}
	s.tokensByHash = map[string]*Token{}
	s.logsByReqID = map[string]*RequestLog{}
	s.usageByKey = map[string]*UsageDaily{}
	s.settingByKey = map[string]*Setting{}
	for _, u := range s.db.Users {
		s.usersByID[u.ID] = u
		s.usersByEmail[strings.ToLower(u.Email)] = u
	}
	for _, c := range s.db.Channels {
		s.channelsByID[c.ID] = c
	}
	for _, m := range s.db.Models {
		s.modelsByID[m.ID] = m
		s.modelByCU[m.ChannelID+"|"+m.UpstreamID] = m
	}
	for _, a := range s.db.Aliases {
		s.aliasByName[a.Alias] = a
	}
	for _, t := range s.db.Tokens {
		s.tokensByID[t.ID] = t
		s.tokensByHash[t.TokenHash] = t
	}
	for _, l := range s.db.RequestLogs {
		s.logsByReqID[l.RequestID] = l
	}
	for _, u := range s.db.UsageDaily {
		s.usageByKey[usageKey(u.Day, u.UserID, u.Alias)] = u
	}
	for _, st := range s.db.Settings {
		s.settingByKey[st.Key] = st
	}
	s.ledgerRefs = map[string]bool{}
	for _, l := range s.db.QuotaLedger {
		if l.RefID != "" {
			s.ledgerRefs[l.Reason+"|"+l.RefID] = true
		}
	}
}

func (s *Store) ensureDefaults() {
	s.mu.Lock()
	defer s.mu.Unlock()
	defaults := map[string]interface{}{
		"default_timeout_sec":     180,
		"max_failover":            3,
		"circuit_cooldown_sec":    30,
		"contributor_reward_rate": 0.15,
		"registration_open":       true,
		"log_mask_keys":           true,
		"probe_enabled":           true,
	}
	for k, v := range defaults {
		if _, ok := s.settingByKey[k]; !ok {
			raw, _ := json.Marshal(v)
			st := &Setting{Key: k, Value: raw, UpdatedAt: time.Now().Unix()}
			s.db.Settings = append(s.db.Settings, st)
			s.settingByKey[k] = st
		}
	}
}

// ---------- Users ----------

func (s *Store) CreateUser(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.usersByEmail[strings.ToLower(u.Email)]; ok {
		return fmt.Errorf("邮箱已存在")
	}
	s.db.Users = append(s.db.Users, u)
	s.usersByID[u.ID] = u
	s.usersByEmail[strings.ToLower(u.Email)] = u
	return nil
}

func (s *Store) GetUserByID(id string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.usersByID[id]
	return u, ok
}

func (s *Store) GetUserByEmail(email string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.usersByEmail[strings.ToLower(email)]
	return u, ok
}

// NormalizeRolesToSuperuser 把历史数据里残留的 admin/user 角色统一改写为
// superuser，返回被改写的账号数。天衡没有角色区分（所有用户即超级用户），
// 这一步只是让存量数据与新语义一致，不改变任何人的实际权限。
func (s *Store) NormalizeRolesToSuperuser() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, u := range s.db.Users {
		if u != nil && u.Role != "superuser" {
			u.Role = "superuser"
			u.UpdatedAt = time.Now().Unix()
			n++
		}
	}
	return n
}

func (s *Store) UpdateUser(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.usersByID[u.ID]; !ok {
		return fmt.Errorf("用户不存在")
	}
	return nil // 原地修改指针已生效
}

func (s *Store) ListUsers() []*User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*User, len(s.db.Users))
	copy(out, s.db.Users)
	return out
}

func (s *Store) CountUsers() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.db.Users)
}

// CountRole 统计指定角色的用户数，用于「最后一个 superuser 不可被降权」这类 fail-closed 判定。
func (s *Store) CountRole(role string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, u := range s.db.Users {
		if u.Role == role {
			n++
		}
	}
	return n
}

// ---------- Channels ----------

func (s *Store) CreateChannel(c *Channel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db.Channels = append(s.db.Channels, c)
	s.channelsByID[c.ID] = c
}

func (s *Store) GetChannel(id string) (*Channel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.channelsByID[id]
	return c, ok
}

func (s *Store) UpdateChannel(c *Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.channelsByID[c.ID]; !ok {
		return fmt.Errorf("渠道不存在")
	}
	return nil
}

func (s *Store) DeleteChannel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.channelsByID, id)
	kept := s.db.Channels[:0]
	for _, c := range s.db.Channels {
		if c.ID != id {
			kept = append(kept, c)
		}
	}
	s.db.Channels = kept
	// 同步删除该渠道下的模型与别名引用
	mkept := s.db.Models[:0]
	for _, m := range s.db.Models {
		if m.ChannelID == id {
			delete(s.modelsByID, m.ID)
			delete(s.modelByCU, m.ChannelID+"|"+m.UpstreamID)
			continue
		}
		mkept = append(mkept, m)
	}
	s.db.Models = mkept
}

func (s *Store) ListChannels() []*Channel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Channel, len(s.db.Channels))
	copy(out, s.db.Channels)
	return out
}

// ListChannelsByOwner 返回某用户的渠道。
func (s *Store) ListChannelsByOwner(ownerID string) []*Channel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*Channel{}
	for _, c := range s.db.Channels {
		if c.OwnerID == ownerID {
			out = append(out, c)
		}
	}
	return out
}

// ---------- Models ----------

// UpsertModel 按 (channel_id, upstream_id) 插入或更新模型。
// 重新同步时若传入模型尚未探测（ProbedAt==0），保留已有真实探测结果不被清空。
func (s *Store) UpsertModel(m *Model) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := m.ChannelID + "|" + m.UpstreamID
	if existing, ok := s.modelByCU[key]; ok {
		id := existing.ID
		keepProbe := m.ProbedAt == 0
		var probe Model
		if keepProbe {
			probe = *existing
		}
		*existing = *m
		existing.ID = id
		if keepProbe {
			existing.ProbeStatus = probe.ProbeStatus
			existing.ProbeInput = probe.ProbeInput
			existing.ProbeOutput = probe.ProbeOutput
			existing.ProbePromptTokens = probe.ProbePromptTokens
			existing.ProbeCompletionTokens = probe.ProbeCompletionTokens
			existing.ProbeLatencyMs = probe.ProbeLatencyMs
			existing.ProbedAt = probe.ProbedAt
		}
		return
	}
	if m.ID == "" {
		m.ID = NewID("mdl")
	}
	s.db.Models = append(s.db.Models, m)
	s.modelsByID[m.ID] = m
	s.modelByCU[key] = m
}

func (s *Store) GetModel(id string) (*Model, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.modelsByID[id]
	return m, ok
}

// ListModels 返回全部模型的快照副本（避免调用方直接改内存对象）。
func (s *Store) ListModels() []*Model {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Model, 0, len(s.db.Models))
	for _, m := range s.db.Models {
		cp := *m
		out = append(out, &cp)
	}
	return out
}

// ListModelsByChannel 返回某渠道下的模型快照。
func (s *Store) ListModelsByChannel(channelID string) []*Model {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*Model{}
	for _, m := range s.db.Models {
		if m.ChannelID == channelID {
			cp := *m
			out = append(out, &cp)
		}
	}
	return out
}

// UpdateModel 原地更新一个模型（调用方需先 GetModel）。
func (s *Store) UpdateModel(id string, mutate func(*Model)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.modelsByID[id]
	if !ok {
		return false
	}
	mutate(m)
	return true
}

// DeleteModelsByChannel 删除某渠道全部模型（同步前清理用）。
func (s *Store) DeleteModelsByChannel(channelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.db.Models[:0]
	for _, m := range s.db.Models {
		if m.ChannelID == channelID {
			delete(s.modelsByID, m.ID)
			delete(s.modelByCU, m.ChannelID+"|"+m.UpstreamID)
			continue
		}
		kept = append(kept, m)
	}
	s.db.Models = kept
}

// ---------- Aliases ----------

func (s *Store) UpsertAlias(a *ModelAlias) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.aliasByName[a.Alias]; !ok {
		s.db.Aliases = append(s.db.Aliases, a)
	}
	s.aliasByName[a.Alias] = a
}

func (s *Store) GetAlias(name string) (*ModelAlias, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.aliasByName[name]
	return a, ok
}

func (s *Store) ListAliases() []*ModelAlias {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*ModelAlias, len(s.db.Aliases))
	copy(out, s.db.Aliases)
	return out
}

func (s *Store) DeleteAlias(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.aliasByName, name)
	kept := s.db.Aliases[:0]
	for _, a := range s.db.Aliases {
		if a.Alias != name {
			kept = append(kept, a)
		}
	}
	s.db.Aliases = kept
}

// ---------- Tokens ----------

func (s *Store) CreateToken(t *Token) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db.Tokens = append(s.db.Tokens, t)
	s.tokensByID[t.ID] = t
	s.tokensByHash[t.TokenHash] = t
}

// RotateToken 原子替换令牌哈希并重建哈希索引：旧 sk- 立即失效。
// 返回更新后的令牌；id 不存在时返回 false。
// UpdateTokenOverride 按令牌明文查询并就地改写其记录（仅供测试构造边界状态）。
// 生产路径不应依赖它来做业务判定。
func (s *Store) UpdateTokenOverride(plainToken string, mutate func(*Token)) bool {
	hash := crypto.TokenHash(plainToken)
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokensByHash[hash]
	if !ok {
		return false
	}
	if mutate != nil {
		mutate(t)
	}
	return true
}

func (s *Store) RotateToken(id, newHash, newHint string) (*Token, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokensByID[id]
	if !ok {
		return nil, false
	}
	if t.TokenHash != "" {
		delete(s.tokensByHash, t.TokenHash)
	}
	t.TokenHash = newHash
	t.KeyHint = newHint
	t.LastUsedAt = 0
	s.tokensByHash[newHash] = t
	return t, true
}

func (s *Store) GetTokenByHash(hash string) (*Token, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tokensByHash[hash]
	return t, ok
}

func (s *Store) GetTokenByID(id string) (*Token, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tokensByID[id]
	return t, ok
}

func (s *Store) ListTokensByUser(userID string) []*Token {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*Token{}
	for _, t := range s.db.Tokens {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	return out
}

func (s *Store) ListTokens() []*Token {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Token, len(s.db.Tokens))
	copy(out, s.db.Tokens)
	return out
}

func (s *Store) DeleteToken(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tokensByID[id]; ok {
		delete(s.tokensByHash, t.TokenHash)
	}
	delete(s.tokensByID, id)
	kept := s.db.Tokens[:0]
	for _, t := range s.db.Tokens {
		if t.ID != id {
			kept = append(kept, t)
		}
	}
	s.db.Tokens = kept
}

// AdjustQuota 原子增减用户余额并写一条流水。
func (s *Store) AdjustQuota(userID string, delta float64, reason, refID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 拒绝非有限/异常量级金额：NaN/Inf 会污染余额并使所有额度比较永久失效。
	if math.IsNaN(delta) || math.IsInf(delta, 0) || math.Abs(delta) > 1e12 {
		return
	}
	// 贡献奖励按 (reason, ref_id) 幂等，防止重试/并发重复返还造成双花。
	if reason == "contribution_reward" && refID != "" {
		k := reason + "|" + refID
		if s.ledgerRefs[k] {
			return
		}
		s.ledgerRefs[k] = true
	}
	if u, ok := s.usersByID[userID]; ok {
		u.BalanceUSD += delta
		u.UpdatedAt = time.Now().Unix()
	}
	entry := &QuotaLedger{
		ID: NewID("ldg"), UserID: userID, DeltaUSD: delta,
		Reason: reason, RefID: refID, CreatedAt: time.Now().Unix(),
	}
	s.db.QuotaLedger = append(s.db.QuotaLedger, entry)
}

// ApplyBilling 在 store 锁内原子执行令牌已用额度、用户余额、贡献奖励返还，
// 替代 metering 对共享指针的无锁写入，消除并发丢更新/双花。
func (s *Store) ApplyBilling(tokenID, userID, contributorID string, cost, reward float64, refID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost <= 0 || math.IsNaN(reward) || math.IsInf(reward, 0) {
		return false
	}
	t := s.tokensByID[tokenID]
	u := s.usersByID[userID]
	if t != nil && t.QuotaUSD > 0 && t.UsedUSD+cost > t.QuotaUSD {
		return false
	}
	if u != nil && u.BalanceUSD < cost {
		return false
	}
	now := time.Now().Unix()
	if t != nil {
		t.UsedUSD += cost
		t.LastUsedAt = now
	}
	if u != nil {
		u.BalanceUSD -= cost
		u.UpdatedAt = now
		s.db.QuotaLedger = append(s.db.QuotaLedger, &QuotaLedger{
			ID: NewID("ldg"), UserID: userID, DeltaUSD: -cost,
			Reason: "consumption", RefID: refID, CreatedAt: now,
		})
	}
	if reward > 0 && contributorID != "" && contributorID != userID {
		k := "contribution_reward|" + refID
		if refID != "" && s.ledgerRefs[k] {
			return true
		}
		if refID != "" {
			s.ledgerRefs[k] = true
		}
		if cu, ok := s.usersByID[contributorID]; ok {
			cu.BalanceUSD += reward
			cu.UpdatedAt = now
		}
		s.db.QuotaLedger = append(s.db.QuotaLedger, &QuotaLedger{
			ID: NewID("ldg"), UserID: contributorID, DeltaUSD: reward,
			Reason: "contribution_reward", RefID: refID, CreatedAt: now,
		})
	}
	return true
}

// UpdateChannelLocked 在 store 锁内修改渠道，消除对内部指针的无锁写入竞态。
func (s *Store) UpdateChannelLocked(id string, mutate func(*Channel)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.channelsByID[id]
	if !ok {
		return fmt.Errorf("渠道不存在")
	}
	mutate(c)
	return nil
}

// SetPasswordHash 在锁内更新口令哈希，避免对内部指针的无锁写入造成数据竞争。
func (s *Store) SetPasswordHash(userID, hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.usersByID[userID]
	if !ok {
		return false
	}
	u.PasswordHash = hash
	u.UpdatedAt = time.Now().Unix()
	return true
}

func (s *Store) ListLedgerByUser(userID string) []*QuotaLedger {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*QuotaLedger{}
	for _, l := range s.db.QuotaLedger {
		if l.UserID == userID {
			out = append(out, l)
		}
	}
	return out
}

// ---------- RequestLogs ----------

func (s *Store) AddRequestLog(l *RequestLog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.ID == "" {
		l.ID = NewID("log")
	}
	s.db.RequestLogs = append(s.db.RequestLogs, l)
	s.logsByReqID[l.RequestID] = l
}

// LogFilter 日志检索条件。
type LogFilter struct {
	RequestID string
	UserID    string
	Alias     string
	Status    int
	Keyword   string
	Page      int
	PageSize  int
}

func (s *Store) ListRequestLogs(f LogFilter) ([]*RequestLog, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := []*RequestLog{}
	for i := len(s.db.RequestLogs) - 1; i >= 0; i-- {
		l := s.db.RequestLogs[i]
		if f.RequestID != "" && l.RequestID != f.RequestID {
			continue
		}
		if f.UserID != "" && l.UserID != f.UserID {
			continue
		}
		if f.Alias != "" && l.Alias != f.Alias {
			continue
		}
		if f.Status != 0 && l.StatusCode != f.Status {
			continue
		}
		if f.Keyword != "" && !strings.Contains(l.ResponseBody, f.Keyword) && !strings.Contains(string(l.RequestBody), f.Keyword) {
			continue
		}
		matched = append(matched, l)
	}
	total := len(matched)
	if f.PageSize <= 0 {
		f.PageSize = 50
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	// 钳制 page 上限，避免 (f.Page-1)*f.PageSize 整数溢出为负导致切片 panic；
	// PageSize 不设小上限，以保留管理面仪表盘一次性聚合全部日志的能力。
	if f.Page > 1000000 {
		f.Page = 1000000
	}
	start := (f.Page - 1) * f.PageSize
	if start > total {
		start = total
	}
	end := start + f.PageSize
	if end > total {
		end = total
	}
	return matched[start:end], total
}

func (s *Store) GetLogByRequestID(id string) (*RequestLog, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.logsByReqID[id]
	return l, ok
}

// ---------- ModelProbes ----------

func (s *Store) AddProbe(p *ModelProbe) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.ID == "" {
		p.ID = NewID("prb")
	}
	s.db.ModelProbes = append(s.db.ModelProbes, p)
}

func (s *Store) ListProbesByModel(modelID string) []*ModelProbe {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*ModelProbe{}
	for _, p := range s.db.ModelProbes {
		if p.ModelID == modelID {
			out = append(out, p)
		}
	}
	return out
}

// ---------- UsageDaily ----------

func usageKey(day, userID, alias string) string { return day + "|" + userID + "|" + alias }

func (s *Store) AddUsage(day, userID, alias string, requests, promptTokens, completionTokens int, costUSD float64) {
	// 上游 usage 不可信：非法聚合值不得入库（NaN 会让后续 json.Marshal 永久失败）。
	if math.IsNaN(costUSD) || math.IsInf(costUSD, 0) || costUSD < 0 || requests < 0 ||
		promptTokens < 0 || completionTokens < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := usageKey(day, userID, alias)
	if u, ok := s.usageByKey[k]; ok {
		u.Requests += requests
		u.PromptTokens += promptTokens
		u.CompletionTokens += completionTokens
		u.CostUSD += costUSD
		return
	}
	u := &UsageDaily{Day: day, UserID: userID, Alias: alias, Requests: requests,
		PromptTokens: promptTokens, CompletionTokens: completionTokens, CostUSD: costUSD}
	s.db.UsageDaily = append(s.db.UsageDaily, u)
	s.usageByKey[k] = u
}

// UsageFilter 用量聚合查询条件。
type UsageFilter struct {
	From    string
	To      string
	UserID  string
	GroupBy string // day | alias | model | channel
}

func (s *Store) ListUsage(f UsageFilter) []*UsageDaily {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*UsageDaily{}
	for _, u := range s.db.UsageDaily {
		if f.UserID != "" && u.UserID != f.UserID {
			continue
		}
		if f.From != "" && u.Day < f.From {
			continue
		}
		if f.To != "" && u.Day > f.To {
			continue
		}
		cp := *u
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].Alias < out[j].Alias
	})
	return out
}

// ---------- Settings ----------

func (s *Store) GetSetting(key string) (json.RawMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.settingByKey[key]
	if !ok {
		return nil, false
	}
	cp := make(json.RawMessage, len(st.Value))
	copy(cp, st.Value)
	return cp, true
}

func (s *Store) SetSetting(key string, value json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.settingByKey[key]; ok {
		st.Value = value
		st.UpdatedAt = time.Now().Unix()
		return
	}
	st := &Setting{Key: key, Value: value, UpdatedAt: time.Now().Unix()}
	s.db.Settings = append(s.db.Settings, st)
	s.settingByKey[key] = st
}

func (s *Store) ListSettings() []*Setting {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Setting, len(s.db.Settings))
	copy(out, s.db.Settings)
	return out
}

// ---------- 概览 ----------

// Counts 返回关键计数快照，用于 /metrics 与仪表盘。
type Counts struct {
	Users    int
	Channels int
	Models   int
	Aliases  int
	Tokens   int
	Logs     int
	Probes   int
}

func (s *Store) Counts() Counts {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Counts{
		Users:    len(s.db.Users),
		Channels: len(s.db.Channels),
		Models:   len(s.db.Models),
		Aliases:  len(s.db.Aliases),
		Tokens:   len(s.db.Tokens),
		Logs:     len(s.db.RequestLogs),
		Probes:   len(s.db.ModelProbes),
	}
}
