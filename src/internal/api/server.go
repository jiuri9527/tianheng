// Package api 实现 F1/F5/F9/F10/F11/F12 的全部 HTTP 端点：
// 数据面 /v1/*（OpenAI 兼容 + Anthropic Messages）、管理面 /api/*、/healthz、/metrics。
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"llm-hub/kernel"
	"llm-hub/src/internal/auth"
	"llm-hub/src/internal/avatar"
	"llm-hub/src/internal/channel"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/crypto"
	"llm-hub/src/internal/discovery"
	"llm-hub/src/internal/firewall"
	"llm-hub/src/internal/forum"
	"llm-hub/src/internal/health"
	"llm-hub/src/internal/metering"
	"llm-hub/src/internal/middleware"
	"llm-hub/src/internal/relay"
	"llm-hub/src/internal/router"
	"llm-hub/src/internal/rt"
	"llm-hub/src/internal/store"
	"llm-hub/src/internal/telemetry"
	"llm-hub/src/internal/usage"

	"llm-hub/src/internal/affinity"
	"llm-hub/src/internal/grouping"
	"llm-hub/src/internal/redact"
	"llm-hub/src/internal/routingjournal"
)

// Server 聚合所有依赖。
type Server struct {
	Cfg        *config.Config
	St         *store.Store
	Cipher     *crypto.Cipher
	Auth       *auth.Service
	Discoverer *discovery.Discoverer
	Channels   *channel.Service
	Router     *router.Router
	Relay      *relay.Relay
	Meter      *metering.Meter
	Health     *health.Checker
	Scheduler  kernel.Scheduler
	Selector   kernel.Selector
	StreamPipe kernel.StreamPipe
	Limiter    *middleware.Limiter
	FW         *firewall.Firewall
	mux        http.Handler

	// ── Magpie 原理原生实现（零第三方依赖）────────────────────────────
	Groups   *grouping.Catalog     // 逻辑分组 + 条件规则（逻辑名 → 多上游成员）
	Affinity *affinity.Store       // 会话亲和：多轮会话钉住同一上游以复用 prompt 缓存
	RJ       *routingjournal.Journal // 路由决策全量留痕（order/tries/affinity/masking）
	RedactOn bool                  // 出站脱敏开关（默认按 Policy.Enabled）
}

// NewServer 装配全部组件并返回可启动的 Server。
func NewServer(cfg *config.Config, st *store.Store) (*Server, error) {
	cipher, err := crypto.NewCipher(cfg.MasterKeyB64)
	if err != nil {
		return nil, err
	}
	authSvc := auth.NewService(st, cfg)

	// 四个 kernel 接口通过团队6自研 rt 实现注入（不调用 kernel 中不存在的构造函数）
	var scheduler kernel.Scheduler = rt.NewPriorityScheduler(4, 1024)
	var selector kernel.Selector = rt.NewLoadBalancer(rt.StrategyWeightedRR, cfg.CircuitCooldown)
	var streamPipe kernel.StreamPipe = rt.NewStreamPipe()
	var defaultLimiter kernel.Limiter = rt.NewTokenBucketLimiter(1000, 1000)

	scheduler.Start()
	disc := discovery.New(st, cipher, cfg)
	rly := relay.New(st, cipher, cfg).SetStreamPipe(streamPipe)
	meter := metering.New(st, cfg).SetScheduler(scheduler)
	meter.Start()
	chSvc := channel.New(st, cipher, disc, cfg)
	rtr := router.New(st, selector, cfg.MaxFailover, cfg.CircuitCooldown)
	chk := health.New(st, disc, scheduler, rtr, cfg.HealthCheckInterval)
	chk.Start()

	// ── Magpie 原理原生实现的装配（全部零第三方依赖）──────────────────
	// 分组目录：从 <DATA_DIR>/groups.json 载入；不存在则从真实渠道模型自动派生。
	catalog, err := grouping.Load(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	rows := make([]grouping.Row, 0, 64)
	for _, m := range st.ListModels() {
		if ch, ok := st.GetChannel(m.ChannelID); ok {
			rows = append(rows, grouping.Row{
				ChannelID: ch.ID, ProviderType: ch.ProviderType,
				UpstreamModelID: m.UpstreamID, Alias: m.Alias,
			})
		}
	}
	if n := catalog.AutoDerive(rows); n > 0 {
		_, _ = catalog.Save(cfg.DataDir) // 首次派生落盘（后续渠道同步会继续 AutoDerive）
	}

	// 会话亲和：粘住上游复用 prompt 缓存（不是用户限制）。
	affStore := affinity.New(cfg.DataDir)
	_ = affStore.Load() // 无状态文件属首次运行；载入失败不阻塞启动（下次 Save 会重建）

	// 路由决策日志：<DATA_DIR>/routing/YYYY-MM-DD.jsonl，历史自动 gzip 归档。
	rj := routingjournal.New(cfg.DataDir)

	fwCfg := firewall.DefaultConfig(cfg.DataDir)
	// 天衡：rate_burst 属攻击检测不是使用限制，保留；放宽阈值避免移除限流后
	// 把正常高频调用误判为突发攻击（阈值由 FIREWALL_BURST_THRESHOLD 配置）。
	if cfg.FirewallBurstThreshold > 0 {
		fwCfg.BurstThreshold = cfg.FirewallBurstThreshold
	}
	fw := firewall.New(fwCfg)
	fw.OnLockdown(func() { _ = st.Save() })
	fw.OnRecover(func() { _ = st.Save() })

	s := &Server{
		Cfg: cfg, St: st, Cipher: cipher, Auth: authSvc,
		Discoverer: disc, Channels: chSvc, Router: rtr, Relay: rly, Meter: meter,
		Health: chk, Scheduler: scheduler, Selector: selector, StreamPipe: streamPipe,
		Limiter: middleware.NewLimiter(defaultLimiter),
		FW:        fw,
		Groups:    catalog,
		Affinity:  affStore,
		RJ:        rj,
		RedactOn:  redact.DefaultPolicy().Enabled,
	}
	s.mux = s.buildMux()

	// 周期任务：归档旧路由日志（历史 gzip）+ 亲和落盘 + 清理过期亲和。
	// 单独 goroutine，随进程退出（日志/亲和都是尽力持久化，不阻塞关闭）。
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		now := time.Now()
		_, _ = rj.Archive(now)
		_, _ = affStore.Save(now)
		_ = affStore.Prune(now)
		for now = range t.C {
			_, _ = rj.Archive(now)
			_, _ = affStore.Save(now)
			_ = affStore.Prune(now)
		}
	}()

	return s, nil
}

// Handler 返回根 HTTP Handler。
func (s *Server) Handler() http.Handler { return s.mux }

// SetDoer 注入 HTTP 执行器（测试/沙箱联调共用）。
func (s *Server) SetDoer(d discovery.Doer) *Server {
	s.Discoverer.SetDoer(d)
	s.Relay.SetDoer(d)
	return s
}

// SetValidator 注入自定义 SSRF 校验器（离线测试用）。
func (s *Server) SetValidator(v func(string) error) *Server {
	s.Channels.SetValidator(v)
	return s
}

// SetSynchronousMetering 让计量同步写（测试确定性）。
func (s *Server) SetSynchronousMetering(b bool) *Server {
	s.Meter.SetSynchronous(b)
	return s
}

// Stop 释放后台资源。
func (s *Server) Stop() {
	if s.Health != nil {
		s.Health.Stop()
	}
	if s.Meter != nil {
		s.Meter.Stop()
	}
	if s.Channels != nil {
		s.Channels.Stop() // 停止模型自动刷新器（docs/15 未完成项 1，Lead 接线）
	}
	if s.Scheduler != nil {
		s.Scheduler.Stop()
	}
	_ = s.St.Save()
}

// ---------- 中间件包装 ----------

func (s *Server) public(h http.HandlerFunc) http.Handler {
	return middleware.Chain(h, middleware.RequestID, middleware.CORS(s.Cfg.CORSOrigins))
}

func (s *Server) data(h http.HandlerFunc) http.Handler {
	return middleware.Chain(h,
		middleware.RequestID,
		middleware.CORS(s.Cfg.CORSOrigins),
		middleware.BodyLimit(s.Cfg.BodyLimitBytes()),
		middleware.DataPlane(s.St, s.Limiter, s.Cfg),
	)
}

func (s *Server) mgmt(h http.HandlerFunc) http.Handler {
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r)
		if r.Method != http.MethodGet && r.Method != http.MethodOptions {
			_ = s.St.Save()
		}
	})
	chain := middleware.Chain(wrapped,
		middleware.RequestID,
		middleware.CORS(s.Cfg.CORSOrigins),
		middleware.BodyLimit(s.Cfg.BodyLimitBytes()),
		middleware.Management(s.Auth),
	)
	if s.FW != nil {
		chain = s.FW.GuardSensitive(chain)
	}
	return chain
}

// adminOnly 在 mgmt 的身份链之上再加 F11 角色门禁。
func (s *Server) adminOnly(h http.HandlerFunc) http.Handler {
	return s.mgmt(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAdmin(w, r); !ok {
			return
		}
		h(w, r)
	})
}

// ---------- 路由表 ----------

func (s *Server) buildMux() http.Handler {
	mux := http.NewServeMux()

	// 公共
	mux.Handle("GET /healthz", s.public(s.healthz))
	mux.Handle("GET /metrics", s.public(s.metrics))
	mux.Handle("POST /api/auth/register", s.public(s.register))
	mux.Handle("POST /api/auth/login", s.public(s.login))
	mux.Handle("POST /api/auth/refresh", s.public(s.refresh))

	// 绝对真实模型排行榜（只读公开端点）
	mux.Handle("GET /api/rankings", s.public(s.rankings))
	// ── Magpie 原理原生实现的运行时视图（只读、真实数据、无数据即空态）──
	mux.Handle("GET /api/routing/affinity", s.public(s.affinityView))
	mux.Handle("GET /api/routing/journal", s.public(s.routingJournalView))
	mux.Handle("GET /api/routing/groups", s.public(s.groupsView))
	mux.Handle("GET /api/routing/resolve", s.public(s.resolveView))
	mux.Handle("GET /api/routing/redact", s.public(s.redactPolicyView))
	mux.Handle("POST /api/routing/redact/preview", s.public(s.redactPreview))
	mux.Handle("GET /api/rankings/{dimension}", s.public(s.rankings))

	// 内置攻防防火墙运维端点
	mux.Handle("GET /api/firewall/status", s.public(s.fwStatus))
	mux.Handle("GET /api/firewall/events", s.mgmt(s.fwEvents))
	mux.Handle("GET /api/firewall/verify", s.public(s.fwVerify))
	mux.Handle("POST /api/firewall/unlock", s.mgmt(s.fwUnlock))
	mux.Handle("POST /api/firewall/seal", s.mgmt(s.fwSeal))

	// 数据面（sk-）
	mux.Handle("GET /v1/models", s.data(s.v1Models))
	// 真实模型 id 形如 liquid/lfm-3-1.7b，含斜杠，必须用 {id...} 余段通配。
	mux.Handle("GET /v1/models/{id...}", s.data(s.v1Model))
	mux.Handle("POST /v1/chat/completions", s.data(s.v1Chat))
	mux.Handle("POST /v1/completions", s.data(s.v1Raw("/completions", false)))
	mux.Handle("POST /v1/embeddings", s.data(s.v1Raw("/embeddings", false)))
	mux.Handle("POST /v1/images/generations", s.data(s.v1Raw("/images/generations", false)))
	mux.Handle("POST /v1/audio/speech", s.data(s.v1Raw("/audio/speech", false)))
	mux.Handle("POST /v1/audio/transcriptions", s.data(s.v1Raw("/audio/transcriptions", false)))
	mux.Handle("POST /v1/messages", s.data(s.v1Messages))
	mux.Handle("POST /v1/responses", s.data(s.v1Raw("/responses", false)))

	// 管理面（JWT）
	mux.Handle("POST /api/auth/logout", s.mgmt(s.logout))
	mux.Handle("POST /api/auth/password", s.mgmt(s.changePassword))
	mux.Handle("GET /api/me", s.mgmt(s.me))
	mux.Handle("GET /api/me/channels", s.mgmt(s.myChannels))
	mux.Handle("GET /api/me/tokens", s.mgmt(s.myTokens))
	mux.Handle("GET /api/me/usage", s.mgmt(s.myUsage))
	mux.Handle("GET /api/me/rewards", s.mgmt(s.myRewards))
	mux.Handle("GET /api/me/examples", s.mgmt(s.myExamples))

	mux.Handle("POST /api/channels", s.mgmt(s.createChannel))
	mux.Handle("GET /api/channels", s.mgmt(s.listChannels))
	mux.Handle("GET /api/channels/{id}", s.mgmt(s.getChannel))
	mux.Handle("PUT /api/channels/{id}", s.mgmt(s.updateChannel))
	mux.Handle("DELETE /api/channels/{id}", s.mgmt(s.deleteChannel))
	mux.Handle("POST /api/channels/{id}/test", s.mgmt(s.testChannel))
	mux.Handle("POST /api/channels/{id}/sync-models", s.mgmt(s.syncChannel))
	mux.Handle("POST /api/channels/{id}/probe", s.mgmt(s.probeChannel))
	mux.Handle("POST /api/channels/{id}/status", s.mgmt(s.setChannelStatus))

	mux.Handle("GET /api/models", s.mgmt(s.listModels))
	mux.Handle("GET /api/models/{id}", s.mgmt(s.getModel))
	mux.Handle("PUT /api/models/{id}", s.mgmt(s.updateModel))
	mux.Handle("POST /api/models/{id}/probe", s.mgmt(s.probeModel))

	mux.Handle("GET /api/aliases", s.mgmt(s.listAliases))
	mux.Handle("POST /api/aliases", s.mgmt(s.createAlias))
	mux.Handle("PUT /api/aliases/{alias}", s.mgmt(s.updateAlias))
	mux.Handle("DELETE /api/aliases/{alias}", s.mgmt(s.deleteAlias))

	mux.Handle("POST /api/tokens", s.mgmt(s.createToken))
	mux.Handle("GET /api/tokens", s.mgmt(s.listTokens))
	mux.Handle("DELETE /api/tokens/{id}", s.mgmt(s.revokeToken))
	mux.Handle("POST /api/tokens/{id}/rotate", s.mgmt(s.rotateToken))

	mux.Handle("GET /api/usage", s.mgmt(s.usage))
	mux.Handle("GET /api/logs", s.mgmt(s.logs))
	mux.Handle("GET /api/admin/dashboard", s.adminOnly(s.adminDashboard))
	mux.Handle("GET /api/admin/users", s.adminOnly(s.adminUsers))
	mux.Handle("PUT /api/admin/users/{id}", s.adminOnly(s.adminUpdateUser))
	mux.Handle("GET /api/admin/settings", s.adminOnly(s.adminGetSettings))
	mux.Handle("PUT /api/admin/settings", s.adminOnly(s.adminPutSettings))
	mux.Handle("GET /api/admin/channels", s.adminOnly(s.adminChannels))
	mux.Handle("GET /api/admin/models", s.adminOnly(s.adminModels))
	mux.Handle("GET /api/admin/tokens", s.adminOnly(s.adminTokens))

	// ---- 天衡新模块（各包自注册，Lead 统一接线）----
	// 社区论坛 /api/forum/*
	forum.Register(mux, s.St, s.Cfg, s.Auth)
	// 头像上传与个人资料 /api/avatar/* /api/profile*
	avatar.Register(mux, s.St, s.Cfg, s.Auth)
	// 全员可见实时监控（public） /api/telemetry/*
	telemetry.Register(mux, s.St, s.Cfg, s.Auth)
	// 用量时间序列与按模型聚合 /api/usage/*
	usage.Register(mux, s.St, s.Cfg, s.Auth)

	return s.FW.Middleware(mux)
}

// ---------- 公共响应辅助 ----------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, r *http.Request, status int, errType, code, message string) {
	relay.WriteJSONError(w, status, errType, code, message, middleware.RequestIDFrom(r.Context()))
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	data, _, ok := middleware.ReadBody(w, r)
	return data, ok
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	data, ok := readBody(w, r)
	if !ok {
		return false
	}
	if len(data) == 0 {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "empty_body", "request body required")
		return false
	}
	if err := json.Unmarshal(data, dst); err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_json", err.Error())
		return false
	}
	return true
}

// requireAdmin 天衡：所有用户都是超级用户，没有任何管理员；
// 本函数恒通过，不做任何角色判断（仅保留 JWT 身份校验）。
func requireAdmin(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	u := middleware.UserFrom(r.Context())
	if u == nil {
		fail(w, r, http.StatusUnauthorized, "invalid_request_error", "unauthorized", "authentication required")
		return nil, false
	}
	return u, true
}

// isAdminRole 天衡无角色区分：恒为 true（保留函数以免破坏既有调用点）。
func isAdminRole(role string) bool { return true }

// authorizeChannel 校验渠道存在性与操作权限（F2 多租户隔离）。
// 规则：superuser/admin 可管理任意渠道；普通用户仅本人渠道；
// public 渠道对所有人可读，但只有所有者或管理员可写。
func (s *Server) authorizeChannel(w http.ResponseWriter, r *http.Request, id string) (*store.Channel, bool) {
	ch, ok := s.St.GetChannel(id)
	if !ok {
		fail(w, r, http.StatusNotFound, "invalid_request_error", "not_found", "channel not found")
		return nil, false
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		fail(w, r, http.StatusUnauthorized, "invalid_request_error", "unauthorized", "authentication required")
		return nil, false
	}
	if channel.Authorize(ch, u.ID, u.Role) {
		return ch, true
	}
	// 读操作可放宽到 public 渠道；写操作到此为止已经拒绝。
	if r.Method == http.MethodGet && ch.Visibility == "public" {
		return ch, true
	}
	fail(w, r, http.StatusForbidden, "invalid_request_error", "forbidden_channel",
		"channel belongs to another user")
	return nil, false
}

// channelView 安全视图：绝不回显 api_key 明文或密文，只给 key_hint。
func channelView(ch *store.Channel) map[string]interface{} {
	return map[string]interface{}{
		"id": ch.ID, "owner_id": ch.OwnerID, "name": ch.Name,
		"provider_type": ch.ProviderType, "base_uri": ch.BaseURI,
		"models_path": ch.ModelsPath, "chat_path": ch.ChatPath,
		"auth_style": ch.AuthStyle, "api_version": ch.APIVersion,
		"key_hint": ch.KeyHint, "custom_headers": ch.CustomHeaders,
		"visibility": ch.Visibility, "weight": ch.Weight, "priority": ch.Priority,
		"status": ch.Status, "probe_status": ch.ProbeStatus,
		"health_checked_at": ch.HealthCheckedAt, "last_error": ch.LastError,
		"fail_count": ch.FailCount, "cooldown_until": ch.CooldownUntil,
		"created_at": ch.CreatedAt, "updated_at": ch.UpdatedAt,
	}
}

func tokenView(t *store.Token) map[string]interface{} {
	return map[string]interface{}{
		"id": t.ID, "name": t.Name, "key_hint": t.KeyHint,
		"allowed_models": t.AllowedModels, "rpm": t.RPM, "tpm": t.TPM,
		"max_concurrency": t.MaxConcurrency, "quota_usd": t.QuotaUSD,
		"used_usd": t.UsedUSD, "ip_allowlist": t.IPAllowlist,
		"expires_at": t.ExpiresAt, "status": t.Status,
		"last_used_at": t.LastUsedAt, "created_at": t.CreatedAt,
	}
}

func userView(u *store.User) map[string]interface{} {
	return map[string]interface{}{
		"id": u.ID, "email": u.Email, "role": u.Role,
		"balance_usd": u.BalanceUSD, "status": u.Status,
		"created_at": u.CreatedAt,
	}
}

func nowUnix() int64 { return time.Now().Unix() }
