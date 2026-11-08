// 文件: kernel/scheduler_test.go
package kernel

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

func waitCompleted(t *testing.T, s Scheduler, want uint64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.Stats().Completed >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting Completed>=%d, stats=%+v", want, s.Stats())
}

func TestSchedulerPriorityOrder(t *testing.T) {
	s := NewScheduler(1, 1024) // 单 worker：执行顺序 = 优先级发布顺序
	var mu sync.Mutex
	var seq []int

	// 先全部投递再 Start，保证 dispatch 看到完整的优先级堆
	for i := 0; i < 12; i++ {
		p := i % 4
		priority := p
		if err := s.Submit(&Task{
			ID:       "t",
			Priority: priority,
			Fn: func(context.Context) error {
				mu.Lock()
				seq = append(seq, priority)
				mu.Unlock()
				return nil
			},
		}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	s.Start()
	defer s.Stop()
	waitCompleted(t, s, 12, 5*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(seq) != 12 {
		t.Fatalf("executed=%d want 12", len(seq))
	}
	for i := 1; i < len(seq); i++ {
		if seq[i] > seq[i-1] {
			t.Fatalf("priority order violated at %d: %v", i, seq)
		}
	}
}

func TestSchedulerBackpressure(t *testing.T) {
	s := NewScheduler(1, 2)
	if err := s.Submit(&Task{ID: "a", Fn: func(context.Context) error { return nil }}); err != nil {
		t.Fatalf("submit a: %v", err)
	}
	if err := s.Submit(&Task{ID: "b", Fn: func(context.Context) error { return nil }}); err != nil {
		t.Fatalf("submit b: %v", err)
	}
	if err := s.Submit(&Task{ID: "c"}); err != ErrQueueFull {
		t.Fatalf("third submit err=%v want ErrQueueFull", err)
	}
	s.Start()
	defer s.Stop()
	waitCompleted(t, s, 2, 5*time.Second)
}

func TestSchedulerStatsAndPanic(t *testing.T) {
	s := NewScheduler(4, 1<<12)
	const n = 200
	for i := 0; i < n; i++ {
		i := i
		fn := func(context.Context) error { return nil }
		if i%25 == 0 {
			fn = func(context.Context) error { panic("boom") }
		}
		for {
			if err := s.Submit(&Task{ID: "t", Fn: fn}); err == nil {
				break
			}
			runtime.Gosched()
		}
	}
	s.Start()
	defer s.Stop()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := s.Stats()
		if st.Completed+st.Failed >= n {
			break
		}
		time.Sleep(time.Millisecond)
	}
	st := s.Stats()
	if st.Submitted != n {
		t.Fatalf("Submitted=%d want %d", st.Submitted, n)
	}
	if st.Completed+st.Failed != n {
		t.Fatalf("Completed+Failed=%d want %d (stats=%+v)", st.Completed+st.Failed, n, st)
	}
	if st.Failed == 0 {
		t.Fatalf("expected recovered panics to be counted as failed")
	}
	if st.Workers != 4 {
		t.Fatalf("Workers=%d want 4", st.Workers)
	}
}

func TestSchedulerStopRejectsSubmit(t *testing.T) {
	s := NewScheduler(2, 16)
	s.Start()
	s.Stop()
	if err := s.Submit(&Task{ID: "x"}); err != ErrSchedulerStopped {
		t.Fatalf("err=%v want ErrSchedulerStopped", err)
	}
}
