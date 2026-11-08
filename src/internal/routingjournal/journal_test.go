package routingjournal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleRecord(t *testing.T, model, provider string, tried int) Record {
	t.Helper()
	tries := make([]Try, 0, tried)
	for i := 0; i < tried; i++ {
		st := 200
		if i < tried-1 {
			st = 503
		}
		tries = append(tries, Try{
			ChannelID: "ch-" + string(rune('a'+i)), Model: model, Start: time.Now().Format(time.RFC3339),
			Done: true, Status: st, Ms: 100 * (i + 1),
		})
	}
	aff, _ := json.Marshal(map[string]any{"kept": true, "why": "cache", "turn": 82})
	msk, _ := json.Marshal(map[string]any{"n": 7, "distinct": 3})
	prm, _ := json.Marshal(map[string]any{"window": 1000000, "tokens": 516345, "counted": false})
	return Record{
		Time: "2026-11-08T10:00:00+08:00", Key: "req-1", Agent: "dsh",
		Entry: "/v1/chat/completions", Model: model, Provider: provider,
		Group: GroupSnapshot{ID: "auto-dfmodel", Ref: "auto/dfmodel", Name: "DeepSeek V4.1 flash",
			Members: []string{"a/m", "b/m"}, Why: "rule", Rules: []string{"use=c/m3 effort=high"}},
		Order: []OrderEntry{{Rank: 1, ChannelID: "ch-a", Model: model}, {Rank: 2, ChannelID: "ch-b", Model: model}},
		Tries: tries, Status: 200, Ms: 120, Conv: "magpie-abc", ResponseID: "chatcmpl-1",
		Affinity: aff, Masking: msk, Prompt: prm, Tokens: 516457, Out: 112, TTFT: 5993,
	}
}

// TestAppendAndQuery 写入后可按日读回，字段完整（含 order/tries/affinity/masking/prompt）。
func TestAppendAndQuery(t *testing.T) {
	dir := t.TempDir()
	j := New(dir)
	if _, err := j.Append(sampleRecord(t, "auto/dfmodel", "commandcode", 2)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	recs, err := j.Query("2026-11-08", 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("应读回 1 条，实际 %d", len(recs))
	}
	r := recs[0]
	if r.Seq != 1 {
		t.Fatalf("Seq 应从 1 开始，实际 %d", r.Seq)
	}
	if len(r.Order) != 2 || len(r.Tries) != 2 {
		t.Fatalf("order/tries 必须完整留痕: order=%d tries=%d", len(r.Order), len(r.Tries))
	}
	if r.Tries[0].Status != 503 || r.Tries[1].Status != 200 {
		t.Fatalf("尝试链状态应逐次记录: %+v", r.Tries)
	}
	if r.Group.Ref != "auto/dfmodel" || len(r.Group.Members) != 2 || r.Group.Why != "rule" {
		t.Fatalf("分组快照应完整: %+v", r.Group)
	}
	if len(r.Affinity) == 0 || len(r.Masking) == 0 || len(r.Prompt) == 0 {
		t.Fatal("亲和/脱敏/上下文三类统计都必须在同一条记录里串联")
	}
}

// TestSeqMonotonic Seq 单调递增，便于同日文件内排序。
func TestSeqMonotonic(t *testing.T) {
	j := New(t.TempDir())
	var last int64
	for i := 0; i < 5; i++ {
		s, err := j.Append(sampleRecord(t, "m", "p", 1))
		if err != nil {
			t.Fatal(err)
		}
		if s <= last {
			t.Fatalf("Seq 应递增: %d 之后是 %d", last, s)
		}
		last = s
	}
}

// TestDailySharding 按日分片，不同日期进不同文件。
func TestDailySharding(t *testing.T) {
	dir := t.TempDir()
	j := New(dir)
	for _, day := range []string{"2026-11-06", "2026-11-07", "2026-11-08", "2026-11-08"} {
		rec := sampleRecord(t, "m", "p", 1)
		rec.Time = day + "T10:00:00+08:00"
		if _, err := j.Append(rec); err != nil {
			t.Fatal(err)
		}
	}
	days, err := j.Days()
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 3 {
		t.Fatalf("应有 3 个分片，实际 %v", days)
	}
	if days[0] != "2026-11-08" {
		t.Fatalf("分片应按日期倒序: %v", days)
	}
	r8, _ := j.Query("2026-11-08", 0)
	if len(r8) != 2 {
		t.Fatalf("11-08 应有 2 条，实际 %d", len(r8))
	}
}

// TestPermissions 目录 0700、文件 0600。
func TestPermissions(t *testing.T) {
	dir := t.TempDir()
	j := New(dir)
	if _, err := j.Append(sampleRecord(t, "m", "p", 1)); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(j.Dir()); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("日志目录应为 0700，实际 %v err=%v", st.Mode().Perm(), err)
	}
	p := filepath.Join(j.Dir(), "2026-11-08.jsonl")
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("日志文件应为 0600，实际 %v err=%v", st.Mode().Perm(), err)
	}
}

// TestQueryMissingDayIsEmpty 当天没有决策时返回空集（不是错误）。
func TestQueryMissingDayIsEmpty(t *testing.T) {
	j := New(t.TempDir())
	recs, err := j.Query("2000-01-01", 0)
	if err != nil {
		t.Fatalf("缺分片不应报错: %v", err)
	}
	if len(recs) != 0 {
		t.Fatal("应为空")
	}
}

// TestQueryLimitReturnsLatest limit 取最近 N 条。
func TestQueryLimitReturnsLatest(t *testing.T) {
	j := New(t.TempDir())
	for i := 0; i < 10; i++ {
		rec := sampleRecord(t, "m", "p", 1)
		rec.Key = "req-" + string(rune('0'+i))
		if _, err := j.Append(rec); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := j.Query("2026-11-08", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("应返回 3 条，实际 %d", len(recs))
	}
	if recs[2].Key != "req-9" {
		t.Fatalf("应取最近记录，实际最后一条 %s", recs[2].Key)
	}
}

// TestArchiveAndReadArchived 归档为 gz 且可透明读回，内容不丢。
func TestArchiveAndReadArchived(t *testing.T) {
	j := New(t.TempDir())
	for _, day := range []string{"2026-11-05", "2026-11-06", "2026-11-08"} {
		rec := sampleRecord(t, "m", "p", 2)
		rec.Time = day + "T10:00:00+08:00"
		if _, err := j.Append(rec); err != nil {
			t.Fatal(err)
		}
	}
	n, err := j.Archive(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if n != 2 {
		t.Fatalf("应归档 2 个分片，实际 %d", n)
	}
	if _, err := os.Stat(filepath.Join(j.Dir(), "2026-11-05.jsonl")); !os.IsNotExist(err) {
		t.Fatal("归档后明文应被删除")
	}
	if _, err := os.Stat(filepath.Join(j.Dir(), "2026-11-08.jsonl")); err != nil {
		t.Fatal("当天分片必须保留明文")
	}
	recs, err := j.ReadArchived("2026-11-05", 0)
	if err != nil {
		t.Fatalf("ReadArchived: %v", err)
	}
	if len(recs) != 1 || len(recs[0].Tries) != 2 {
		t.Fatalf("归档内容应完整: %+v", recs)
	}
	// 归档文件权限仍是 0600
	if st, err := os.Stat(filepath.Join(j.Dir(), "2026-11-05.jsonl.gz")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("归档文件应为 0600，实际 %v err=%v", st.Mode().Perm(), err)
	}
}

// TestCorruptLineSkipped 损坏行被跳过，不影响其余记录（单行问题不丢整批）。
func TestCorruptLineSkipped(t *testing.T) {
	dir := t.TempDir()
	j := New(dir)
	if _, err := j.Append(sampleRecord(t, "m", "p", 1)); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(j.Dir(), "2026-11-08.jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{ this is not json\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err := j.Append(sampleRecord(t, "m", "p", 1)); err != nil {
		t.Fatal(err)
	}
	recs, err := j.Query("2026-11-08", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("应保留 2 条有效记录，实际 %d", len(recs))
	}
}

// TestSummarizeRealStats 汇总统计真实：状态/渠道/失败切换/亲和/脱敏/token。
func TestSummarizeRealStats(t *testing.T) {
	recs := []Record{
		sampleRecord(t, "auto/dfmodel", "commandcode", 1),
		sampleRecord(t, "auto/dfmodel", "trae-cn", 3), // 有失败切换
		sampleRecord(t, "auto/flash", "zcode", 1),
	}
	recs[0].Status = 503
	st := Summarize("2026-11-08", recs)

	if st.Total != 3 {
		t.Fatalf("总数应为 3，实际 %d", st.Total)
	}
	if st.ByStatus["503"] != 1 || st.ByStatus["200"] != 2 {
		t.Fatalf("状态分布不符: %v", st.ByStatus)
	}
	if st.ByProvider["commandcode"] != 1 || st.ByProvider["trae-cn"] != 1 {
		t.Fatalf("渠道分布不符: %v", st.ByProvider)
	}
	if st.ByAgent["dsh"] != 3 {
		t.Fatalf("调用方分布不符: %v", st.ByAgent)
	}
	if st.FailoverRuns != 1 {
		t.Fatalf("失败切换请求数应为 1，实际 %d", st.FailoverRuns)
	}
	if st.TotalTries != 5 {
		t.Fatalf("总尝试数应为 5，实际 %d", st.TotalTries)
	}
	if st.AffinityKept != 3 {
		t.Fatalf("亲和命中应为 3，实际 %d", st.AffinityKept)
	}
	if st.MaskedTotal != 21 {
		t.Fatalf("脱敏总数应为 21，实际 %d", st.MaskedTotal)
	}
	if st.TotalTokens != 3*516457 {
		t.Fatalf("token 总量不符: %d", st.TotalTokens)
	}
	if st.AvgMs <= 0 || st.P95Ms <= 0 {
		t.Fatalf("延迟统计应真实: avg=%d p95=%d", st.AvgMs, st.P95Ms)
	}
}

// TestSummarizeEmpty 空输入返回零值而不是 NaN。
func TestSummarizeEmpty(t *testing.T) {
	st := Summarize("2026-11-08", nil)
	if st.Total != 0 || st.AvgMs != 0 || st.P95Ms != 0 {
		t.Fatalf("空输入应返回零值: %+v", st)
	}
}

// TestRecordRoundTripKeepsRawStats 三类 JSON 统计原样往返，不被重编码损坏。
func TestRecordRoundTripKeepsRawStats(t *testing.T) {
	j := New(t.TempDir())
	if _, err := j.Append(sampleRecord(t, "m", "p", 1)); err != nil {
		t.Fatal(err)
	}
	recs, _ := j.Query("2026-11-08", 0)
	var aff map[string]any
	if err := json.Unmarshal(recs[0].Affinity, &aff); err != nil {
		t.Fatalf("affinity 应可解析: %v", err)
	}
	if aff["why"] != "cache" {
		t.Fatalf("affinity 内容不符: %v", aff)
	}
	if !strings.Contains(string(recs[0].Prompt), "516345") {
		t.Fatalf("prompt 内容不符: %s", recs[0].Prompt)
	}
}
