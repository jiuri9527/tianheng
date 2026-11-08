// 文件: kernel/queue.go
package kernel

import (
	"errors"
	"runtime"
	"sync/atomic"
)

var (
	// ErrQueueFull 表示有界队列已达容量上限，触发背压。
	ErrQueueFull = errors.New("kernel: bounded queue is full")
	// ErrQueueEmpty 表示队列当前无可用任务。
	ErrQueueEmpty = errors.New("kernel: queue is empty")
	// ErrNilTask 表示提交了空任务。
	ErrNilTask = errors.New("kernel: nil task")
)

func nextPow2(n int) uint64 {
	if n < 2 {
		return 2
	}
	v := uint64(1)
	for v < uint64(n) {
		v <<= 1
	}
	return v
}

// cell 是 Vyukov MPMC 环形队列的单格：seq 作为状态位，val 存任务指针。
// padding 降低伪共享（false sharing）。
type cell struct {
	seq atomic.Uint64
	val atomic.Pointer[Task]
	_   [48]byte
}

// MPMCQueue 自研有界无锁多生产者/多消费者环形队列（纯 sync/atomic，零第三方依赖）。
// 算法：每个 cell 的 seq 等于 pos 时表示可写，等于 pos+1 时表示可读；
// 生产/消费各自先 CAS 抢占位置，再写值并发布 seq。
type MPMCQueue struct {
	cells []cell
	mask  uint64
	_     [56]byte
	enq   atomic.Uint64
	_     [56]byte
	deq   atomic.Uint64
}

// NewMPMCQueue 创建容量向上取整到 2 的幂的队列。
func NewMPMCQueue(capacity int) *MPMCQueue {
	n := nextPow2(capacity)
	q := &MPMCQueue{cells: make([]cell, n), mask: n - 1}
	for i := range q.cells {
		q.cells[i].seq.Store(uint64(i))
	}
	return q
}

// Cap 返回实际槽位数。
func (q *MPMCQueue) Cap() int { return len(q.cells) }

// Len 返回近似在队任务数（并发下为瞬时快照）。
func (q *MPMCQueue) Len() int64 {
	e := q.enq.Load()
	d := q.deq.Load()
	if e < d {
		return 0
	}
	return int64(e - d)
}

// TryPush 非阻塞入队；满时返回 ErrQueueFull 供上层做背压。
func (q *MPMCQueue) TryPush(t *Task) error {
	if t == nil {
		return ErrNilTask
	}
	for {
		pos := q.enq.Load()
		c := &q.cells[pos&q.mask]
		seq := c.seq.Load()
		switch {
		case seq == pos:
			if q.enq.CompareAndSwap(pos, pos+1) {
				c.val.Store(t)
				c.seq.Store(pos + 1)
				return nil
			}
		case seq < pos:
			return ErrQueueFull
		default:
			runtime.Gosched()
		}
	}
}

// TryPop 非阻塞出队；空时返回 ErrQueueEmpty。
func (q *MPMCQueue) TryPop() (*Task, error) {
	for {
		pos := q.deq.Load()
		c := &q.cells[pos&q.mask]
		seq := c.seq.Load()
		switch {
		case seq == pos+1:
			if q.deq.CompareAndSwap(pos, pos+1) {
				t := c.val.Swap(nil)
				c.seq.Store(pos + q.mask + 1)
				if t == nil {
					return nil, ErrQueueEmpty
				}
				return t, nil
			}
		case seq < pos+1:
			return nil, ErrQueueEmpty
		default:
			runtime.Gosched()
		}
	}
}

// ShardedQueue 分片队列：写入按轮转分散到分片，弹出时按 worker id 顺序探测，
// 天然支持 work-stealing 的跨分片窃取。
type ShardedQueue struct {
	shards []*MPMCQueue
	n      uint64
	next   atomic.Uint64
}

// NewShardedQueue 创建 shards 个分片，每片容量 perShard。
func NewShardedQueue(shards, perShard int) *ShardedQueue {
	if shards < 1 {
		shards = 1
	}
	if perShard < 2 {
		perShard = 2
	}
	s := &ShardedQueue{shards: make([]*MPMCQueue, shards), n: uint64(shards)}
	for i := range s.shards {
		s.shards[i] = NewMPMCQueue(perShard)
	}
	return s
}

// Count 返回分片数。
func (s *ShardedQueue) Count() int { return len(s.shards) }

// Shard 返回第 i 个分片（i 会自动取模）。
func (s *ShardedQueue) Shard(i int) *MPMCQueue { return s.shards[uint64(i)%s.n] }

// Push 先写目标分片，满则顺序探测其它分片，全满才返回 ErrQueueFull。
func (s *ShardedQueue) Push(t *Task) error {
	idx := s.next.Add(1) - 1
	for k := uint64(0); k < s.n; k++ {
		if err := s.shards[(idx+k)%s.n].TryPush(t); err == nil {
			return nil
		}
	}
	return ErrQueueFull
}

// Pop 从 start 指定的分片开始轮询所有分片（窃取语义）。
func (s *ShardedQueue) Pop(start int) (*Task, error) {
	base := uint64(start)
	for k := uint64(0); k < s.n; k++ {
		if t, err := s.shards[(base+k)%s.n].TryPop(); err == nil {
			return t, nil
		}
	}
	return nil, ErrQueueEmpty
}

// Len 返回所有分片在队任务数之和（瞬时快照）。
func (s *ShardedQueue) Len() int64 {
	var n int64
	for _, q := range s.shards {
		n += q.Len()
	}
	return n
}
