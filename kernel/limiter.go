// 文件: kernel/limiter.go
package kernel

import (
	"errors"
	"hash/fnv"
	"sync"
	"time"
)

// ErrInvalidCost 表示限流 cost 非法。
var ErrInvalidCost = errors.New("kernel: limiter cost must be positive")

type lbucket struct {
	tokens int64
	last   int64 // unix nano
}

type lshard struct {
	mu      sync.Mutex
	buckets map[string]*lbucket
}

// tokenBucketLimiter 是分片令牌桶：按 key 的 FNV 哈希落到分片，
// 分片内用 mutex 保证 refill 与扣减的原子性，避免跨 key 争用。
type tokenBucketLimiter struct {
	shards []*lshard
	rate   int64 // 每秒补充 token 数
	burst  int64 // 桶容量
}

// NewLimiter 创建分片令牌桶。ratePerSec 为每秒补充速率，burst 为桶容量，
// shards 为分片数（建议为 CPU 核数的 2 倍）。
func NewLimiter(ratePerSec, burst int64, shards int) Limiter {
	if ratePerSec < 1 {
		ratePerSec = 1
	}
	if burst < 1 {
		burst = 1
	}
	if shards < 1 {
		shards = 16
	}
	l := &tokenBucketLimiter{shards: make([]*lshard, shards), rate: ratePerSec, burst: burst}
	for i := range l.shards {
		l.shards[i] = &lshard{buckets: make(map[string]*lbucket)}
	}
	return l
}

// Allow 尝试从 key 对应桶中扣除 cost 个 token；不足时返回 false（不阻塞）。
func (l *tokenBucketLimiter) Allow(key string, cost int64) (bool, error) {
	if cost <= 0 {
		return false, ErrInvalidCost
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	s := l.shards[int(h.Sum32())%len(l.shards)]

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixNano()
	b, ok := s.buckets[key]
	if !ok {
		// 桶表基数上限：key 若可控，无界 map 会导致内存耗尽。
		if len(s.buckets) >= 100000 {
			return false, nil
		}
		b = &lbucket{tokens: l.burst, last: now}
		s.buckets[key] = b
	}
	elapsed := now - b.last
	if elapsed > 0 {
		// 限制单次补充窗口，避免 elapsed*rate 溢出 int64 导致令牌数回绕
		// （限流被绕过或该 key 永久拒绝）。
		if elapsed > int64(time.Hour) {
			elapsed = int64(time.Hour)
		}
		rate := l.rate
		if rate > 1e9 {
			rate = 1e9 // 限定整数乘法上界
		}
		// 拆分整秒与亚秒分别计算：保留 <1s 的补充精度（F9），
		// elapsed 已钳制到 1h、rate<=1e9，两段乘积都不溢出 int64。
		whole := elapsed / int64(time.Second)
		frac := elapsed % int64(time.Second)
		b.tokens += whole*rate + (frac*rate)/int64(time.Second)
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}
	if b.tokens < cost {
		return false, nil
	}
	b.tokens -= cost
	return true, nil
}
