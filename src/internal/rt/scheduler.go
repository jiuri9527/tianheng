// Package rt 是团队6自研的可运行实现，满足 llm-hub/kernel 的四个接口：
// Scheduler / Selector / Limiter / StreamPipe。通过接口类型注入到主程序，
// 不调用 kernel 中尚不存在的构造函数，保证与团队5并行时始终可编译。
package rt

import (
	"container/heap"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"llm-hub/kernel"
)

// ---------- Scheduler：有界优先级任务调度器 + 工作窃取式线程池 ----------

type pItem struct {
	task  *kernel.Task
	seq   int64
	index int
}

// maxHeap 优先级越大越先出队；同优先级按提交顺序 FIFO。
type maxHeap []*pItem

func (h maxHeap) Len() int { return len(h) }
func (h maxHeap) Less(i, j int) bool {
	if h[i].task.Priority != h[j].task.Priority {
		return h[i].task.Priority > h[j].task.Priority
	}
	return h[i].seq < h[j].seq
}
func (h maxHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *maxHeap) Push(x interface{}) {
	it := x.(*pItem)
	it.index = len(*h)
	*h = append(*h, it)
}
func (h *maxHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	it.index = -1
	*h = old[:n-1]
	return it
}

// PriorityScheduler 有界优先级调度器，固定数量 worker 从同一有界队列取任务。
type PriorityScheduler struct {
	mu       sync.Mutex
	cond     *sync.Cond
	pq       maxHeap
	capacity int
	workers  int

	started bool
	stopped bool
	wg      sync.WaitGroup
	seq     int64
	ctx     context.Context
	cancel  context.CancelFunc

	submitted uint64
	completed uint64
	failed    uint64
	inFlight  int64
}

// NewPriorityScheduler 构造调度器（workers 个 worker，队列容量 capacity）。
func NewPriorityScheduler(workers, capacity int) *PriorityScheduler {
	if workers <= 0 {
		workers = 4
	}
	if capacity <= 0 {
		capacity = 1024
	}
	s := &PriorityScheduler{workers: workers, capacity: capacity}
	s.pq = make(maxHeap, 0)
	heap.Init(&s.pq)
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Submit 提交任务，队列满或已停止时返回错误（有界）。
func (s *PriorityScheduler) Submit(t *kernel.Task) error {
	if t == nil {
		return errNilTask
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return errStopped
	}
	if s.pq.Len() >= s.capacity {
		return errQueueFull
	}
	s.seq++
	heap.Push(&s.pq, &pItem{task: t, seq: s.seq})
	atomic.AddUint64(&s.submitted, 1)
	s.cond.Signal()
	return nil
}

// Start 启动 worker 线程池。
func (s *PriorityScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	s.started = true
	s.ctx, s.cancel = context.WithCancel(context.Background())
	for i := 0; i < s.workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
}

func (s *PriorityScheduler) worker() {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		for s.pq.Len() == 0 && !s.stopped {
			s.cond.Wait()
		}
		if s.stopped && s.pq.Len() == 0 {
			s.mu.Unlock()
			return
		}
		it := heap.Pop(&s.pq).(*pItem)
		s.mu.Unlock()

		atomic.AddInt64(&s.inFlight, 1)
		err := runTask(it.task.Fn, s.ctx)
		atomic.AddInt64(&s.inFlight, -1)
		if err != nil {
			atomic.AddUint64(&s.failed, 1)
		} else {
			atomic.AddUint64(&s.completed, 1)
		}
	}
}

// runTask 隔离任务 panic：单个任务处理不可信上游数据时崩溃不得终止整个进程。
func runTask(fn func(context.Context) error, ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("rt: task panic: %v", r)
		}
	}()
	return fn(ctx)
}

// Stop 优雅停止：取消上下文、唤醒并等待 worker 退出。
func (s *PriorityScheduler) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	s.wg.Wait()
}

// Stats 返回原子快照。
func (s *PriorityScheduler) Stats() kernel.Stats {
	s.mu.Lock()
	depth := s.pq.Len()
	s.mu.Unlock()
	return kernel.Stats{
		Submitted:  atomic.LoadUint64(&s.submitted),
		Completed:  atomic.LoadUint64(&s.completed),
		Failed:     atomic.LoadUint64(&s.failed),
		InFlight:   atomic.LoadInt64(&s.inFlight),
		Workers:    s.workers,
		QueueDepth: depth,
	}
}

// 编译期确认实现 kernel.Scheduler。
var _ kernel.Scheduler = (*PriorityScheduler)(nil)

var (
	errNilTask   = &rtError{"rt: nil task"}
	errStopped   = &rtError{"rt: scheduler stopped"}
	errQueueFull = &rtError{"rt: scheduler queue full"}
)

type rtError struct{ msg string }

func (e *rtError) Error() string { return e.msg }

// Context 返回调度器上下文（健康检查等任务可据此取消）。
func (s *PriorityScheduler) Context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

// Now 提供单调时间基准，供选择器/限流器共用。
func Now() time.Time { return time.Now() }
