// Package metering 实现 F10：上游 usage 优先 + 估算兜底 + 异步日志 +
// 计费/贡献奖励返还 + 用量日聚合 + 原子指标。
package metering

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"sync"
	"time"

	"llm-hub/kernel"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/rt"
	"llm-hub/src/internal/store"
)

// Record 是一次调用待落库的完整记录。
type Record struct {
	RequestID         string
	UserID            string
	TokenID           string
	Alias             string
	ModelID           string
	ChannelID         string
	ProviderType      string
	Stream            bool
	RequestBody       json.RawMessage
	ResponseBody      string
	PromptTokens      int
	CompletionTokens  int
	TTFTMs            int
	LatencyMs         int
	StatusCode        int
	ErrorCode         string
	ClientIP          string
	Token             *store.Token
	ContributorUserID string
	RewardRate        float64
	CostUSD           float64
}

// Meter 计量器。
type Meter struct {
	St         *store.Store
	Cfg        *config.Config
	Scheduler  kernel.Scheduler // 注入：异步日志任务
	queue      chan *Record
	mu         sync.Mutex
	wg         sync.WaitGroup
	started    bool
	syncMode   bool
}

// New 构造计量器。
func New(st *store.Store, cfg *config.Config) *Meter {
	return &Meter{St: st, Cfg: cfg, queue: make(chan *Record, 4096)}
}

func (m *Meter) SetScheduler(s kernel.Scheduler) *Meter { m.Scheduler = s; return m }
func (m *Meter) SetSynchronous(b bool) *Meter           { m.syncMode = b; return m }

// Start 启动后台消费协程（非同步模式）。
func (m *Meter) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.syncMode {
		return
	}
	m.started = true
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for rec := range m.queue {
			m.writeNow(rec)
		}
	}()
}

// Stop 关闭队列并等待消费完毕。
func (m *Meter) Stop() {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	m.started = false
	close(m.queue)
	m.mu.Unlock()
	m.wg.Wait()
	m.queue = make(chan *Record, 4096)
}

// Record 提交一条记录（异步；同步模式下立即写）。
func (m *Meter) Record(rec *Record) {
	if m.syncMode {
		m.writeNow(rec)
		return
	}
	if m.Scheduler != nil {
		r := rec
		if err := m.Scheduler.Submit(&kernel.Task{
			ID: r.RequestID, Priority: 1, Created: time.Now(),
			Fn: func(context.Context) error { m.writeNow(r); return nil },
		}); err == nil {
			return
		}
	}
	select {
	case m.queue <- rec:
	default:
		m.writeNow(rec) // 队列满时同步落库，避免丢日志
	}
}

// EstimateTokens 估算兜底：约 4 个字符/token（零依赖近似，替代 tiktoken）。
func EstimateTokens(text string) int {
	n := len([]rune(text))
	return (n + 3) / 4
}

var (
	skPattern     = regexp.MustCompile(`sk-[A-Za-z0-9_-]{4,}`)
	bearerPattern = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	apiKeyPattern = regexp.MustCompile(`(?i)(x-api-key|api[_-]?key)["'\s:=]+[A-Za-z0-9._~+/=-]{8,}`)
)

func (m *Meter) mask(s string) string {
	if m.Cfg == nil || !m.Cfg.LogMaskKeys {
		return s
	}
	s = skPattern.ReplaceAllString(s, "****")
	s = bearerPattern.ReplaceAllString(s, "Bearer ****")
	s = apiKeyPattern.ReplaceAllString(s, "$1=****")
	return s
}

func (m *Meter) writeNow(rec *Record) {
	// 上游 usage 优先；缺省时估算兜底
	if rec.PromptTokens == 0 {
		rec.PromptTokens = EstimateTokens(string(rec.RequestBody))
	}
	if rec.CompletionTokens == 0 {
		rec.CompletionTokens = EstimateTokens(rec.ResponseBody)
	}
	// 计费：价格取自真实发现的模型（上游定价），无价格则为 0，绝不编造
	if rec.CostUSD == 0 {
		rec.CostUSD = m.computeCost(rec)
	}
	if math.IsNaN(rec.CostUSD) || math.IsInf(rec.CostUSD, 0) || rec.CostUSD < 0 {
		rec.CostUSD = 0
	}

	reqBody := json.RawMessage(m.mask(string(rec.RequestBody)))
	respBody := m.mask(rec.ResponseBody)
	log := &store.RequestLog{
		RequestID: rec.RequestID, UserID: rec.UserID, TokenID: rec.TokenID,
		Alias: rec.Alias, ModelID: rec.ModelID, ChannelID: rec.ChannelID,
		ProviderType: rec.ProviderType, Stream: rec.Stream,
		RequestBody: reqBody, ResponseBody: respBody,
		PromptTokens: rec.PromptTokens, CompletionTokens: rec.CompletionTokens,
		TTFTMs: rec.TTFTMs, LatencyMs: rec.LatencyMs,
		StatusCode: rec.StatusCode, ErrorCode: rec.ErrorCode,
		CostUSD: rec.CostUSD, ClientIP: rec.ClientIP, CreatedAt: time.Now().Unix(),
	}
	m.St.AddRequestLog(log)
	day := time.Now().Format("2006-01-02")
	m.St.AddUsage(day, rec.UserID, rec.Alias, 1, rec.PromptTokens, rec.CompletionTokens, rec.CostUSD)
	m.applyBilling(rec)
	// 原子指标
	status := statusLabel(rec.StatusCode)
	rt.GlobalMetrics.AddRequest(rec.Alias, rec.ChannelID, status)
	rt.GlobalMetrics.AddTokens("prompt", int64(rec.PromptTokens))
	rt.GlobalMetrics.AddTokens("completion", int64(rec.CompletionTokens))
	if rec.TTFTMs > 0 {
		rt.GlobalMetrics.AddTTFT(int64(rec.TTFTMs) * int64(time.Millisecond))
	}
}

func statusLabel(code int) string {
	if code == 0 {
		return "0"
	}
	if code >= 200 && code < 300 {
		return "2xx"
	}
	if code >= 400 && code < 500 {
		return "4xx"
	}
	if code >= 500 {
		return "5xx"
	}
	return "other"
}

func (m *Meter) computeCost(rec *Record) float64 {
	model, ok := m.St.GetModel(rec.ModelID)
	if !ok {
		return 0
	}
	in := float64(rec.PromptTokens) / 1e6 * model.InputPricePerMtok
	out := float64(rec.CompletionTokens) / 1e6 * model.OutputPricePerMtok
	c := in + out
	// 非有限/负数/异常量级花费一律归零：NaN 会写进 UsageDaily 使 json.Marshal 永久失败。
	if math.IsNaN(c) || math.IsInf(c, 0) || c < 0 || c > 1e6 {
		return 0
	}
	return c
}

func (m *Meter) applyBilling(rec *Record) {
	if rec.CostUSD <= 0 || math.IsNaN(rec.CostUSD) || math.IsInf(rec.CostUSD, 0) {
		return
	}
	var tokenID string
	if rec.Token != nil {
		tokenID = rec.Token.ID
	}
	reward := 0.0
	if rec.ContributorUserID != "" && rec.ContributorUserID != rec.UserID && rec.RewardRate > 0 {
		reward = rec.CostUSD * rec.RewardRate
	}
	// 全部额度变更在 store 同一把锁下原子完成，避免并发丢更新/双花。
	m.St.ApplyBilling(tokenID, rec.UserID, rec.ContributorUserID, rec.CostUSD, reward, rec.RequestID)
}
