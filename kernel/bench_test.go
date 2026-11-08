// 文件: kernel/bench_test.go
package kernel

import (
	"context"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func BenchmarkQueue(b *testing.B) {
	q := NewMPMCQueue(1 << 16)
	t := &Task{ID: "bench"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = q.TryPush(t)
		_, _ = q.TryPop()
	}
}

func BenchmarkScheduler(b *testing.B) {
	s := NewScheduler(8, 1<<16)
	s.Start()
	fn := func(context.Context) error { return nil }
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for {
			if err := s.Submit(&Task{ID: "bench", Fn: fn}); err == nil {
				break
			}
			runtime.Gosched()
		}
	}
	for s.Stats().Completed < uint64(b.N) {
		time.Sleep(time.Millisecond)
	}
	b.StopTimer()
	s.Stop()
}

func BenchmarkStreamPipe(b *testing.B) {
	chunk := "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"
	payload := strings.Repeat(chunk, 32)
	rec := httptest.NewRecorder()
	p := NewStreamPipe(4096, nil)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec.Body.Reset()
		_ = p.Forward(context.Background(), rec, strings.NewReader(payload))
	}
}
