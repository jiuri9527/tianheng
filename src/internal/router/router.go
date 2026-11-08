// Package router 实现 F8：别名 -> 候选渠道集、加权轮询/最少在途/优先级、
// 熔断与 MAX_FAILOVER 切换。选择器通过 kernel.Selector 接口注入（团队5/rt 实现）。
package router

import (
	"errors"
	"strings"
	"sync"
	"time"

	"llm-hub/kernel"
	"llm-hub/src/internal/store"
)

var knownProviders = map[string]bool{
	"openai": true, "anthropic": true, "xai": true, "gemini": true, "openai_compatible": true,
}

// Router 路由器。
type Router struct {
	St          *store.Store
	Selector    kernel.Selector
	MaxFailover int
	Cooldown    time.Duration
	mu          sync.Mutex
}

// New 构造路由器。
func New(st *store.Store, selector kernel.Selector, maxFailover int, cooldown time.Duration) *Router {
	if maxFailover <= 0 {
		maxFailover = 3
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &Router{St: st, Selector: selector, MaxFailover: maxFailover, Cooldown: cooldown}
}

// Candidate 是一个可选渠道及其命中的模型。
type Candidate struct {
	Channel *store.Channel
	Model   *store.Model
}

// Candidates 解析别名对应的候选渠道（active、已发现、可见性允许、非冷却）。
func (r *Router) Candidates(alias, userID string) []Candidate {
	providerFilter, aliases := parseAlias(alias)
	models := r.St.ListModels()
	byChannel := map[string]Candidate{}
	for _, m := range models {
		if !m.Enabled {
			continue
		}
		if !matchAlias(m.Alias, aliases) {
			continue
		}
		ch, ok := r.St.GetChannel(m.ChannelID)
		if !ok || ch.Status != "active" {
			continue
		}
		if providerFilter != "" && ch.ProviderType != providerFilter {
			continue
		}
		if ch.Visibility == "private" && ch.OwnerID != userID {
			continue
		}
		if ch.ProbeStatus == "unavailable" {
			continue // 拉取失败、无真实模型的渠道不参与路由
		}
		if time.Now().Unix() < ch.CooldownUntil {
			continue
		}
		byChannel[ch.ID] = Candidate{Channel: ch, Model: m}
	}
	out := make([]Candidate, 0, len(byChannel))
	for _, c := range byChannel {
		out = append(out, c)
	}
	return out
}

func parseAlias(alias string) (provider string, aliases []string) {
	aliases = []string{alias}
	if i := strings.IndexByte(alias, '/'); i > 0 {
		prefix := alias[:i]
		if knownProviders[prefix] {
			provider = prefix
			aliases = []string{alias, alias[i+1:]}
		}
	}
	return
}

func matchAlias(candidate string, aliases []string) bool {
	for _, a := range aliases {
		if a != "" && (candidate == a || strings.HasSuffix(candidate, "/"+a)) {
			return true
		}
	}
	return false
}

// Pick 用注入的选择器挑一个渠道。
func (r *Router) Pick(alias, userID string) (*store.Channel, error) {
	cands := r.Candidates(alias, userID)
	if len(cands) == 0 {
		return nil, errors.New("no available upstream channel for model")
	}
	kcs := make([]kernel.Channel, 0, len(cands))
	for _, c := range cands {
		kc := kernel.Channel{
			ID: c.Channel.ID, Weight: c.Channel.Weight, Priority: c.Channel.Priority,
			Healthy: true,
		}
		if c.Channel.CooldownUntil > 0 {
			kc.CooldownUntil = time.Unix(c.Channel.CooldownUntil, 0)
		}
		if inFlight, ok := r.Selector.(interface{ InFlight(string) int64 }); ok {
			kc.InFlight = inFlight.InFlight(c.Channel.ID)
		}
		kcs = append(kcs, kc)
	}
	chosen, err := r.Selector.Pick(kcs)
	if err != nil {
		return nil, err
	}
	ch, ok := r.St.GetChannel(chosen.ID)
	if !ok {
		return nil, errors.New("selected channel missing")
	}
	return ch, nil
}

// Report 回写调用结果到选择器（熔断）与渠道状态（冷却/失败计数）。
func (r *Router) Report(ch *store.Channel, result kernel.Result) {
	r.Selector.Report(kernel.Channel{ID: ch.ID}, result)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.St.UpdateChannelLocked(ch.ID, func(c *store.Channel) {
		if !result.OK || result.Status == 429 || result.Status >= 500 || result.Err != nil {
			c.FailCount++
			c.CooldownUntil = time.Now().Add(r.Cooldown).Unix()
			if result.Err != nil {
				c.LastError = result.Err.Error()
			} else {
				c.LastError = "upstream status " + itoa(result.Status)
			}
		} else {
			c.FailCount = 0
			c.CooldownUntil = 0
			c.LastError = ""
		}
		c.HealthCheckedAt = time.Now().Unix()
	})
}

// SetCooldown 手动设置冷却（管理面/健康检查）。
func (r *Router) SetCooldown(id string, until time.Time) {
	r.St.UpdateChannelLocked(id, func(ch *store.Channel) {
		ch.CooldownUntil = until.Unix()
	})
	if s, ok := r.Selector.(interface{ SetCooldown(string, time.Time) }); ok {
		s.SetCooldown(id, until)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
