// 文件: kernel/limiter_test.go
package kernel

import (
	"testing"
	"time"
)

func TestLimiterBurstAndRefill(t *testing.T) {
	l := NewLimiter(10, 5, 4) // 10 token/s，桶容量 5
	for i := 0; i < 5; i++ {
		ok, err := l.Allow("tenant-a", 1)
		if err != nil || !ok {
			t.Fatalf("burst %d: ok=%v err=%v", i, ok, err)
		}
	}
	if ok, _ := l.Allow("tenant-a", 1); ok {
		t.Fatal("bucket should be exhausted after burst")
	}
	time.Sleep(130 * time.Millisecond)
	if ok, _ := l.Allow("tenant-a", 1); !ok {
		t.Fatal("token should have been refilled")
	}
}

func TestLimiterPerKeyIsolation(t *testing.T) {
	l := NewLimiter(1, 2, 2)
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("k1", 1); !ok {
			t.Fatalf("k1 burst %d denied", i)
		}
	}
	if ok, _ := l.Allow("k1", 1); ok {
		t.Fatal("k1 should be exhausted")
	}
	if ok, _ := l.Allow("k2", 1); !ok {
		t.Fatal("k2 must have its own bucket")
	}
}

func TestLimiterInvalidCost(t *testing.T) {
	l := NewLimiter(10, 10, 2)
	if _, err := l.Allow("k", 0); err != ErrInvalidCost {
		t.Fatalf("err=%v want ErrInvalidCost", err)
	}
}

func TestMetricsRender(t *testing.T) {
	m := NewMetrics()
	m.Inc("llmhub_requests_total", `model="x-ai/grok-4.7",status="200"`)
	m.Add("llmhub_tokens_total", `type="prompt"`, 42)
	m.Set("llmhub_channel_health", 1)
	if got := m.Get("llmhub_requests_total", `model="x-ai/grok-4.7",status="200"`); got != 1 {
		t.Fatalf("Get=%d want 1", got)
	}
	out := m.Render()
	for _, want := range []string{
		"# TYPE llmhub_requests_total counter",
		`llmhub_requests_total{model="x-ai/grok-4.7",status="200"} 1`,
		"llmhub_tokens_total{type=\"prompt\"} 42",
		"# TYPE llmhub_channel_health gauge",
	} {
		if !contains(out, want) {
			t.Fatalf("Render missing %q\n%s", want, out)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
