// Package usage 提供按真实 request_logs 聚合的用量时间序列与按模型排行。
//
// 数据来源（唯一）：store.Store.ListRequestLogs(LogFilter{}) —— 运行时真实
// 调用记录。本包只读，不新增/修改/删除任何日志。
//
// 真实性硬约束：所有请求数 / token / 花费 / 桶点均来自真实日志逐条聚合；
// 一条日志都没有时 available=false、所有数字为真实 0，桶仍按真实时间轴生成，
// 绝不填充任何占位或编造数字。
package usage

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"llm-hub/src/internal/auth"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/middleware"
	"llm-hub/src/internal/relay"
	"llm-hub/src/internal/store"
)

const (
	defaultWindow = 3600 // 默认回溯 60 分钟
	defaultBucket = 60   // 默认每分钟一桶
)

// Point 是时间序列中的一个真实桶。
type Point struct {
	BucketStart      int64   `json:"bucket_start"`
	Label            string  `json:"label"`
	Requests         int64   `json:"requests"`
	SuccessCount     int64   `json:"success_count"`
	ErrorCount       int64   `json:"error_count"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

// ModelRow 是按模型聚合的真实一行。
type ModelRow struct {
	Model            string  `json:"model"`
	Requests         int64   `json:"requests"`
	SuccessCount     int64   `json:"success_count"`
	ErrorCount       int64   `json:"error_count"`
	SuccessRate      float64 `json:"success_rate"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

type handler struct {
	st *store.Store
}

// Register 注册用量端点（管理面 JWT，复用既有约定）。
func Register(mux *http.ServeMux, st *store.Store, cfg *config.Config, authSvc *auth.Service) {
	h := &handler{st: st}
	corsOrigins := "*"
	if cfg != nil && strings.TrimSpace(cfg.CORSOrigins) != "" {
		corsOrigins = cfg.CORSOrigins
	}
	user := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn,
			middleware.RequestID,
			middleware.CORS(corsOrigins),
			middleware.Management(authSvc),
		)
	}
	mux.Handle("GET /api/usage/series", user(h.series))
	mux.Handle("GET /api/usage/by-model", user(h.byModel))
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = jsonEncode(w, v)
}

func fail(w http.ResponseWriter, r *http.Request, status int, errType, code, message string) {
	relay.WriteJSONError(w, status, errType, code, message, middleware.RequestIDFrom(r.Context()))
}

// parseWindow 解析 ?window=60|300|3600 与 ?bucket=10|60（桶必须整除窗口）。
func parseWindow(r *http.Request) (window, bucket int, ok bool) {
	window, bucket = defaultWindow, defaultBucket
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

func (h *handler) series(w http.ResponseWriter, r *http.Request) {
	window, bucket, ok := parseWindow(r)
	if !ok {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_parameter",
			"window must be 60|300|3600 seconds and bucket 10|60 seconds (bucket must divide window)")
		return
	}
	if h.st == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"object": "list", "available": false,
			"window_seconds": window, "bucket_seconds": bucket,
			"count": 0, "data": []Point{},
		})
		return
	}
	now := time.Now()
	logs, _ := h.st.ListRequestLogs(store.LogFilter{})
	nowSec := now.Unix()
	nowBucket := nowSec - nowSec%int64(bucket)
	start := nowBucket - int64(window/bucket-1)*int64(bucket)

	pts := make([]Point, 0, window/bucket)
	idx := make(map[int64]int, window/bucket)
	for i := 0; i < window/bucket; i++ {
		b := start + int64(i)*int64(bucket)
		idx[b] = len(pts)
		pts = append(pts, Point{
			BucketStart: b,
			Label:       time.Unix(b, 0).Format("15:04"),
		})
	}
	for _, l := range logs {
		if l == nil || l.CreatedAt < start {
			continue
		}
		b := l.CreatedAt - l.CreatedAt%int64(bucket)
		i, ok := idx[b]
		if !ok {
			continue
		}
		p := &pts[i]
		p.Requests++
		if l.StatusCode >= 200 && l.StatusCode < 400 {
			p.SuccessCount++
		} else {
			p.ErrorCount++
		}
		p.PromptTokens += int64(l.PromptTokens)
		p.CompletionTokens += int64(l.CompletionTokens)
		p.CostUSD += l.CostUSD
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object":         "list",
		"available":      len(logs) > 0,
		"source":         "store.request_logs/ListRequestLogs (real request_logs aggregate)",
		"window_seconds": window,
		"bucket_seconds": bucket,
		"generated_at":   nowSec,
		"count":          len(pts),
		"data":           pts,
	})
}

func (h *handler) byModel(w http.ResponseWriter, r *http.Request) {
	if h.st == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"object": "list", "available": false, "count": 0, "data": []ModelRow{},
		})
		return
	}
	logs, _ := h.st.ListRequestLogs(store.LogFilter{})
	agg := make(map[string]*ModelRow, 64)
	for _, l := range logs {
		if l == nil {
			continue
		}
		key := l.Alias
		if key == "" {
			key = l.ModelID
		}
		if key == "" {
			key = "(unknown)"
		}
		row := agg[key]
		if row == nil {
			row = &ModelRow{Model: key}
			agg[key] = row
		}
		row.Requests++
		if l.StatusCode >= 200 && l.StatusCode < 400 {
			row.SuccessCount++
		} else {
			row.ErrorCount++
		}
		row.PromptTokens += int64(l.PromptTokens)
		row.CompletionTokens += int64(l.CompletionTokens)
		row.CostUSD += l.CostUSD
	}
	rows := make([]ModelRow, 0, len(agg))
	for _, row := range agg {
		row.TotalTokens = row.PromptTokens + row.CompletionTokens
		if row.Requests > 0 {
			row.SuccessRate = float64(row.SuccessCount) / float64(row.Requests)
		}
		rows = append(rows, *row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Requests != rows[j].Requests {
			return rows[i].Requests > rows[j].Requests
		}
		return rows[i].Model < rows[j].Model
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object":       "list",
		"available":    len(logs) > 0,
		"source":       "store.request_logs/ListRequestLogs (real request_logs aggregate)",
		"generated_at": time.Now().Unix(),
		"count":        len(rows),
		"data":         rows,
	})
}
