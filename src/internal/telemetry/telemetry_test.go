// 文件: src/internal/telemetry/telemetry_test.go
// 用真实写入 store 的 request_logs 验证聚合、空态、public 端点与隐私约束。
package telemetry

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"llm-hub/src/internal/config"
	"llm-hub/src/internal/store"
)

func seedStore(t *testing.T, now time.Time) *store.Store {
	t.Helper()
	st, err := store.New("") // 纯内存
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	base := now.Unix()
	// 10 条真实记录：跨两个供应商 / 三个别名 / 两个渠道 / 多种状态 / 分钟分布。
	rows := []struct {
		alias, channel, provider string
		status                   int
		prompt, completion, ttft int
		stream                   bool
		offsetSec                int64
	}{
		{"model-a", "ch-1", "openai", 200, 100, 50, 120, true, 5},
		{"model-a", "ch-1", "openai", 200, 200, 80, 200, true, 15},
		{"model-a", "ch-1", "openai", 500, 10, 0, 0, false, 25},
		{"model-b", "ch-2", "anthropic", 200, 300, 120, 300, false, 35},
		{"model-b", "ch-2", "anthropic", 429, 20, 0, 0, false, 45},
		{"model-b", "ch-2", "anthropic", 200, 0, 0, 0, false, 55},
		{"model-c", "ch-1", "openai", 200, 50, 25, 90, true, 70},
		{"model-c", "ch-1", "openai", 200, 50, 25, 110, true, 130},
		{"model-c", "ch-1", "openai", 0, 5, 0, 0, false, 200},
		{"model-a", "ch-2", "anthropic", 200, 10, 5, 0, false, 4000}, // 窗口外（>1h）
	}
	for i, r := range rows {
		st.AddRequestLog(&store.RequestLog{
			ID:               "log_" + string(rune('a'+i)),
			RequestID:        "req_" + string(rune('a'+i)),
			Alias:            r.alias,
			ModelID:          "mdl_" + r.alias,
			ChannelID:        r.channel,
			ProviderType:     r.provider,
			Stream:           r.stream,
			RequestBody:      json.RawMessage(`{"messages":[{"role":"user","content":"SECRET-INPUT"}]}`),
			ResponseBody:     "SECRET-OUTPUT sk-abcdefgh12345678",
			PromptTokens:     r.prompt,
			CompletionTokens: r.completion,
			TTFTMs:           r.ttft,
			StatusCode:       r.status,
			ClientIP:         "203.0.113.9",
			CreatedAt:        base - r.offsetSec,
		})
	}
	return st
}

func TestAggregateRealNumbers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0) // 固定时间保证确定性
	st := seedStore(t, now)
	s := aggregate(st, now, windowSeconds, bucketSeconds)

	if !s.Available {
		t.Fatal("有日志时 Available 应为 true")
	}
	if s.TotalRequests != 10 {
		t.Fatalf("TotalRequests=%d want 10", s.TotalRequests)
	}
	// 成功 = 2xx：200 的共 7 条（a,b,d,f,g,h,j）；500/429/0 共 3 条。
	if s.SuccessCount != 7 || s.ErrorCount != 3 {
		t.Fatalf("Success=%d Error=%d want 7/3", s.SuccessCount, s.ErrorCount)
	}
	if s.SuccessRate != 70 {
		t.Fatalf("SuccessRate=%v want 70", s.SuccessRate)
	}
	// TTFT>0 样本：120,200,300,90,110 => sum=820 cnt=5 avg=164 max=300
	if s.TTFTSampleCount != 5 || s.AvgTTFTMs != 164 || s.MaxTTFTMs != 300 {
		t.Fatalf("TTFT samples=%d avg=%v max=%d want 5/164/300", s.TTFTSampleCount, s.AvgTTFTMs, s.MaxTTFTMs)
	}
	// prompt 合计 100+200+10+300+20+0+50+50+5+10=745
	if s.PromptTokens != 745 {
		t.Fatalf("PromptTokens=%d want 745", s.PromptTokens)
	}
	// completion 合计 50+80+0+120+0+0+25+25+0+5=305
	if s.CompletionTokens != 305 || s.TotalTokens != 1050 {
		t.Fatalf("Completion=%d Total=%d want 305/1050", s.CompletionTokens, s.TotalTokens)
	}
	if s.StreamRequests != 4 {
		t.Fatalf("StreamRequests=%d want 4", s.StreamRequests)
	}
	// QPS：最近 60s 内（offset<=60）共 6 条 => 6/60=0.1
	if s.QPS != 0.1 {
		t.Fatalf("QPS=%v want 0.1", s.QPS)
	}
	// 60 个桶，长度恒定；窗口内 9 条（第 10 条 offset=4000 在窗口外）
	totalInSeries := int64(0)
	for _, b := range s.Series {
		totalInSeries += b.Requests
	}
	if len(s.Series) != 60 {
		t.Fatalf("Series len=%d want 60", len(s.Series))
	}
	if totalInSeries != 9 {
		t.Fatalf("series requests=%d want 9", totalInSeries)
	}
	// 分组维度：model 3 类、channel 2 类、provider 2 类、status 4 类(200/500/429/0)
	if len(s.Groups.ByModel) != 3 {
		t.Fatalf("ByModel=%d want 3", len(s.Groups.ByModel))
	}
	if len(s.Groups.ByChannel) != 2 || len(s.Groups.ByProvider) != 2 {
		t.Fatalf("ByChannel=%d ByProvider=%d want 2/2", len(s.Groups.ByChannel), len(s.Groups.ByProvider))
	}
	if len(s.Groups.ByStatus) != 4 {
		t.Fatalf("ByStatus=%d want 4", len(s.Groups.ByStatus))
	}
	// model-a 4 条应为 model 维度第一（降序）
	if s.Groups.ByModel[0].Key != "model-a" || s.Groups.ByModel[0].Requests != 4 {
		t.Fatalf("ByModel[0]=%+v want model-a/4", s.Groups.ByModel[0])
	}
}

func TestAggregateEmptyState(t *testing.T) {
	st, _ := store.New("")
	s := aggregate(st, time.Now(), windowSeconds, bucketSeconds)
	if s.Available {
		t.Fatal("无日志时 Available 应为 false（真实空态）")
	}
	if s.TotalRequests != 0 || s.QPS != 0 || s.SuccessRate != 0 ||
		s.AvgTTFTMs != 0 || s.MaxTTFTMs != 0 || s.PromptTokens != 0 ||
		s.CompletionTokens != 0 || s.TotalTokens != 0 || s.PeakQPS != 0 {
		t.Fatalf("空态数字应全为 0: %+v", s)
	}
	if len(s.Series) != 60 {
		t.Fatalf("空态仍应有 60 个零值桶, got %d", len(s.Series))
	}
	for _, b := range s.Series {
		if b.Requests != 0 || b.QPS != 0 || b.SuccessRate != 0 || b.AvgTTFTMs != 0 {
			t.Fatalf("空态桶应为全 0: %+v", b)
		}
	}
	for _, g := range [][]GroupStat{s.Groups.ByModel, s.Groups.ByChannel, s.Groups.ByProvider, s.Groups.ByStatus} {
		if len(g) != 0 {
			t.Fatal("空态分组应为空")
		}
	}
}

func TestSnapshotPublicAndNoLeak(t *testing.T) {
	st := seedStore(t, time.Now())
	mux := http.NewServeMux()
	Register(mux, st, &config.Config{CORSOrigins: "*"}, nil) // auth=nil 亦可用（public）

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/telemetry/snapshot", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot status=%d want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type=%q", ct)
	}
	// public：无需任何 Authorization 即可访问（上面请求未带 token 且返回 200）
	body := rec.Body.String()
	for _, leak := range []string{
		"SECRET-INPUT", "SECRET-OUTPUT", "sk-abcdefgh12345678",
		"request_body", "response_body", "password_hash", "token_hash",
		"203.0.113.9", "req_a", "api_key",
	} {
		if strings.Contains(body, leak) {
			t.Fatalf("snapshot 泄露敏感字段 %q", leak)
		}
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(body), &snap); err != nil {
		t.Fatalf("snapshot 非法 JSON: %v", err)
	}
	if !snap.Available || snap.TotalRequests != 10 {
		t.Fatalf("snapshot 聚合值不对: available=%v total=%d", snap.Available, snap.TotalRequests)
	}
	// CORS 头由 middleware.CORS 注入
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("缺少 CORS 头")
	}
}

// TestStreamSSEHeadersAndFirstFrame 用真实 httptest.Server 走真实 socket 验证 SSE：
// header 正确、首帧立即推送（而不是等一个 tick）、ctx 取消后连接即断、且不泄露敏感字段。
// 早期版本用 goroutine 写 ResponseRecorder + 主线程轮询 rec.Body，
// 而 ResponseRecorder 明确不是并发安全的，那本身就是数据竞争，故改为真实连接。
func TestStreamSSEHeadersAndFirstFrame(t *testing.T) {
	st := seedStore(t, time.Now())
	mux := http.NewServeMux()
	Register(mux, st, &config.Config{CORSOrigins: "*"}, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/telemetry/stream", nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	start := time.Now()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/telemetry/stream: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("stream status=%d want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type=%q want text/event-stream", ct)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Fatalf("Cache-Control=%q", cc)
	}
	if res.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal("缺少 X-Accel-Buffering: no")
	}

	// 逐行读，直到凑出完整首帧。首帧必须在远小于 streamInterval 的时间内到达，
	// 否则说明服务端把 SSE 整体缓冲了。
	br := bufio.NewReader(res.Body)
	var out strings.Builder
	firstFrameAt := time.Duration(0)
	for time.Since(start) < 3*time.Second {
		line, rerr := br.ReadString('\n')
		if len(line) > 0 {
			out.WriteString(line)
			if strings.Count(out.String(), "\n") >= 2 &&
				strings.Contains(out.String(), "data: {") {
				firstFrameAt = time.Since(start)
				break
			}
		}
		if rerr != nil {
			t.Fatalf("读取 SSE 首帧失败: %v; 已读到=%q", rerr, out.String())
		}
	}
	if firstFrameAt == 0 {
		t.Fatalf("3s 内未读到完整首帧: %q", out.String())
	}
	if firstFrameAt >= streamInterval {
		t.Fatalf("首帧耗时 %v 未小于推送间隔 %v，疑似被缓冲", firstFrameAt, streamInterval)
	}
	t.Logf("首帧真实到达耗时=%v（推送间隔=%v）", firstFrameAt, streamInterval)

	if !strings.Contains(out.String(), "event: snapshot") || !strings.Contains(out.String(), "data: {") {
		t.Fatalf("缺少 SSE 快照帧: %q", out.String())
	}
	for _, leak := range []string{"SECRET-INPUT", "SECRET-OUTPUT", "sk-abcdefgh12345678"} {
		if strings.Contains(out.String(), leak) {
			t.Fatalf("SSE 帧泄露敏感字段 %q", leak)
		}
	}

	// 取消 ctx 后，服务端应停止推送且客户端读立即失败。
	cancel()
	readDone := make(chan error, 1)
	go func() {
		_, rerr := br.ReadString('\n')
		readDone <- rerr
	}()
	select {
	case <-readDone:
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后读未返回，服务端可能仍在推送")
	}
}

// TestEndToEndSnapshotJSON 起一个真实 httptest.Server，通过真实 HTTP GET
// 打印聚合快照，证明端点对外只输出真实聚合数字、且不含任何敏感字段。
func TestEndToEndSnapshotJSON(t *testing.T) {
	st := seedStore(t, time.Now())
	mux := http.NewServeMux()
	Register(mux, st, &config.Config{CORSOrigins: "*"}, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/telemetry/snapshot")
	if err != nil {
		t.Fatalf("GET snapshot: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	t.Logf("HTTP %d Content-Type=%q", res.StatusCode, res.Header.Get("Content-Type"))
	t.Logf("snapshot JSON (real aggregate of seeded request_logs): %s", string(body))

	var snap Snapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !snap.Available || snap.TotalRequests != 10 || len(snap.Series) != 60 {
		t.Fatalf("unexpected aggregate: available=%v total=%d series=%d", snap.Available, snap.TotalRequests, len(snap.Series))
	}
}

// TestParseWindow 校验 window/bucket 参数白名单与整除约束（真实 HTTP 层）。
func TestParseWindow(t *testing.T) {
	cases := []struct {
		q      string
		window int
		bucket int
		ok     bool
	}{
		{"", 3600, 60, true},
		{"?window=60", 60, 60, true},
		{"?window=300", 300, 60, true},
		{"?window=60&bucket=10", 60, 10, true},
		{"?window=3600&bucket=10", 3600, 10, true},
		{"?window=120", 0, 0, false},
		{"?window=abc", 0, 0, false},
		{"?bucket=30", 0, 0, false},
		{"?window=60&bucket=60", 60, 60, true},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/api/telemetry/snapshot"+c.q, nil)
		w, b, ok := parseWindow(r)
		if ok != c.ok {
			t.Fatalf("%q: ok=%v want %v", c.q, ok, c.ok)
		}
		if ok && (w != c.window || b != c.bucket) {
			t.Fatalf("%q: got window=%d bucket=%d want %d/%d", c.q, w, b, c.window, c.bucket)
		}
	}
}

// TestSnapshotRejectsInvalidWindow 非法参数必须 400，不得静默回退。
func TestSnapshotRejectsInvalidWindow(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DataDir: dir, CORSOrigins: "*"}
	mux := http.NewServeMux()
	Register(mux, st, cfg, nil)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/telemetry/snapshot?window=999", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 window 应 400，实际 %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/telemetry/snapshot?window=60&bucket=10", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("合法参数应 200，实际 %d", w.Code)
	}
}

// TestModelsEndpointRealAggregate /api/telemetry/models 返回真实聚合结构。
func TestModelsEndpointRealAggregate(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DataDir: dir, CORSOrigins: "*"}
	mux := http.NewServeMux()
	Register(mux, st, cfg, nil)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/telemetry/models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("models status=%d %s", w.Code, w.Body.String())
	}
	var out struct {
		Object        string                   `json:"object"`
		Available     bool                     `json:"available"`
		WindowSeconds int                      `json:"window_seconds"`
		BucketSeconds int                      `json:"bucket_seconds"`
		Count         int                      `json:"count"`
		Data          []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Object != "list" || out.WindowSeconds != 3600 || out.BucketSeconds != 60 {
		t.Fatalf("响应结构不符: %+v", out)
	}
	// 无真实调用时必须 available=false 且 data 为空数组（真实空态，不编造）
	if out.Available {
		t.Fatalf("无调用时 available 应为 false")
	}
	if out.Count != 0 || len(out.Data) != 0 {
		t.Fatalf("无调用时不应有模型行: %+v", out.Data)
	}
}
