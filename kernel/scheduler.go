// 文件: kernel/scheduler.go
package kernel

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ErrSchedulerStopped 表示调度器已停止，不再接受任务。
var ErrSchedulerStopped = errors.New("kernel: scheduler stopped")

type pqItem struct {
	task *Task
	seq  uint64
}

// priorityQueue 是最大堆：Priority 大者优先，同优先级按 seq 保证 FIFO。
type priorityQueue []pqItem

func (q priorityQueue) Len() int { return len(q) }
func (q priorityQueue) Less(i, j int) bool {
	if q[i].task.Priority != q[j].task.Priority {
		return q[i].task.Priority > q[j].task.Priority
	}
	return q[i].seq < q[j].seq
}
func (q priorityQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *priorityQueue) Push(x any)    { *q = append(*q, x.(pqItem)) }
func (q *priorityQueue) Pop() any {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}

// priorityScheduler 自研有界优先级调度器：
// Submit 进入 mutex 保护的有界最大堆（背压），dispatch goroutine 按优先级
// 把任务发布到分片全局队列，WorkerPool 通过 work-stealing 执行。
type priorityScheduler struct {
	mu       sync.Mutex
	cond     *sync.Cond
	pq       priorityQueue
	seq      uint64
	capacity int
	workers  int
	global   *ShardedQueue
	pool     *WorkerPool

	submitted atomic.Uint64
	completed atomic.Uint64
	failed    atomic.Uint64
	inFlight  atomic.Int64
	started   atomic.Bool
	stopped   atomic.Bool
	stopOnce  sync.Once
}

// NewScheduler 创建自研优先级调度器；workers 决定并行度，capacity 决定有界队列上限。
func NewScheduler(workers, capacity int) Scheduler {
	if workers < 1 {
		workers = 1
	}
	if capacity < 1 {
		capacity = 1
	}
	per := (capacity + workers - 1) / workers
	if per < 2 {
		per = 2
	}
	g := NewShardedQueue(workers, per)
	s := &priorityScheduler{
		capacity: capacity,
		workers:  workers,
		global:   g,
	}
	s.cond = sync.NewCond(&s.mu)
	heap.Init(&s.pq)
	s.pool = NewWorkerPool(workers, g, s.execute)
	return s
}

// Submit 非阻塞投递；队列满返回 ErrQueueFull，已停止返回 ErrSchedulerStopped。
func (s *priorityScheduler) Submit(t *Task) error {
	if t == nil {
		return ErrNilTask
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped.Load() {
		return ErrSchedulerStopped
	}
	if s.pq.Len() >= s.capacity {
		return ErrQueueFull
	}
	if t.Created.IsZero() {
		t.Created = time.Now()
	}
	s.seq++
	heap.Push(&s.pq, pqItem{task: t, seq: s.seq})
	s.submitted.Add(1)
	s.cond.Signal()
	return nil
}

// Start 启动 worker 池与优先级分发 goroutine（幂等）。
func (s *priorityScheduler) Start() {
	if !s.started.CompareAndSwap(false, true) {
		return
	}
	s.pool.Start()
	go s.dispatch()
}

// Stop 停止接收新任务并优雅关闭 worker 池（幂等）。
func (s *priorityScheduler) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped.Store(true)
		s.cond.Broadcast()
		s.mu.Unlock()
		s.pool.Stop()
	})
}

// Stats 返回原子计数快照。
func (s *priorityScheduler) Stats() Stats {
	s.mu.Lock()
	depth := s.pq.Len()
	s.mu.Unlock()
	return Stats{
		Submitted:  s.submitted.Load(),
		Completed:  s.completed.Load(),
		Failed:     s.failed.Load(),
		InFlight:   s.inFlight.Load(),
		Workers:    s.workers,
		QueueDepth: depth + int(s.global.Len()),
	}
}

func (s *priorityScheduler) dispatch() {
	for {
		s.mu.Lock()
		for s.pq.Len() == 0 && !s.stopped.Load() {
			s.cond.Wait()
		}
		if s.pq.Len() == 0 {
			s.mu.Unlock()
			if s.stopped.Load() {
				return
			}
			continue
		}
		it := heap.Pop(&s.pq).(pqItem)
		s.mu.Unlock()

		for {
			if err := s.global.Push(it.task); err == nil {
				break
			}
			if s.stopped.Load() {
				return
			}
			time.Sleep(100 * time.Microsecond)
		}
	}
}

func (s *priorityScheduler) execute(ctx context.Context, t *Task) error {
	s.inFlight.Add(1)
	defer s.inFlight.Add(-1)

	var err error
	if t.Fn != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("kernel: worker panic: %v", r)
				}
			}()
			err = t.Fn(ctx)
		}()
	}
	if err != nil {
		s.failed.Add(1)
	} else {
		s.completed.Add(1)
	}
	return err
}
