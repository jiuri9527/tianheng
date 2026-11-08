// Package channel 实现 F2 渠道贡献与管理：三步校验（SSRF / 连接测试 / AES 加密）、
// CRUD、启停、手动同步、手动探测。api_key 只以密文落库，接口只回 key_hint。
package channel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"llm-hub/src/internal/adapter"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/crypto"
	"llm-hub/src/internal/discovery"
	"llm-hub/src/internal/ssrf"
	"llm-hub/src/internal/store"
)

// Input 是新增/编辑渠道的请求字段。全部使用 SPEC §9 的 snake_case 契约标签。
type Input struct {
	Name          string            `json:"name"`
	ProviderType  string            `json:"provider_type"`
	BaseURI       string            `json:"base_uri"`
	APIKey        string            `json:"api_key"` // 编辑时为空表示不更换密钥
	Visibility    string            `json:"visibility"`
	Weight        int               `json:"weight"`
	Priority      int               `json:"priority"`
	CustomHeaders map[string]string `json:"custom_headers"`
	ModelsPath    string            `json:"models_path"`
	ChatPath      string            `json:"chat_path"`
	AuthStyle     string            `json:"auth_style"`
	APIVersion    string            `json:"api_version"`
}

// CreateResult 创建渠道结果。
type CreateResult struct {
	Channel          *store.Channel
	ModelsDiscovered int
	ConnectionOK     bool
	ConnectionError  string
	AutoProbeStarted bool
}

// Service 渠道服务。
type Service struct {
	St          *store.Store
	Cipher      *crypto.Cipher
	Discoverer  *discovery.Discoverer
	Validate    func(string) error
	Cfg         *config.Config
	autoRefresh *discovery.AutoRefresher
}

// New 构造渠道服务，默认 SSRF 校验器为 ssrf.Validate；
// 同时启动定时真实模型刷新（MODEL_AUTO_REFRESH_SEC，默认 6h）。
func New(st *store.Store, cipher *crypto.Cipher, disc *discovery.Discoverer, cfg *config.Config) *Service {
	s := &Service{St: st, Cipher: cipher, Discoverer: disc, Validate: ssrf.Validate, Cfg: cfg}
	if disc != nil {
		s.autoRefresh = discovery.NewAutoRefresher(disc, discovery.RefreshInterval())
		s.autoRefresh.Start()
	}
	return s
}

// Stop 停止定时刷新（供装配层关闭时调用）。
func (s *Service) Stop() {
	if s.autoRefresh != nil {
		s.autoRefresh.Stop()
	}
}

// RefreshNow 立即对所有 active 渠道做一轮真实同步（供运维/测试调用）。
func (s *Service) RefreshNow(ctx context.Context) (channels, discovered int) {
	if s.Discoverer == nil {
		return 0, 0
	}
	return discovery.NewAutoRefresher(s.Discoverer, discovery.RefreshInterval()).RefreshAll(ctx)
}

// probeEnabled 读取 F4 真实探测开关。
func (s *Service) probeEnabled() bool {
	return s.Cfg == nil || s.Cfg.ProbeEnabled
}

// SetValidator 注入自定义 SSRF 校验器（离线测试用）。
func (s *Service) SetValidator(v func(string) error) *Service {
	s.Validate = v
	return s
}

func normalizeInput(in *Input) error {
	in.Name = strings.TrimSpace(in.Name)
	in.ProviderType = strings.TrimSpace(in.ProviderType)
	in.BaseURI = strings.TrimRight(strings.TrimSpace(in.BaseURI), "/")
	if in.Name == "" {
		// 用户只填 provider_type/base_uri/api_key/visibility 时自动生成渠道名，
		// 站点自动发现全部真实模型，用户不需要填写任何模型名。
		in.Name = in.ProviderType + " 自动发现渠道"
	}
	if in.ProviderType == "" {
		return errors.New("provider_type 必填")
	}
	if in.BaseURI == "" {
		return errors.New("base_uri 必填")
	}
	if in.Visibility == "" {
		in.Visibility = "private"
	}
	if in.Visibility != "public" && in.Visibility != "private" {
		return errors.New("visibility 只能是 public|private")
	}
	if in.Weight <= 0 {
		in.Weight = 1
	}
	if in.AuthStyle == "" {
		in.AuthStyle = adapter.AuthStyle(in.ProviderType)
	}
	if in.APIVersion == "" && in.ProviderType == "anthropic" {
		in.APIVersion = adapter.AnthropicVersion
	}
	return nil
}

func (s *Service) encryptKey(apiKey string) (cipherText, iv, tag []byte, hint string, err error) {
	cipherText, iv, tag, err = s.Cipher.Encrypt([]byte(apiKey))
	if err != nil {
		return nil, nil, nil, "", err
	}
	return cipherText, iv, tag, crypto.KeyHint(apiKey), nil
}

// Create 三步校验后创建渠道：① SSRF ② 真实上游 /models 验证 ③ AES-256-GCM 加密落库。
// 上游不可达时不阻塞创建，模型列表保持为空并标 probe_status=unavailable，绝不编造模型。
func (s *Service) Create(ctx context.Context, ownerID string, in Input) (*CreateResult, error) {
	if err := normalizeInput(&in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.APIKey) == "" {
		return nil, errors.New("api_key 必填")
	}
	if s.Validate != nil {
		if err := s.Validate(in.BaseURI); err != nil {
			return nil, fmt.Errorf("base_uri 校验失败: %w", err)
		}
	}
	cipherText, iv, tag, hint, err := s.encryptKey(in.APIKey)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	ch := &store.Channel{
		ID: store.NewID("chn"), OwnerID: ownerID, Name: in.Name,
		ProviderType: in.ProviderType, BaseURI: in.BaseURI,
		ModelsPath: in.ModelsPath, ChatPath: in.ChatPath,
		AuthStyle: in.AuthStyle, APIVersion: in.APIVersion,
		APIKeyCipher: cipherText, APIKeyIV: iv, APIKeyTag: tag, KeyHint: hint,
		CustomHeaders: in.CustomHeaders, Visibility: in.Visibility,
		Weight: in.Weight, Priority: in.Priority,
		Status: "active", ProbeStatus: "unavailable",
		CreatedAt: now, UpdatedAt: now,
	}
	s.St.CreateChannel(ch)

	res := &CreateResult{Channel: ch}
	// ② 提交成功后自动用该 key 请求真实上游 GET /models，解析全部真实 id/参数/能力位/价格，
	//    并后台对每条新模型做一次真实极小探测采集输入/输出样例（F3+F4）。
	//    同步失败不阻塞创建：models_discovered=0 且渠道 probe_status=unavailable，绝不编造。
	if s.Discoverer != nil {
		ctxTimeout, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		auto := s.Discoverer.AutoDiscover(ctxTimeout, ch, discovery.AutoDiscoverOptions{Probe: s.probeEnabled()})
		res.ConnectionOK = auto.OK
		res.ConnectionError = auto.Error
		res.ModelsDiscovered = auto.Discovered
		res.AutoProbeStarted = auto.ProbeStarted
	}
	return res, nil
}

// Get 取渠道。
func (s *Service) Get(id string) (*store.Channel, bool) { return s.St.GetChannel(id) }

// List 列出渠道；ownerID 非空时只看该用户的渠道。
func (s *Service) List(ownerID string) []*store.Channel {
	if ownerID == "" {
		return s.St.ListChannels()
	}
	return s.St.ListChannelsByOwner(ownerID)
}

// Update 编辑渠道。api_key 为空则保留原密文；base_uri 变化需重新做 SSRF 校验。
// 更新成功后自动重新用该 key 拉取真实 /models 并补齐真实探测样例。
func (s *Service) Update(id string, in Input) (*store.Channel, error) {
	ch, ok := s.St.GetChannel(id)
	if !ok {
		return nil, errors.New("渠道不存在")
	}
	if err := normalizeInput(&in); err != nil {
		return nil, err
	}
	if in.BaseURI != ch.BaseURI && s.Validate != nil {
		if err := s.Validate(in.BaseURI); err != nil {
			return nil, fmt.Errorf("base_uri 校验失败: %w", err)
		}
	}
	var ct, iv, tag []byte
	var hint string
	if strings.TrimSpace(in.APIKey) != "" {
		var err error
		ct, iv, tag, hint, err = s.encryptKey(in.APIKey)
		if err != nil {
			return nil, err
		}
	}
	if err := s.St.UpdateChannelLocked(id, func(c *store.Channel) {
		c.Name = in.Name
		c.ProviderType = in.ProviderType
		c.BaseURI = in.BaseURI
		c.ModelsPath, c.ChatPath = in.ModelsPath, in.ChatPath
		c.AuthStyle, c.APIVersion = in.AuthStyle, in.APIVersion
		c.CustomHeaders = in.CustomHeaders
		c.Visibility, c.Weight, c.Priority = in.Visibility, in.Weight, in.Priority
		if len(ct) > 0 {
			c.APIKeyCipher, c.APIKeyIV, c.APIKeyTag, c.KeyHint = ct, iv, tag, hint
		}
		c.UpdatedAt = time.Now().Unix()
	}); err != nil {
		return nil, err
	}
	// 密钥或 base_uri 变更后重新发现真实模型（失败不影响更新成功）。
	if s.Discoverer != nil {
		refCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = s.Discoverer.AutoDiscover(refCtx, ch, discovery.AutoDiscoverOptions{Probe: s.probeEnabled()})
	}
	out, _ := s.St.GetChannel(id)
	return out, nil
}

// SetStatus 启停渠道。
func (s *Service) SetStatus(id, status string) (*store.Channel, error) {
	if status != "active" && status != "disabled" {
		return nil, errors.New("status 只能是 active|disabled")
	}
	if _, ok := s.St.GetChannel(id); !ok {
		return nil, errors.New("渠道不存在")
	}
	if err := s.St.UpdateChannelLocked(id, func(c *store.Channel) {
		c.Status = status
		c.UpdatedAt = time.Now().Unix()
	}); err != nil {
		return nil, err
	}
	out, _ := s.St.GetChannel(id)
	return out, nil
}

// Delete 删除渠道。
func (s *Service) Delete(id string) {
	s.St.DeleteChannel(id)
}

// TestResult 是 /test 端点的真实发现结果（模型 id 逐字来自上游 /models）。
type TestResult struct {
	OK         bool
	LatencyMs  int
	Discovered int
	ModelIDs   []string
	Message    string
}

// TestDetail 返回真实上游 /models 的完整发现结果，供前端/运维核对。
func (s *Service) TestDetail(ctx context.Context, id string) TestResult {
	ch, exists := s.St.GetChannel(id)
	if !exists {
		return TestResult{Message: "渠道不存在"}
	}
	if s.Discoverer == nil {
		return TestResult{Message: "未配置发现器"}
	}
	start := time.Now()
	ctxTimeout, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	models, err := s.Discoverer.ListModels(ctxTimeout, ch)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return TestResult{LatencyMs: latency, Message: err.Error()}
	}
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	shown := ids
	if len(shown) > 20 {
		shown = shown[:20]
	}
	return TestResult{
		OK: true, LatencyMs: latency, Discovered: len(models), ModelIDs: ids,
		Message: fmt.Sprintf("发现 %d 个真实模型: %s", len(models), strings.Join(shown, ", ")),
	}
}

// Test 手动测试连接，返回真实延迟与真实发现结果（保持既有三返回值契约）。
func (s *Service) Test(ctx context.Context, id string) (ok bool, latencyMs int, errMsg string) {
	r := s.TestDetail(ctx, id)
	return r.OK, r.LatencyMs, r.Message
}

// Sync 手动同步模型（F3）。
func (s *Service) Sync(ctx context.Context, id string) (discovery.SyncResult, error) {
	ch, ok := s.St.GetChannel(id)
	if !ok {
		return discovery.SyncResult{}, errors.New("渠道不存在")
	}
	if s.Discoverer == nil {
		return discovery.SyncResult{}, errors.New("未配置发现器")
	}
	ctxTimeout, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res := s.Discoverer.SyncChannel(ctxTimeout, ch)
	// 手动同步后同样补齐一次真实探测样例（不阻塞返回，后台进行）。
	if res.OK && s.probeEnabled() && len(res.Models) > 0 {
		s.Discoverer.AutoProbe(ch)
	}
	return res, nil
}

// Probe 手动探测（F4）。
func (s *Service) Probe(ctx context.Context, id string) (probed, failed int, err error) {
	ch, ok := s.St.GetChannel(id)
	if !ok {
		return 0, 0, errors.New("渠道不存在")
	}
	if s.Discoverer == nil {
		return 0, 0, errors.New("未配置发现器")
	}
	ctxTimeout, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	p, f := s.Discoverer.ProbeChannel(ctxTimeout, ch)
	return p, f, nil
}

// Authorize 校验用户是否有权操作该渠道（本人，或 admin/superuser 角色）。
func Authorize(ch *store.Channel, userID, role string) bool {
	if ch == nil {
		return false
	}
	if ch.OwnerID == userID {
		return true
	}
	return role == "admin" || role == "superuser"
}
