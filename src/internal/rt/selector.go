package rt

import (
	"errors"
	"sync"
	"time"

	"llm-hub/kernel"
)

// Strategy 选择策略。
type Strategy string

const (
	// StrategyWeightedRR 平滑加权轮询（默认）。
	StrategyWeightedRR Strategy = "weighted_round_robin"
	// StrategyLeastInFlight 最少在途。
	StrategyLeastInFlight Strategy = "least_in_flight"
	// StrategyPriority 优先级优先。
	StrategyPriority Strategy = "priority"
)

type channelState struct {
	wrrCurrent    int
	inFlight      int64
	failures      int
	cooldownUntil time.Time
}

// LoadBalancer 实现 kernel.Selector：三种策略 + 在途计数 + 短时熔断。
type LoadBalancer struct {
	mu       sync.Mutex
	strategy Strategy
	cooldown time.Duration
	states   map[string]*channelState
	wrrTotal int
	clock    func() time.Time
}

// NewLoadBalancer 构造选择器。cooldown<=0 时默认 30s。
func NewLoadBalancer(strategy Strategy, cooldown time.Duration) *LoadBalancer {
	if strategy == "" {
		strategy = StrategyWeightedRR
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &LoadBalancer{
		strategy: strategy, cooldown: cooldown,
		states: map[string]*channelState{}, clock: time.Now,
	}
}

func (s *LoadBalancer) state(id string) *channelState {
	st, ok := s.states[id]
	if !ok {
		st = &channelState{}
		s.states[id] = st
	}
	return st
}

// SetStrategy 运行时切换策略（管理面设置项）。
func (s *LoadBalancer) SetStrategy(strategy Strategy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strategy = strategy
}

// Pick 从候选渠道中选一个；过滤 unhealthy 与处于冷却期的渠道。
// 命中后在途计数 +1（配对 Report 会 -1）。
func (s *LoadBalancer) Pick(candidates []kernel.Channel) (kernel.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	avail := make([]kernel.Channel, 0, len(candidates))
	for _, c := range candidates {
		if !c.Healthy {
			continue
		}
		st := s.state(c.ID)
		if now.Before(c.CooldownUntil) || now.Before(st.cooldownUntil) {
			continue
		}
		avail = append(avail, c)
	}
	if len(avail) == 0 {
		return kernel.Channel{}, errors.New("rt: no healthy channel available")
	}

	var chosen kernel.Channel
	switch s.strategy {
	case StrategyLeastInFlight:
		best := 0
		bestVal := s.state(avail[0].ID).inFlight
		for i := 1; i < len(avail); i++ {
			v := s.state(avail[i].ID).inFlight
			if v < bestVal {
				bestVal, best = v, i
			}
		}
		chosen = avail[best]
	case StrategyPriority:
		best := 0
		for i := 1; i < len(avail); i++ {
			if avail[i].Priority > avail[best].Priority {
				best = i
			} else if avail[i].Priority == avail[best].Priority &&
				s.state(avail[i].ID).inFlight < s.state(avail[best].ID).inFlight {
				best = i
			}
		}
		chosen = avail[best]
	default: // 平滑加权轮询
		total := 0
		for _, c := range avail {
			w := c.Weight
			if w <= 0 {
				w = 1
			}
			s.state(c.ID).wrrCurrent += w
			total += w
		}
		best := 0
		for i := 1; i < len(avail); i++ {
			if s.state(avail[i].ID).wrrCurrent > s.state(avail[best].ID).wrrCurrent {
				best = i
			}
		}
		chosen = avail[best]
		s.state(chosen.ID).wrrCurrent -= total
		s.wrrTotal = total
	}
	s.state(chosen.ID).inFlight++
	return chosen, nil
}

// Report 回写调用结果：在途 -1；失败/429/5xx 触发短时熔断与失败计数。
func (s *LoadBalancer) Report(ch kernel.Channel, r kernel.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state(ch.ID)
	if st.inFlight > 0 {
		st.inFlight--
	}
	fail := !r.OK || r.Status == 429 || r.Status >= 500 || r.Err != nil
	if fail {
		st.failures++
		st.cooldownUntil = s.clock().Add(s.cooldown)
	} else {
		st.failures = 0
		st.cooldownUntil = time.Time{}
	}
}

// CooldownUntil 返回某渠道当前内部冷却截止时间（供 router 回写 store）。
func (s *LoadBalancer) CooldownUntil(id string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state(id).cooldownUntil
}

// InFlight 返回某渠道当前在途计数。
func (s *LoadBalancer) InFlight(id string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state(id).inFlight
}

// SetCooldown 手动设置冷却（health/check 使用）。
func (s *LoadBalancer) SetCooldown(id string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state(id).cooldownUntil = until
}

var _ kernel.Selector = (*LoadBalancer)(nil)
