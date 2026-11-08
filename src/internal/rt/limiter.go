package rt

import (
	"math"
	"sync"
	"time"

	"llm-hub/kernel"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// TokenBucketLimiter 是「Redis 风格」内存令牌桶，按键独立计数，实现 kernel.Limiter。
type TokenBucketLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     float64 // 每秒补充 token 数
	capacity float64 // 桶容量
	clock    func() time.Time
}

// NewTokenBucketLimiter 构造限流器。rate=每秒补充速率，capacity=桶容量。
func NewTokenBucketLimiter(rate, capacity float64) *TokenBucketLimiter {
	// 非有限值会让 b.tokens 变成 NaN/Inf：NaN 令比较永久失败（全量拒绝），
	// Inf 令限流被绕过。此处强制回退到有限默认值。
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		rate = 1
	}
	if math.IsNaN(capacity) || math.IsInf(capacity, 0) || capacity <= 0 {
		capacity = rate
	}
	return &TokenBucketLimiter{
		buckets: map[string]*bucket{}, rate: rate, capacity: capacity, clock: time.Now,
	}
}

// maxBuckets 限制桶表基数：key 若来自攻击者可控标识，无界 map 会导致内存耗尽。
const maxBuckets = 100000

// Allow 判断 key 是否还能消费 cost 个 token。
func (l *TokenBucketLimiter) Allow(key string, cost int64) (bool, error) {
	if cost < 0 {
		return false, errInvalidCost
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxBuckets {
			// 先清理已满（空闲）的桶；仍满则拒绝新 key，fail closed 不无限增长。
			for k, v := range l.buckets {
				if v.tokens >= l.capacity {
					delete(l.buckets, k)
				}
			}
			if len(l.buckets) >= maxBuckets {
				return false, nil
			}
		}
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.capacity {
			b.tokens = l.capacity
		}
		b.last = now
	}
	if float64(cost) > l.capacity && cost != 0 {
		// 单次成本大于桶容量：永远无法满足，直接拒绝
		return false, nil
	}
	if b.tokens >= float64(cost) {
		b.tokens -= float64(cost)
		return true, nil
	}
	return false, nil
}

// Reset 清空某 key（吊销令牌/测试用）。
func (l *TokenBucketLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

var errInvalidCost = &rtError{"rt: negative cost"}

var _ kernel.Limiter = (*TokenBucketLimiter)(nil)
