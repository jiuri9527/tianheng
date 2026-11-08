// 文件: kernel/worker.go
package kernel

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// worker 是 work-stealing 线程池中的单个执行单元：
// 先消费本地双端队列，再消费全局分片队列，最后跨 worker 偷取。
type worker struct {
	id     int
	local  *MPMCQueue
	global *ShardedQueue
	peers  []*worker
	fn     func(context.Context, *Task) error
	stop   <-chan struct{}
	wg     *sync.WaitGroup
	idle   atomic.Int64
	stolen atomic.Uint64
}

// WorkerPool 自研 work-stealing 线程池。
type WorkerPool struct {
	workers []*worker
	global  *ShardedQueue
	stop    chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	started atomic.Bool
}

// NewWorkerPool 创建 n 个 worker，共享 global 分片队列。
func NewWorkerPool(n int, global *ShardedQueue, fn func(context.Context, *Task) error) *WorkerPool {
	if n < 1 {
		n = 1
	}
	if global == nil {
		global = NewShardedQueue(n, 1024)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &WorkerPool{
		global: global,
		stop:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}
	p.workers = make([]*worker, n)
	for i := 0; i < n; i++ {
		p.workers[i] = &worker{
			id: i, local: NewMPMCQueue(1024), global: global,
			fn: fn, stop: p.stop, wg: &p.wg,
		}
	}
	for _, w := range p.workers {
		w.peers = p.workers
	}
	return p
}

// Local 返回第 i 个 worker 的本地队列，供外部做本地优先投递。
func (p *WorkerPool) Local(i int) *MPMCQueue { return p.workers[i%len(p.workers)].local }

// Workers 返回 worker 数量。
func (p *WorkerPool) Workers() int { return len(p.workers) }

// Start 启动所有 worker goroutine（幂等）。
func (p *WorkerPool) Start() {
	if !p.started.CompareAndSwap(false, true) {
		return
	}
	p.wg.Add(len(p.workers))
	for _, w := range p.workers {
		go w.loop(p.ctx)
	}
}

// Stop 优雅停止：关闭停止信号、取消 ctx、等待所有 worker 排空本地队列后退出。
func (p *WorkerPool) Stop() {
	p.once.Do(func() {
		close(p.stop)
		p.cancel()
		p.wg.Wait()
	})
}

// Stolen 返回累计跨 worker 窃取次数。
func (p *WorkerPool) Stolen() uint64 {
	var n uint64
	for _, w := range p.workers {
		n += w.stolen.Load()
	}
	return n
}

func (w *worker) loop(ctx context.Context) {
	defer w.wg.Done()
	for {
		select {
		case <-w.stop:
			w.drain(ctx)
			return
		default:
		}

		t, err := w.local.TryPop()
		if err != nil {
			t, err = w.global.Pop(w.id)
			if err != nil {
				t = w.steal()
			}
		}
		if t == nil {
			w.idle.Add(1)
			select {
			case <-w.stop:
				w.drain(ctx)
				return
			case <-time.After(200 * time.Microsecond):
			}
			continue
		}
		w.run(ctx, t)
	}
}

func (w *worker) steal() *Task {
	for i := 1; i <= len(w.peers); i++ {
		p := w.peers[(w.id+i)%len(w.peers)]
		if p == w {
			continue
		}
		if t, err := p.local.TryPop(); err == nil {
			w.stolen.Add(1)
			return t
		}
	}
	return nil
}

func (w *worker) run(ctx context.Context, t *Task) {
	defer func() { _ = recover() }()
	if w.fn != nil {
		_ = w.fn(ctx, t)
	}
}

func (w *worker) drain(ctx context.Context) {
	for {
		t, err := w.local.TryPop()
		if err != nil {
			return
		}
		w.run(ctx, t)
	}
}
