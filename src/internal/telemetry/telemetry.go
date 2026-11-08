// Package telemetry 实现「天衡改造 · 实时监控」后端。
//
// 数据来源（唯一）：store.Store.ListRequestLogs(LogFilter{}) —— 即运行时
// request_logs 表（启动时从 cfg.DataDir/request_logs.json 载入、运行期由
// metering 追加的真实调用记录）。本包只读，不新增、不修改、不删除任何日志，
// 也不读写 cfg.DataDir 下的其它文件。
//
// 隐私硬约束：响应体只含「聚合数字」与「分类键」(model alias / channel id /
// provider type / status code)，绝不携带 request_body / response_body /
// api_key / sk- 平台令牌 / JWT / password_hash / user_id / token_id /
// client_ip / error_code。分类键统一经 safeKey 去控制字符 + sk- 脱敏。
//
// 真实性硬约束：所有 QPS / 成功率 / TTFT / token / 曲线点均由上面的真实
// request_logs 逐条聚合得出；一条日志都没有时 available=false、所有数字为
// 真实 0、序列为 60 个真实零值桶，不填充任何占位或编造数字。
//
// 接线：禁止修改 src/internal/api/server.go；导出 Register(mux, st, cfg, auth)，
// 由 Lead 在 buildMux() 内调用。两个端点都用 middleware.Chain 包装为
// pub = RequestID + CORS（public，无需 JWT，遵循 forum/avatar 既有约定）。
package telemetry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"llm-hub/src/internal/auth"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/middleware"
	"llm-hub/src/internal/store"
)

const (
	windowSeconds  = 3600             // 时间序列窗口：最近 60 分钟
	bucketSeconds  = 60               // 桶粒度：每分钟
	bucketCount    = windowSeconds / bucketSeconds // 60 个桶
	qpsWindow      = 60               // QPS 统计窗口：最近 60 秒
	streamInterval = 2 * time.Second  // SSE 推送间隔
	// V-37 修复：无上限的并发 SSE 流 + 每连接独立全量聚合可被单台主机
	// 打成资源耗尽（N 连接 = 每 2s N 次全量 request_logs 扫描 + N 个常驻
	// goroutine）。streamMaxConns 限制同时打开的流，streamMaxConnsEnv
	// 可用环境变量覆盖；cachedAggregate 让同一 tick 内所有连接共享同一
	// 份聚合结果（每 interval 只算一次）。
	streamMaxConns    = 256
	streamMaxConnsEnv = "TELEMETRY_STREAM_MAX_CONNS"
)

// GroupStat 是某一维度下某个分类键的聚合数字。
type GroupStat struct {
	Key              string  `json:"key"`
	Requests         int64   `json:"requests"`
	Success          int64   `json:"success"`
	Errors           int64   `json:"errors"`
	SuccessRate      float64 `json:"success_rate"`
	AvgTTFTMs        float64 `json:"avg_ttft_ms"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
}

// Groups 保存四个维度的分组结果，均按 requests 降序、key 升序排列。
type Groups struct {
	ByModel    []GroupStat `json:"by_model"`
	ByChannel  []GroupStat `json:"by_channel"`
	ByProvider []GroupStat `json:"by_provider"`
	ByStatus   []GroupStat `json:"by_status"`
}

// Bucket 是最近 60 分钟内某一分钟的聚合点；无日志的分钟是真实零值桶。
type Bucket struct {
	MinuteStart      int64   `json:"minute_start"` // 对齐到分钟的 unix 秒
	Label            string  `json:"label"`         // 服务端本地时间 HH:MM
	Requests         int64   `json:"requests"`
	Success          int64   `json:"success"`
	Errors           int64   `json:"errors"`
	QPS              float64 `json:"qps"`           // requests / 60
	SuccessRate      float64 `json:"success_rate"`
	AvgTTFTMs        float64 `json:"avg_ttft_ms"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
}

// Snapshot 是一次完整聚合快照。Available=false 表示 request_logs 为空（真实空态）。
type Snapshot struct {
	Available          bool     `json:"available"`
	Source             string   `json:"source"`
	GeneratedAt        int64    `json:"generated_at"`
	WindowSeconds      int      `json:"window_seconds"`
	BucketSeconds      int      `json:"bucket_seconds"`
	QPSWindowSeconds   int      `json:"qps_window_seconds"`
	TotalRequests      int64    `json:"total_requests"`
	QPS                float64  `json:"qps"`                 // 最近 60s 请求数 / 60
	PeakQPS            float64  `json:"peak_qps"`            // 60 个桶中的峰值 QPS
	SuccessRate        float64  `json:"success_rate"`        // 2xx / 全部，百分比 0..100
	SuccessCount       int64    `json:"success_count"`
	ErrorCount         int64    `json:"error_count"`
	AvgTTFTMs          float64  `json:"avg_ttft_ms"`         // 仅统计 TTFTMs>0 的样本
	MaxTTFTMs          int64    `json:"max_ttft_ms"`
	TTFTSampleCount    int64    `json:"ttft_sample_count"`
	PromptTokens       int64    `json:"prompt_tokens"`
	CompletionTokens   int64    `json:"completion_tokens"`
	TotalTokens        int64    `json:"total_tokens"`
	StreamRequests     int64    `json:"stream_requests"`
	Groups             Groups   `json:"groups"`
	Series             []Bucket `json:"series"` // 长度恒为 60，最旧 → 最新
}

// logPoint 是从 store.RequestLog 抽取的只读数值投影，避免持有内部指针。
type logPoint struct {
	created    int64
	status     int
	alias      string
	modelID    string
	channel    string
	provider   string
	stream     bool
	prompt     int64
	completion int64
	ttft       int64 // 毫秒，0 表示无样本
}

type groupAcc struct {
	requests, success, errors int64
	ttftSum, ttftCnt          int64
	prompt, completion        int64
}

func (g *groupAcc) add(p logPoint) {
	g.requests++
	if isSuccess(p.status) {
		g.success++
	} else {
		g.errors++
	}
	if p.ttft > 0 {
		g.ttftSum += p.ttft
		g.ttftCnt++
	}
	g.prompt += p.prompt
	g.completion += p.completion
}

func toStats(m map[string]*groupAcc) []GroupStat {
	out := make([]GroupStat, 0, len(m))
	for k, a := range m {
		s := GroupStat{
			Key: k, Requests: a.requests, Success: a.success, Errors: a.errors,
			PromptTokens: a.prompt, CompletionTokens: a.completion,
		}
		if a.requests > 0 {
			s.SuccessRate = 100 * float64(a.success) / float64(a.requests)
		}
		if a.ttftCnt > 0 {
			s.AvgTTFTMs = float64(a.ttftSum) / float64(a.ttftCnt)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func isSuccess(code int) bool { return code >= 200 && code < 300 }

var skPattern = regexp.MustCompile(`sk-[A-Za-z0-9_-]{4,}`)

// safeKey 把分类键限制为短小、无控制字符、无 sk- 令牌片段的纯文本。
// 这是「绝不回显完整输入输出 / 密钥」的最后一道输出侧防线。
func safeKey(s string) string {
	if len(s) > 96 {
		s = s[:96]
	}
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	s = skPattern.ReplaceAllString(s, "sk-****")
	if s == "" {
		return "(unknown)"
	}
	return s
}

func statusKey(code int) string {
	if code == 0 {
		return "0"
	}
	return fmt.Sprintf("%d", code)
}

func modelKey(p logPoint) string {
	if p.alias != "" {
		return p.alias
	}
	if p.modelID != "" {
		return p.modelID
	}
	return "(unknown)"
}

// aggregate 从真实 request_logs 逐条聚合出一份 Snapshot。now 由调用方注入
// 以便测试确定性；生产调用传 time.Now()。本函数只读 store，不产生模型数据。
// aggregate 按给定窗口与桶粒度真实聚合 request_logs。
// window 为回溯秒数，bucket 为桶粒度秒数；两者由调用方经 parseWindow 校验。
func aggregate(st *store.Store, now time.Time, window, bucket int) Snapshot {
	bucketCnt := window / bucket
	s := Snapshot{
		Available:        false,
		Source:           "store.request_logs/ListRequestLogs (real request_logs aggregate)",
		GeneratedAt:      now.Unix(),
		WindowSeconds:    window,
		BucketSeconds:    bucket,
		QPSWindowSeconds: qpsWindow,
		Groups: Groups{
			ByModel: []GroupStat{}, ByChannel: []GroupStat{},
			ByProvider: []GroupStat{}, ByStatus: []GroupStat{},
		},
		Series: make([]Bucket, 0, bucketCnt),
	}

	// 先构造「真实零值桶」：时间戳与标签来自真实当前时间，数值全 0。
	nowSec := now.Unix()
	nowMin := nowSec - nowSec%int64(bucket)
	startMin := nowMin - int64(bucketCnt-1)*int64(bucket)
	for i := 0; i < bucketCnt; i++ {
		m := startMin + int64(i)*int64(bucket)
		s.Series = append(s.Series, Bucket{
			MinuteStart: m,
			Label:       time.Unix(m, 0).Format("15:04"),
		})
	}

	if st == nil {
		return s
	}
	// PageSize 取足够大以一次性取回全部日志（无过滤条件 => matched 为全表）。
	logs, _ := st.ListRequestLogs(store.LogFilter{Page: 1, PageSize: 1 << 30})
	if len(logs) == 0 {
		// 真实空态：available=false，所有数字保持 0，序列为 60 个零值桶。
		return s
	}
	s.Available = true

	pts := make([]logPoint, 0, len(logs))
	for _, l := range logs {
		if l == nil {
			continue
		}
		pts = append(pts, logPoint{
			created: l.CreatedAt, status: l.StatusCode,
			alias: l.Alias, modelID: l.ModelID, channel: l.ChannelID,
			provider: l.ProviderType, stream: l.Stream,
			prompt: int64(l.PromptTokens), completion: int64(l.CompletionTokens),
			ttft: int64(l.TTFTMs),
		})
	}

	byModel := map[string]*groupAcc{}
	byChannel := map[string]*groupAcc{}
	byProvider := map[string]*groupAcc{}
	byStatus := map[string]*groupAcc{}
	addTo := func(m map[string]*groupAcc, key string, p logPoint) {
		a := m[key]
		if a == nil {
			a = &groupAcc{}
			m[key] = a
		}
		a.add(p)
	}

	var ttftSum, ttftCnt, maxTTFT int64
	var qpsCount int64
	qpsFrom := nowSec - qpsWindow
	for _, p := range pts {
		s.TotalRequests++
		if isSuccess(p.status) {
			s.SuccessCount++
		} else {
			s.ErrorCount++
		}
		if p.ttft > 0 {
			ttftSum += p.ttft
			ttftCnt++
			if p.ttft > maxTTFT {
				maxTTFT = p.ttft
			}
		}
		s.PromptTokens += p.prompt
		s.CompletionTokens += p.completion
		if p.stream {
			s.StreamRequests++
		}
		if p.created >= qpsFrom && p.created <= nowSec {
			qpsCount++
		}

		addTo(byModel, safeKey(modelKey(p)), p)
		addTo(byChannel, safeKey(p.channel), p)
		addTo(byProvider, safeKey(p.provider), p)
		addTo(byStatus, safeKey(statusKey(p.status)), p)

		// 时间序列：把该日志归入其所属分钟桶（仅窗口内）。
		min := p.created - p.created%int64(bucket)
		if min >= startMin && min <= nowMin {
			idx := int((min - startMin) / bucketSeconds)
			if idx >= 0 && idx < len(s.Series) {
				b := &s.Series[idx]
				b.Requests++
				if isSuccess(p.status) {
					b.Success++
				} else {
					b.Errors++
				}
				b.PromptTokens += p.prompt
				b.CompletionTokens += p.completion
			}
		}
	}

	s.QPS = float64(qpsCount) / float64(qpsWindow)
	if s.TotalRequests > 0 {
		s.SuccessRate = 100 * float64(s.SuccessCount) / float64(s.TotalRequests)
	}
	if ttftCnt > 0 {
		s.AvgTTFTMs = float64(ttftSum) / float64(ttftCnt)
		s.MaxTTFTMs = maxTTFT
		s.TTFTSampleCount = ttftCnt
	}
	s.TotalTokens = s.PromptTokens + s.CompletionTokens
	s.Groups = Groups{
		ByModel:    toStats(byModel),
		ByChannel:  toStats(byChannel),
		ByProvider: toStats(byProvider),
		ByStatus:   toStats(byStatus),
	}

	// 桶级派生量：QPS、成功率。
	for i := range s.Series {
		b := &s.Series[i]
		b.QPS = float64(b.Requests) / float64(bucketSeconds)
		if b.Requests > 0 {
			b.SuccessRate = 100 * float64(b.Success) / float64(b.Requests)
		}
		if b.QPS > s.PeakQPS {
			s.PeakQPS = b.QPS
		}
	}
	// 精确计算每个桶的平均 TTFT：按分钟桶再遍历一次样本（数据已在内存投影中），
	// 只统计 TTFTMs>0 的样本，无样本桶保持真实 0。
	bucketTTFTSum := make([]int64, bucketCount)
	bucketTTFTCnt := make([]int64, bucketCount)
	for _, p := range pts {
		if p.ttft <= 0 {
			continue
		}
		min := p.created - p.created%bucketSeconds
		if min >= startMin && min <= nowMin {
			idx := int((min - startMin) / bucketSeconds)
			if idx >= 0 && idx < bucketCount {
				bucketTTFTSum[idx] += p.ttft
				bucketTTFTCnt[idx]++
			}
		}
	}
	for i := range s.Series {
		if bucketTTFTCnt[i] > 0 {
			s.Series[i].AvgTTFTMs = float64(bucketTTFTSum[i]) / float64(bucketTTFTCnt[i])
		} else {
			s.Series[i].AvgTTFTMs = 0
		}
	}

	return s
}

// ---------- HTTP 层 ----------

type handler struct {
	st   *store.Store
	cfg  *config.Config
	auth *auth.Service // 两个端点 public，保留以统一 Register 签名
}

// Register 在 Lead 传入的 *http.ServeMux 上注册两个 public 实时监控端点：
//
//	GET /api/telemetry/snapshot —— 完整聚合快照（application/json）
//	GET /api/telemetry/stream   —— SSE，每 2 秒推送一份真实快照
//
// 包内用 middleware.Chain 包装 pub = RequestID + CORS，无 JWT、无 DataPlane，
// 因此所有人可见。禁止修改 server.go：由 Lead 在 buildMux() 内接线。
func Register(mux *http.ServeMux, st *store.Store, cfg *config.Config, authSvc *auth.Service) {
	h := &handler{st: st, cfg: cfg, auth: authSvc}
	corsOrigins := "*"
	if cfg != nil && strings.TrimSpace(cfg.CORSOrigins) != "" {
		corsOrigins = cfg.CORSOrigins
	}
	pub := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn,
			middleware.RequestID,
			middleware.CORS(corsOrigins),
		)
	}
	mux.Handle("GET /api/telemetry/snapshot", pub(h.snapshot))
	mux.Handle("GET /api/telemetry/stream", pub(h.stream))
	mux.Handle("GET /api/telemetry/models", pub(h.models))
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"message":    message,
			"type":       "api_error",
			"code":       code,
			"request_id": middleware.RequestIDFrom(r.Context()),
		},
	})
}

// parseWindow 解析 ?window=60|300|3600 与 ?bucket=10|60，非法值返回 false。
// 允许的桶必须整除窗口，否则桶数与窗口不自洽。
func parseWindow(r *http.Request) (window, bucket int, ok bool) {
	window, bucket = windowSeconds, bucketSeconds
	if v := r.URL.Query().Get("window"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || (n != 60 && n != 300 && n != 3600) {
			return 0, 0, false
		}
		window = n
	}
	if v := r.URL.Query().Get("bucket"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || (n != 10 && n != 60) {
			return 0, 0, false
		}
		bucket = n
	}
	if window%bucket != 0 || window/bucket < 1 || window/bucket > 360 {
		return 0, 0, false
	}
	return window, bucket, true
}

func (h *handler) snapshot(w http.ResponseWriter, r *http.Request) {
	window, bucket, ok := parseWindow(r)
	if !ok {
		writeErr(w, r, http.StatusBadRequest, "invalid_parameter",
			"window must be 60|300|3600 seconds and bucket 10|60 seconds (bucket must divide window)")
		return
	}
	writeJSON(w, http.StatusOK, aggregate(h.st, time.Now(), window, bucket))
}

// stream 用 SSE 每 2 秒推一份真实聚合快照。响应头、逐块 Flush、
// ctx 取消（客户端断开）均在此处理；上游即 store，无需关闭外部连接。
func (h *handler) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, r, http.StatusInternalServerError, "streaming_unsupported",
			"ResponseWriter does not implement http.Flusher")
		return
	}
	maxConns := streamMaxConns
	if v := os.Getenv(streamMaxConnsEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConns = n
		}
	}
	if cur := streamConns.Add(1); cur > int64(maxConns) {
		streamConns.Add(-1)
		writeErr(w, r, http.StatusServiceUnavailable, "too_many_streams",
			"telemetry stream connection limit reached")
		return
	}
	defer streamConns.Add(-1)
	window, bucket, okParams := parseWindow(r)
	if !okParams {
		writeErr(w, r, http.StatusBadRequest, "invalid_parameter",
			"window must be 60|300|3600 seconds and bucket 10|60 seconds (bucket must divide window)")
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache, no-transform")
	hdr.Set("Connection", "keep-alive")
	hdr.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// 首帧立即推送，之后每个 tick 推送；写入失败或 ctx 取消即返回。
	if err := h.writeFrame(w, flusher, window, bucket); err != nil {
		return
	}

	ticker := time.NewTicker(streamInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			select {
			case <-r.Context().Done():
				return
			default:
			}
			if err := h.writeFrame(w, flusher, window, bucket); err != nil {
				return
			}
		}
	}
}

var streamConns atomic.Int64

var (
	snapMu       sync.Mutex
	snapCached   Snapshot
	snapCachedAt time.Time
)

// cachedAggregate 同一 streamInterval 内的所有连接共享同一份聚合：
// 每 interval 至多执行一次全量扫描，防止 N 连接放大成 N 次聚合。
func cachedAggregate(st *store.Store, now time.Time, window, bucket int) Snapshot {
	snapMu.Lock()
	defer snapMu.Unlock()
	if now.Sub(snapCachedAt) < streamInterval && !snapCachedAt.IsZero() &&
		snapCached.WindowSeconds == window && snapCached.BucketSeconds == bucket {
		return snapCached
	}
	snapCached = aggregate(st, now, window, bucket)
	snapCachedAt = now
	return snapCached
}

func (h *handler) writeFrame(w http.ResponseWriter, f http.Flusher, window, bucket int) error {
	b, err := json.Marshal(cachedAggregate(h.st, time.Now(), window, bucket))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", b); err != nil {
		return err
	}
	f.Flush()
	return nil
}

// models GET /api/telemetry/models?window= —— 按真实 model alias 聚合排行。
// 只返回聚合数字（请求数/成功率/平均 TTFT/token），不含任何请求/响应原文。
func (h *handler) models(w http.ResponseWriter, r *http.Request) {
	window, bucket, ok := parseWindow(r)
	if !ok {
		writeErr(w, r, http.StatusBadRequest, "invalid_parameter",
			"window must be 60|300|3600 seconds and bucket 10|60 seconds (bucket must divide window)")
		return
	}
	snap := aggregate(h.st, time.Now(), window, bucket)
	rows := make([]map[string]interface{}, 0, len(snap.Groups.ByModel))
	for _, g := range snap.Groups.ByModel {
		rows = append(rows, map[string]interface{}{
			"model":             g.Key,
			"requests":          g.Requests,
			"success_count":     g.Success,
			"error_count":       g.Errors,
			"success_rate":      g.SuccessRate,
			"avg_ttft_ms":       g.AvgTTFTMs,
			"prompt_tokens":     g.PromptTokens,
			"completion_tokens": g.CompletionTokens,
			"total_tokens":      g.PromptTokens + g.CompletionTokens,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object":         "list",
		"available":      snap.Available,
		"source":         snap.Source,
		"window_seconds": window,
		"bucket_seconds": bucket,
		"generated_at":   snap.GeneratedAt,
		"total_requests": snap.TotalRequests,
		"count":          len(rows),
		"data":           rows,
	})
}
