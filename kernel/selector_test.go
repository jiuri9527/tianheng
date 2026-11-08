// 文件: kernel/selector_test.go
package kernel

import (
	"testing"
	"time"
)

func baseChannels() []Channel {
	return []Channel{
		{ID: "a", Weight: 3, Priority: 10, Healthy: true},
		{ID: "b", Weight: 1, Priority: 1, Healthy: true},
	}
}

func TestSelectorWeightedDistribution(t *testing.T) {
	s := NewSelector(StrategyWeightedRR, 3, time.Second)
	cs := baseChannels()
	counts := map[string]int{}
	for i := 0; i < 400; i++ {
		c, err := s.Pick(cs)
		if err != nil {
			t.Fatalf("pick %d: %v", i, err)
		}
		counts[c.ID]++
		s.Report(c, Result{OK: true, Status: 200})
	}
	if counts["a"] < 250 || counts["a"] > 350 {
		t.Fatalf("weight 3 channel got %d/400, want ~300", counts["a"])
	}
	if counts["b"] < 50 || counts["b"] > 150 {
		t.Fatalf("weight 1 channel got %d/400, want ~100", counts["b"])
	}
}

func TestSelectorLeastInFlight(t *testing.T) {
	s := NewSelector(StrategyLeastInFlight, 3, time.Second)
	cs := []Channel{
		{ID: "a", Weight: 1, Healthy: true, InFlight: 7},
		{ID: "b", Weight: 1, Healthy: true, InFlight: 1},
	}
	c, err := s.Pick(cs)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "b" {
		t.Fatalf("picked %q, want least-in-flight b", c.ID)
	}
}

func TestSelectorPriority(t *testing.T) {
	s := NewSelector(StrategyPriority, 3, time.Second)
	c, err := s.Pick(baseChannels())
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "a" {
		t.Fatalf("picked %q, want highest priority a", c.ID)
	}
}

func TestSelectorCircuitBreaker(t *testing.T) {
	s := NewSelector(StrategyPriority, 3, 200*time.Millisecond)
	cs := baseChannels()
	for i := 0; i < 3; i++ {
		c, err := s.Pick(cs)
		if err != nil {
			t.Fatalf("pick %d: %v", i, err)
		}
		s.Report(c, Result{OK: false, Status: 500})
	}
	c, err := s.Pick(cs)
	if err != nil {
		t.Fatalf("pick after circuit: %v", err)
	}
	if c.ID != "b" {
		t.Fatalf("cooldown channel a still selected: %s", c.ID)
	}
	time.Sleep(220 * time.Millisecond)
	c, err = s.Pick(cs)
	if err != nil {
		t.Fatalf("pick after cooldown: %v", err)
	}
	if c.ID != "a" {
		t.Fatalf("channel a did not recover after cooldown: %s", c.ID)
	}
}

func TestSelectorNoHealthyChannel(t *testing.T) {
	s := NewSelector(StrategyWeightedRR, 3, time.Second)
	if _, err := s.Pick([]Channel{{ID: "down", Healthy: false}}); err != ErrNoHealthyChannel {
		t.Fatalf("err=%v want ErrNoHealthyChannel", err)
	}
	now := time.Now()
	cool := []Channel{{
		ID: "cooling", Healthy: true, Weight: 1,
		CooldownUntil: now.Add(time.Minute),
	}}
	if _, err := s.Pick(cool); err != ErrNoHealthyChannel {
		t.Fatalf("cooldown err=%v want ErrNoHealthyChannel", err)
	}
}

func TestSelectorReportReleasesInFlight(t *testing.T) {
	s := NewSelector(StrategyLeastInFlight, 3, time.Second)
	cs := []Channel{{ID: "a", Healthy: true, Weight: 1}}
	c, _ := s.Pick(cs)
	s.Report(c, Result{OK: true, Status: 200})
	// 连续 Pick/Report 不应让在途计数无限增长导致 a 被饿死
	for i := 0; i < 50; i++ {
		got, err := s.Pick(cs)
		if err != nil {
			t.Fatalf("pick %d: %v", i, err)
		}
		s.Report(got, Result{OK: true, Status: 200})
	}
}
