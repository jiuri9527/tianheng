// Package affinity 原生实现「会话亲和性路由」原理。
//
// ── 原理来源 ────────────────────────────────────────────────────────────────
// 该原理取自对 AI 供应商聚合层 Magpie（com.yetone.magpie）真实运行数据的逆向：
// 它的 affinity.json 以 `group/<分组id>|<会话id>` 为键，值为
//   {rest, who, model, turn, ...}
// 并在每条路由决策里记录
//   affinity: {mode, turn, within, last, lastTurn, at, cacheWrite, kept, why:"turn"}
// 其中 cacheWrite 高达 515871 tokens、turn 达 82 —— 说明其核心目的不是负载均衡，
// 而是**把多轮会话钉在同一个上游账号上，从而复用上游的 prompt 缓存**。
//
// 为什么这对天衡至关重要：
//   LLM 上游（Anthropic / OpenAI / Gemini 等）对「前缀完全相同的多轮对话」有
//   prompt caching，缓存命中的输入 token 计费通常是未命中的 1/10。若每次请求都用
//   加权轮询换一个上游，缓存必然全部 miss —— 十几轮的长对话会把成本放大一个量级。
//   所以「粘住」不是优化，是 LLM 网关的必需能力。
//
// ── 与「用户限制」的区别（重要）────────────────────────────────────────────
// 天衡的设计原则是「所有用户都是超级用户、不设任何使用限制」。亲和性是
// **渠道维度的路由决策**，不是用户维度的配额：它只决定「同一个会话打到哪个上游」，
// 完全不限制任何用户能发多少请求、用多少 token、开多少并发。
//
// ── 语义 ────────────────────────────────────────────────────────────────────
//   kept=true  → 继续粘住上一轮的上游（缓存可复用）
//   kept=false → 放弃粘性，回到正常选择器（上游失效 / 冷却 / TTL 过期 / 分组变更）
//   why 字段如实记录放弃原因，绝不隐藏。
package affinity

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"llm-hub/src/internal/fsutil"
)

// DefaultTTL 是亲和条目的存活时间。超过后即使上游还在也重新选择，
// 避免流量长期偏向同一个上游（也避免上游把这个渠道识别为异常模式）。
const DefaultTTL = 30 * time.Minute

// DefaultTurnLimit 是单条亲和的最大轮数。达到后主动轮换一次上游，
// 让会话在长对话中仍能利用新上游的缓存窗口，同时防止单渠道被长期占死。
const DefaultTurnLimit = 96

// Entry 是一条真实的亲和绑定。
type Entry struct {
	ConversationID string `json:"conversation_id"`
	Group          string `json:"group"`           // 逻辑分组（对外模型名），如 auto/dfmodel
	ChannelID      string `json:"channel_id"`      // 被钉住的渠道
	ChannelName    string `json:"channel_name"`    // 渠道展示名（供面板显示）
	UpstreamModel  string `json:"upstream_model"`  // 该渠道上的真实上游模型 id
	Turn           int    `json:"turn"`            // 已连续粘住的轮数
	FirstAt        int64  `json:"first_at"`
	LastAt         int64  `json:"last_at"`
	CacheWriteTok  int64  `json:"cache_write_tokens"` // 累计观测到的上游缓存写入 token
	CacheReadTok   int64  `json:"cache_read_tokens"`  // 累计观测到的缓存命中 token
}

// Decision 是一条真实的亲和判定结果，字段与原理来源一一对应。
type Decision struct {
	Mode      string `json:"mode"`       // "" 默认 | "pin" 强制钉住 | "off" 关闭
	Turn      int    `json:"turn"`       // 本次是第几轮
	Within    bool   `json:"within"`      // 是否仍在本轮期内
	Last      string `json:"last"`       // 上一轮的渠道 id
	LastTurn  int    `json:"last_turn"`  // 上一轮粘住的轮数
	At        string `json:"at"`         // 本次判定时间（RFC3339）
	CacheWrite int64 `json:"cache_write"` // 观测到的缓存写入
	CacheRead  int64 `json:"cache_read"`  // 观测到的缓存命中
	Kept      bool   `json:"kept"`       // 是否继续粘住
	Why       string `json:"why"`        // 判定依据：first|turn|cache|ttl|gone|cooling|disabled|limit
}

// Reason 常量：如实记录为什么保留/放弃粘性。
const (
	WhyFirst    = "first"     // 该会话首次出现，无历史
	WhyTurn     = "turn"      // 仍在上游续用窗口内，保持粘住以复用缓存
	WhyCache    = "cache"     // 观测到显著缓存命中，保持粘住的价值很高
	WhyTTL      = "ttl"       // 超过 TTL，主动轮换
	WhyGone     = "gone"      // 上游渠道已不存在 / 非 active
	WhyCooling  = "cooling"   // 上游处于熔断冷却
	WhyDisabled = "disabled"  // 会话显式关闭粘性（mode=off）
	WhyLimit    = "limit"     // 达到轮数上限，主动轮换
)

// Probe 是路由器为判定亲和而提供的「候选是否仍可用」查询。
// 返回 (展示名, 上游模型, 是否可用)。
type Probe func(channelID string) (name string, upstreamModel string, usable bool)

// Store 保存全部亲和绑定。内存为主、原子落盘为辅（进程重启后粘性不丢）。
type Store struct {
	mu      sync.RWMutex
	entries map[string]Entry // key = conversationID + "\x00" + group
	path    string
	ttl     time.Duration
	maxTurn int
	dirty   int
}

// New 构造亲和存储。dir 为数据目录，落盘到 <dir>/affinity.json。
func New(dir string) *Store {
	return &Store{
		entries: map[string]Entry{},
		path:    filepath.Join(dir, "affinity.json"),
		ttl:     DefaultTTL,
		maxTurn: DefaultTurnLimit,
	}
}

// SetTTL 覆盖默认 TTL（<=0 表示用默认值）。
func (s *Store) SetTTL(d time.Duration) {
	if d > 0 {
		s.ttl = d
	}
}

// SetTurnLimit 覆盖默认轮数上限。
func (s *Store) SetTurnLimit(n int) {
	if n > 0 {
		s.maxTurn = n
	}
}

func key(conv, group string) string { return conv + "\x00" + group }

// Decide 判定某个会话在当前分组下是否继续粘住上一次的上游。
//
// probe 用于确认被钉住的渠道此刻是否真的可用（存在、active、未冷却）；
// 传 nil 表示跳过可用性检查（仅用于离线测试）。
func (s *Store) Decide(conv, group, mode string, probe Probe, now time.Time) Decision {
	d := Decision{Mode: mode, At: now.Format(time.RFC3339)}
	if mode == "off" {
		d.Why = WhyDisabled
		return d
	}

	s.mu.RLock()
	e, ok := s.entries[key(conv, group)]
	s.mu.RUnlock()

	if !ok {
		d.Why = WhyFirst
		return d
	}

	d.Last = e.ChannelID
	d.LastTurn = e.Turn
	d.Turn = e.Turn + 1
	d.CacheWrite = e.CacheWriteTok
	d.CacheRead = e.CacheReadTok
	d.Within = true

	if probe != nil {
		if _, _, usable := probe(e.ChannelID); !usable {
			d.Within = false
			d.Why = WhyGone
			return d
		}
	}
	if s.ttl > 0 && now.Sub(time.Unix(e.LastAt, 0)) > s.ttl {
		d.Within = false
		d.Why = WhyTTL
		return d
	}
	if s.maxTurn > 0 && e.Turn >= s.maxTurn {
		d.Within = false
		d.Why = WhyLimit
		return d
	}

	d.Kept = true
	if e.CacheReadTok > 0 {
		d.Why = WhyCache
	} else {
		d.Why = WhyTurn
	}
	return d
}

// Bind 记录/推进一条亲和绑定。channelName 与 upstreamModel 为真实值，
// 用于面板展示与「上游变了就得重绑」的判定。
func (s *Store) Bind(conv, group, channelID, channelName, upstreamModel string, now time.Time) Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(conv, group)
	e, ok := s.entries[k]
	if !ok {
		e = Entry{ConversationID: conv, Group: group, FirstAt: now.Unix()}
	} else if e.ChannelID != channelID || e.UpstreamModel != upstreamModel {
		// 上游变了：轮数重新计数，避免把两个不同上游的轮数累加。
		e.Turn = 0
		e.CacheWriteTok = 0
		e.CacheReadTok = 0
		e.FirstAt = now.Unix()
	}
	e.ChannelID = channelID
	e.ChannelName = channelName
	e.UpstreamModel = upstreamModel
	e.Turn++
	e.LastAt = now.Unix()
	s.entries[k] = e
	s.dirty++
	return e
}

// ObserveCache 记录一次真实观测到的上游缓存用量（来自响应 usage 字段）。
// 上游没报缓存时传 0，不做任何推测。
func (s *Store) ObserveCache(conv, group string, cacheWrite, cacheRead int64) {
	if cacheWrite == 0 && cacheRead == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(conv, group)
	e, ok := s.entries[k]
	if !ok {
		return
	}
	e.CacheWriteTok += cacheWrite
	e.CacheReadTok += cacheRead
	s.entries[k] = e
	s.dirty++
}

// Get 读取一条绑定。
func (s *Store) Get(conv, group string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[key(conv, group)]
	return e, ok
}

// Drop 删除一条绑定（渠道失效时调用）。
func (s *Store) Drop(conv, group string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key(conv, group))
	s.dirty++
}

// Snapshot 返回全部绑定的只读副本（按最后使用时间倒序）。
func (s *Store) Snapshot(limit int) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	// 简单插入排序即可（规模为活跃会话数，通常几百）；倒序 = 最近使用在前。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].LastAt > out[j-1].LastAt; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Stats 返回亲和总体统计（供监控面板显示真实数字）。
type Stats struct {
	Entries     int   `json:"entries"`
	Groups      int   `json:"groups"`
	Channels    int   `json:"channels"`
	MaxTurn     int   `json:"max_turn"`
	TotalTurns  int64 `json:"total_turns"`
	CacheWrite  int64 `json:"cache_write_tokens"`
	CacheRead   int64 `json:"cache_read_tokens"`
	CacheHitPct float64 `json:"cache_hit_percent"`
}

// Stats 汇总当前亲和状态。
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Stats{Entries: len(s.entries)}
	groups := map[string]bool{}
	chans := map[string]bool{}
	for _, e := range s.entries {
		groups[e.Group] = true
		chans[e.ChannelID] = true
		if e.Turn > st.MaxTurn {
			st.MaxTurn = e.Turn
		}
		st.TotalTurns += int64(e.Turn)
		st.CacheWrite += e.CacheWriteTok
		st.CacheRead += e.CacheReadTok
	}
	st.Groups = len(groups)
	st.Channels = len(chans)
	if tot := st.CacheWrite + st.CacheRead; tot > 0 {
		st.CacheHitPct = float64(st.CacheRead) / float64(tot) * 100
	}
	return st
}

// Prune 清理过期条目，返回清理数量。
func (s *Store) Prune(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, e := range s.entries {
		if s.ttl > 0 && now.Sub(time.Unix(e.LastAt, 0)) > 2*s.ttl {
			delete(s.entries, k)
			n++
		}
	}
	if n > 0 {
		s.dirty++
	}
	return n
}

// ---------- 持久化（原子写 + 0600，复用 fsutil）----------

type diskFormat struct {
	Format  string  `json:"format"`
	Version int     `json:"version"`
	SavedAt int64   `json:"saved_at"`
	Entries []Entry `json:"entries"`
}

// Load 从 <dir>/affinity.json 载入亲和状态。文件不存在属正常首次运行。
func (s *Store) Load() error {
	b, ok, err := fsutil.ReadFileIfExists(s.path)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	var df diskFormat
	if err := json.Unmarshal(b, &df); err != nil {
		return errors.New("affinity: 状态文件解析失败: " + err.Error())
	}
	if !strings.EqualFold(df.Format, "tianheng-affinity") {
		return errors.New("affinity: 状态文件格式标识不符，拒绝载入")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range df.Entries {
		if e.ConversationID == "" || e.Group == "" {
			continue
		}
		s.entries[key(e.ConversationID, e.Group)] = e
	}
	return nil
}

// Save 原子落盘亲和状态。返回内容 SHA-256。
func (s *Store) Save(now time.Time) (string, error) {
	s.mu.RLock()
	entries := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		entries = append(entries, e)
	}
	s.mu.RUnlock()

	df := diskFormat{Format: "tianheng-affinity", Version: 1, SavedAt: now.Unix(), Entries: entries}
	b, err := json.MarshalIndent(df, "", "  ")
	if err != nil {
		return "", err
	}
	return fsutil.AtomicWriteFile(s.path, append(b, '\n'))
}

// Path 返回状态文件路径（供运维核对权限）。
func (s *Store) Path() string { return s.path }

// ensure 用于测试断言目录权限。
func (s *Store) DirMode() (os.FileMode, error) {
	st, err := os.Stat(filepath.Dir(s.path))
	if err != nil {
		return 0, err
	}
	return st.Mode().Perm(), nil
}
