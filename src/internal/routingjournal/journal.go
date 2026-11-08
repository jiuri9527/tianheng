// Package routingjournal 原生实现「路由决策全量留痕」原理。
//
// ── 原理来源 ────────────────────────────────────────────────────────────────
// 取自 AI 供应商聚合层 Magpie 的 routing/ 目录：按日分片的 JSONL，历史分片 gzip
// 归档（routing/2026-11-07.jsonl.gz、routing/2026-11-08.jsonl）。每条记录把
// **一次路由决策的全部事实**写下来：
//
//	{ seq, id, time, agent, model, effort, provider,
//	  group:{id,ref,name,routing,affinity,members[],subs[]},
//	  order:[候选排序], tries:[{id,model,effort,start,done,status,ms,ttft}],
//	  status, ms, masking, masked{...}, bytes, conv, affinity{...},
//	  served, left[], flow, estTok, tokens, out, ttft, prompt{...}, responseId }
//
// 提炼出的原理：
//  1. **决策可复盘**：不只记「最后用了谁」，还记**候选顺序 order** 与**每次尝试
//     tries**（含每次的耗时/状态/TTFT）。出现线上问题时能直接回答「当时为什么
//     选它、失败了几次、每次多久」。
//  2. **按日分片 + 压缩归档**：当天写明文方便实时排查，历史自动 gzip，成本可控。
//  3. **与其它子系统互相串联**：同一记录里同时带 affinity（粘性判定）、masking
//     （脱敏统计）、prompt（上下文构成），用同一个 key 串起——这正是天衡
//     把 affinity/redact/ctxbreakdown 三个原语接进来的落点。
//
// ── 天衡的落法 ──────────────────────────────────────────────────────────────
// 纯标准库、零依赖；写入用 O_APPEND 单次系统调用（并发安全），归档用标准库
// compress/gzip。日志目录 0700、文件 0600。
package routingjournal

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"llm-hub/src/internal/fsutil"
)

// OrderEntry 是一个候选及其被选中的依据（对应原理来源的 order[]）。
type OrderEntry struct {
	Rank       int    `json:"rank"`
	ChannelID  string `json:"channel_id"`
	ChannelNm  string `json:"channel_name,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	Weight     int    `json:"weight,omitempty"`
	Priority   int    `json:"priority,omitempty"`
	InFlight   int64  `json:"in_flight,omitempty"`
	Note       string `json:"note,omitempty"` // 例如 "pinned-by-rule" / "affinity"
}

// Try 是一次真实的上游尝试（对应原理来源的 tries[]）。
type Try struct {
	ChannelID string `json:"channel_id"`
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Start     string `json:"start"`
	Done      bool   `json:"done"`
	Status    int    `json:"status"`
	Ms        int    `json:"ms"`
	TTFTms    int    `json:"ttft,omitempty"`
	Err       string `json:"err,omitempty"`
}

// GroupSnapshot 是决策时刻的分组快照（对应原理来源的 group{}）。
// 记录当时的成员列表，避免分组日后被改动导致历史无法复盘。
type GroupSnapshot struct {
	ID       string   `json:"id,omitempty"`
	Ref      string   `json:"ref,omitempty"`
	Name     string   `json:"name,omitempty"`
	Affinity string   `json:"affinity,omitempty"`
	Members  []string `json:"members,omitempty"`
	Rules    []string `json:"rules_applied,omitempty"`
	Why      string   `json:"why,omitempty"`
}

// Record 是一条完整的路由决策记录。
type Record struct {
	Seq      int64  `json:"seq"`
	ID       int64  `json:"id"`
	Time     string `json:"time"`
	Key      string `json:"key,omitempty"`      // 请求关联键
	Agent    string `json:"agent,omitempty"`    // 调用方（UA 归一后）
	Entry    string `json:"entrypoint,omitempty"` // /v1/chat/completions 等
	Model    string `json:"model"`              // 逻辑模型名（分组 ref 或裸模型）
	Effort   string `json:"effort,omitempty"`
	Provider string `json:"provider,omitempty"` // 最终选中的渠道类型
	Channel  string `json:"channel_id,omitempty"`

	Group GroupSnapshot `json:"group"`
	Order []OrderEntry  `json:"order"`
	Tries []Try         `json:"tries"`

	Status     int    `json:"status"`
	Ms         int    `json:"ms"`
	Stream     bool   `json:"flow"`
	Conv       string `json:"conv,omitempty"`
	ResponseID string `json:"response_id,omitempty"`

	// 与其它子系统串联的真实统计
	Affinity json.RawMessage `json:"affinity,omitempty"`
	Masking  json.RawMessage `json:"masking,omitempty"`
	Prompt   json.RawMessage `json:"prompt,omitempty"`

	Bytes  int `json:"bytes,omitempty"`
	EstTok int `json:"est_tok,omitempty"`
	Tokens int `json:"tokens,omitempty"`
	Out    int `json:"out,omitempty"`
	TTFT   int `json:"ttft,omitempty"`
}

// Journal 按日分片的路由日志。
type Journal struct {
	dir string
	mu  sync.Mutex
	seq int64
	maxBytesPerFile int64
}

// New 构造日志器，写入 <dataDir>/routing/。
func New(dataDir string) *Journal {
	return &Journal{dir: filepath.Join(dataDir, "routing"), maxBytesPerFile: 256 << 20}
}

// Dir 返回日志目录（供运维核对权限）。
func (j *Journal) Dir() string { return j.dir }

// Append 追加一条记录。Seq 由日志器单调分配，保证同日文件内可排序。
func (j *Journal) Append(rec Record) (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq++
	rec.Seq = j.seq
	if rec.ID == 0 {
		rec.ID = time.Now().UnixNano()
	}
	if rec.Time == "" {
		rec.Time = time.Now().Format(time.RFC3339Nano)
	}
	if rec.Order == nil {
		rec.Order = []OrderEntry{}
	}
	if rec.Tries == nil {
		rec.Tries = []Try{}
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return 0, fmt.Errorf("routingjournal: marshal: %w", err)
	}
	path := j.dayPath(rec.Time)
	if err := os.MkdirAll(j.dir, 0o700); err != nil {
		return 0, err
	}
	if err := os.Chmod(j.dir, 0o700); err != nil {
		return 0, err
	}
	if err := fsutil.AppendLine(path, b); err != nil {
		return 0, err
	}
	return rec.Seq, nil
}

func (j *Journal) dayPath(t string) string {
	day := t
	if len(day) >= 10 {
		day = day[:10]
	} else {
		day = time.Now().Format("2006-01-02")
	}
	return filepath.Join(j.dir, day+".jsonl")
}

// Query 读取某天的记录（day 形如 2026-11-08；空表示今天）。
// limit<=0 表示不限制。按写入顺序返回。
func (j *Journal) Query(day string, limit int) ([]Record, error) {
	if day == "" {
		day = time.Now().Format("2006-01-02")
	}
	path := filepath.Join(j.dir, day+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Record{}, nil // 当天还没有任何决策，属正常
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	out := []Record{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20) // 单条记录上限 8 MiB
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if json.Unmarshal([]byte(line), &r) != nil {
			continue // 跳过损坏行，不因单行问题丢掉整批
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:] // 取最近 limit 条
	}
	return out, nil
}

// Days 列出已有分片日期（含已归档的 .gz），按日期倒序。
func (j *Journal) Days() ([]string, error) {
	ents, err := os.ReadDir(j.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	set := map[string]bool{}
	for _, e := range ents {
		n := e.Name()
		switch {
		case strings.HasSuffix(n, ".jsonl"):
			set[strings.TrimSuffix(n, ".jsonl")] = true
		case strings.HasSuffix(n, ".jsonl.gz"):
			set[strings.TrimSuffix(n, ".jsonl.gz")] = true
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

// Archive 把 before 之前的明文分片压缩为 .gz 并删除原文件，返回归档数量。
// before 当天的分片不归档（当天要留明文便于实时排查）。
func (j *Journal) Archive(before time.Time) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	days, err := j.Days()
	if err != nil {
		return 0, err
	}
	cut := before.Format("2006-01-02")
	n := 0
	for _, d := range days {
		if d >= cut {
			continue
		}
		src := filepath.Join(j.dir, d+".jsonl")
		if _, err := os.Stat(src); err != nil {
			continue // 已是 .gz 或不存在
		}
		if err := gzipFile(src, src+".gz"); err != nil {
			return n, fmt.Errorf("routingjournal: 归档 %s: %w", d, err)
		}
		if err := os.Remove(src); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ReadArchived 读取已归档分片（透明解压）。
func (j *Journal) ReadArchived(day string, limit int) ([]Record, error) {
	path := filepath.Join(j.dir, day+".jsonl.gz")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()

	out := []Record{}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		out = append(out, r)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, sc.Err()
}

// Stats 汇总某天的真实统计（供监控面板）。
type Stats struct {
	Day          string         `json:"day"`
	Total        int            `json:"total"`
	ByStatus     map[string]int `json:"by_status"`
	ByProvider   map[string]int `json:"by_provider"`
	ByModel      map[string]int `json:"by_model"`
	ByAgent      map[string]int `json:"by_agent"`
	FailoverRuns int            `json:"failover_runs"`   // tries>1 的请求数
	TotalTries   int            `json:"total_tries"`
	AffinityKept int            `json:"affinity_kept"`
	MaskedTotal  int            `json:"masked_total"`
	AvgMs        int            `json:"avg_ms"`
	P95Ms        int            `json:"p95_ms"`
	AvgTTFT      int            `json:"avg_ttft_ms"`
	TotalTokens  int            `json:"total_tokens"`
}

// Summarize 对一组记录做真实汇总。
func Summarize(day string, recs []Record) Stats {
	st := Stats{
		Day: day, Total: len(recs),
		ByStatus: map[string]int{}, ByProvider: map[string]int{},
		ByModel: map[string]int{}, ByAgent: map[string]int{},
	}
	if len(recs) == 0 {
		return st
	}
	lats := make([]int, 0, len(recs))
	msSum, ttftSum, ttftN := 0, 0, 0
	for _, r := range recs {
		st.ByStatus[fmt.Sprintf("%d", r.Status)]++
		if r.Provider != "" {
			st.ByProvider[r.Provider]++
		}
		if r.Model != "" {
			st.ByModel[r.Model]++
		}
		if r.Agent != "" {
			st.ByAgent[r.Agent]++
		}
		st.TotalTries += len(r.Tries)
		if len(r.Tries) > 1 {
			st.FailoverRuns++
		}
		msSum += r.Ms
		lats = append(lats, r.Ms)
		if r.TTFT > 0 {
			ttftSum += r.TTFT
			ttftN++
		}
		st.TotalTokens += r.Tokens
		if len(r.Masking) > 0 {
			var mr struct {
				N int `json:"n"`
			}
			if json.Unmarshal(r.Masking, &mr) == nil {
				st.MaskedTotal += mr.N
			}
		}
		if len(r.Affinity) > 0 {
			var ar struct {
				Kept bool `json:"kept"`
			}
			if json.Unmarshal(r.Affinity, &ar) == nil && ar.Kept {
				st.AffinityKept++
			}
		}
	}
	st.AvgMs = msSum / len(recs)
	if ttftN > 0 {
		st.AvgTTFT = ttftSum / ttftN
	}
	sort.Ints(lats)
	idx := int(float64(len(lats))*0.95) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(lats) {
		idx = len(lats) - 1
	}
	st.P95Ms = lats[idx]
	return st
}

func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	zw := gzip.NewWriter(tmp)
	if _, err := io.Copy(zw, in); err != nil {
		_ = zw.Close()
		_ = tmp.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	return os.Chmod(dst, 0o600)
}

// ErrNotFound 供上层区分「无记录」与「读失败」。
var ErrNotFound = errors.New("routingjournal: 无此分片")
