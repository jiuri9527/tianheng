package middleware

import (
	"testing"

	"llm-hub/src/internal/store"
)

// 天衡：**不设任何使用限制**。
//
// 需求原文：不准对任何人设置 token 使用限制、并发限制、设备限制，这些都是不必要的。
// 因此 Limiter 的所有判定路径必须恒放行；令牌结构体里的 RPM/TPM/MaxConcurrency/
// quota_usd/IP 白名单/expires_at 字段仅为接口形态保留，一律不参与判定。
//
// 下面这些测试锁定"无限制"语义。任何把限制加回来的改动都会让它们失败。

func tokWithLimits(rpm, tpm, conc int, quota float64) *store.Token {
	return &store.Token{
		ID: "tok_test", UserID: "usr_test", Name: "t",
		RPM: rpm, TPM: tpm, MaxConcurrency: conc, QuotaUSD: quota,
	}
}

// TestLimiterAllowsUnlimitedCalls 连续大量调用一律放行（RPM/TPM 不再是闸门）。
func TestLimiterAllowsUnlimitedCalls(t *testing.T) {
	l := NewLimiter(nil)
	tok := tokWithLimits(1, 10, 0, 0) // 故意设成极小值：也不该生效
	for i := 0; i < 5000; i++ {
		ok, reason := l.Allow(tok, 100000)
		if !ok {
			t.Fatalf("第 %d 次调用被拒绝（reason=%s）—— 天衡不得有任何使用限制", i+1, reason)
		}
		if reason != "" {
			t.Fatalf("放行时不应返回拒绝原因，实际 %q", reason)
		}
	}
}

// TestLimiterIgnoresConcurrencyCap 并发上限不再是闸门。
func TestLimiterIgnoresConcurrencyCap(t *testing.T) {
	l := NewLimiter(nil)
	tok := tokWithLimits(0, 0, 1, 0) // MaxConcurrency=1
	for i := 0; i < 200; i++ {
		if ok, reason := l.AllowLimits(tok, 1, 1, 100); !ok {
			t.Fatalf("并发上限不应生效，第 %d 次被拒（reason=%s）", i+1, reason)
		}
	}
}

// TestLimiterIgnoresQuota 配额耗尽不再是闸门（不再 402）。
func TestLimiterIgnoresQuota(t *testing.T) {
	l := NewLimiter(nil)
	tok := tokWithLimits(0, 0, 0, 0.000001)
	tok.UsedUSD = 999999 // 远超配额
	if ok, reason := l.Allow(tok, 1); !ok {
		t.Fatalf("配额不应生效（reason=%s）", reason)
	}
}

// TestLimiterAllowsNilToken 无令牌上下文时同样放行（Limiter 不再参与鉴权判定）。
func TestLimiterAllowsNilToken(t *testing.T) {
	l := NewLimiter(nil)
	if ok, reason := l.Allow(nil, 0); !ok {
		t.Fatalf("nil token 应放行（鉴权由上游中间件负责），实际 reason=%s", reason)
	}
}

// TestLimiterStillTracksInflight 放行不代表不观测：并发计数仍真实累加，
// 供监控面板展示真实在途数量（这是可观测性，不是限制）。
func TestLimiterStillTracksInflight(t *testing.T) {
	l := NewLimiter(nil)
	tok := tokWithLimits(0, 0, 0, 0)
	for i := 0; i < 7; i++ {
		if ok, _ := l.AllowLimits(tok, 0, 0, 0); !ok {
			t.Fatal("应放行")
		}
	}
	l.mu.Lock()
	got := l.inflight[tok.ID]
	l.mu.Unlock()
	if got != 7 {
		t.Fatalf("并发计数应真实累加为 7，实际 %d", got)
	}
	l.Release(tok.ID)
	l.mu.Lock()
	after := l.inflight[tok.ID]
	l.mu.Unlock()
	if after != 6 {
		t.Fatalf("释放后应为 6，实际 %d", after)
	}
}

// TestLimiterResetClearsState Reset 清空计数状态（供测试与运维复位）。
func TestLimiterResetClearsState(t *testing.T) {
	l := NewLimiter(nil)
	tok := tokWithLimits(0, 0, 0, 0)
	for i := 0; i < 5; i++ {
		l.AllowLimits(tok, 0, 0, 0)
	}
	l.Reset(tok.ID)
	l.mu.Lock()
	got := l.inflight[tok.ID]
	l.mu.Unlock()
	if got != 0 {
		t.Fatalf("Reset 后计数应为 0，实际 %d", got)
	}
}

// TestLimiterBucketStillAvailable 令牌桶构造仍可用（供其它维度复用），
// 但不再挂到任何拒绝路径上。
func TestLimiterBucketStillAvailable(t *testing.T) {
	l := NewLimiter(nil)
	b := l.bucket("k", 1000, 1000)
	if b == nil {
		t.Fatal("bucket 应可构造")
	}
	if ok, _ := b.Allow("k", 1); !ok {
		t.Fatal("高配额桶应放行")
	}
}
