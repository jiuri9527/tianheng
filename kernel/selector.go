// 文件: kernel/selector.go
package kernel

import (
	"errors"
	"sync"
	"time"
)

// ErrNoHealthyChannel 表示候选渠道全部不健康或处于熔断冷却期。
var ErrNoHealthyChannel = errors.New("kernel: no healthy channel available")

// Strategy 是渠道选择策略。
type Strategy string

const (
	StrategyWeightedRR     Strategy = "weighted_rr"
	StrategyLeastInFlight  Strategy = "least_in_flight"
	StrategyPriority       Strategy = "priority"
)

type selector struct {
	mu               sync.Mutex
	strategy         Strategy
	rrCurrent        map[string]int
	inFlight         map[string]int64
	failStreak       map[string]int
	cooldown         map[string]time.Time
	cooldownDuration time.Duration
	failThreshold    int
}

// NewSelector 创建自研渠道选择器；默认加权轮询，连续 3 次失败冷却 30s。
func NewSelector(strategy Strategy, failThreshold int, cooldown time.Duration) Selector {
	if strategy == "" {
		strategy = StrategyWeightedRR
	}
	if failThreshold < 1 {
		failThreshold = 3
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &selector{
		strategy:         strategy,
		rrCurrent:        make(map[string]int),
		inFlight:         make(map[string]int64),
		failStreak:       make(map[string]int),
		cooldown:         make(map[string]time.Time),
		cooldownDuration: cooldown,
		failThreshold:    failThreshold,
	}
}

// Pick 过滤不健康/冷却中的渠道，按策略选一个，并登记在途计数。
func (s *selector) Pick(candidates []Channel) (Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	avail := make([]Channel, 0, len(candidates))
	for _, c := range candidates {
		if !c.Healthy {
			continue
		}
		if !c.CooldownUntil.IsZero() && now.Before(c.CooldownUntil) {
			continue
		}
		if cd, ok := s.cooldown[c.ID]; ok && now.Before(cd) {
			continue
		}
		avail = append(avail, c)
	}
	if len(avail) == 0 {
		return Channel{}, ErrNoHealthyChannel
	}

	var chosen Channel
	switch s.strategy {
	case StrategyLeastInFlight:
		best := int64(1 << 62)
		for _, c := range avail {
			v := s.inFlight[c.ID] + c.InFlight
			if v < best {
				best = v
				chosen = c
			}
		}
	case StrategyPriority:
		bestPriority := -1 << 60
		for _, c := range avail {
			if c.Priority > bestPriority || (c.Priority == bestPriority && c.ID < chosen.ID) {
				bestPriority = c.Priority
				chosen = c
			}
		}
	default: // 平滑加权轮询 (SWRR)
		total := 0
		for _, c := range avail {
			w := c.Weight
			if w <= 0 {
				w = 1
			}
			total += w
			s.rrCurrent[c.ID] += w
		}
		best := -1 << 60
		for _, c := range avail {
			if v := s.rrCurrent[c.ID]; v > best {
				best = v
				chosen = c
			}
		}
		w := chosen.Weight
		if w <= 0 {
			w = 1
		}
		s.rrCurrent[chosen.ID] -= total
	}

	s.inFlight[chosen.ID]++
	return chosen, nil
}

// Report 回写调用结果：释放在途、累计失败、达到阈值触发熔断；成功清零。
func (s *selector) Report(c Channel, r Result) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.inFlight[c.ID] > 0 {
		s.inFlight[c.ID]--
	}
	bad := !r.OK || r.Err != nil || r.Status == 429 || r.Status >= 500
	if bad {
		s.failStreak[c.ID]++
		if s.failStreak[c.ID] >= s.failThreshold {
			s.cooldown[c.ID] = time.Now().Add(s.cooldownDuration)
			s.failStreak[c.ID] = 0
		}
		return
	}
	s.failStreak[c.ID] = 0
	delete(s.cooldown, c.ID)
}
