// 文件: kernel/queue_test.go
package kernel

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMPMCQueueFullAndDrain(t *testing.T) {
	q := NewMPMCQueue(4)
	if q.Cap() != 4 {
		t.Fatalf("Cap()=%d want 4", q.Cap())
	}
	for i := 0; i < 4; i++ {
		if err := q.TryPush(&Task{ID: "t"}); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}
	if err := q.TryPush(&Task{ID: "overflow"}); err != ErrQueueFull {
		t.Fatalf("overflow err=%v want ErrQueueFull", err)
	}
	for i := 0; i < 4; i++ {
		got, err := q.TryPop()
		if err != nil || got == nil {
			t.Fatalf("pop %d: task=%v err=%v", i, got, err)
		}
	}
	if _, err := q.TryPop(); err != ErrQueueEmpty {
		t.Fatalf("empty err=%v want ErrQueueEmpty", err)
	}
}

func TestMPMCQueueConcurrent(t *testing.T) {
	const producers = 8
	const perProducer = 2000
	total := int64(producers * perProducer)
	q := NewMPMCQueue(1 << 12)

	var wg sync.WaitGroup
	var pushed, popped atomic.Int64

	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				for {
					if err := q.TryPush(&Task{ID: "x"}); err == nil {
						pushed.Add(1)
						break
					}
					runtime.Gosched()
				}
			}
		}()
	}
	for c := 0; c < producers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for popped.Load() < total {
				if v, err := q.TryPop(); err == nil && v != nil {
					popped.Add(1)
				} else {
					runtime.Gosched()
				}
			}
		}()
	}
	wg.Wait()

	if pushed.Load() != total || popped.Load() != total {
		t.Fatalf("pushed=%d popped=%d want %d", pushed.Load(), popped.Load(), total)
	}
	if q.Len() != 0 {
		t.Fatalf("queue not drained, Len=%d", q.Len())
	}
}

func TestShardedQueueSteal(t *testing.T) {
	s := NewShardedQueue(4, 8)
	if s.Count() != 4 {
		t.Fatalf("Count=%d want 4", s.Count())
	}
	for i := 0; i < 20; i++ {
		if err := s.Push(&Task{ID: "t"}); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}
	got := 0
	for i := 0; i < 4; i++ {
		for {
			if _, err := s.Pop(i); err == nil {
				got++
				continue
			}
			break
		}
	}
	if got != 20 {
		t.Fatalf("popped=%d want 20", got)
	}
}
