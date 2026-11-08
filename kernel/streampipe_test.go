// 文件: kernel/streampipe_test.go
package kernel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type noFlushWriter struct {
	h    http.Header
	body strings.Builder
	code int
}

func (w *noFlushWriter) Header() http.Header { return w.h }
func (w *noFlushWriter) Write(p []byte) (int, error) { return w.body.Write(p) }
func (w *noFlushWriter) WriteHeader(c int)          { w.code = c }

func TestStreamPipeForwardSSE(t *testing.T) {
	upstream := strings.NewReader(strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start"}`,
		"",
		": ping",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","delta":{"text":"hello"}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n"))

	rec := httptest.NewRecorder()
	var firstCalled atomic.Bool
	p := NewStreamPipe(1024, func(time.Duration) { firstCalled.Store(true) })

	if err := p.Forward(context.Background(), rec, upstream); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"event: message_start",
		": ping",
		"content_block_delta",
		"data: [DONE]",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering=%q", got)
	}
	if !rec.Flushed {
		t.Fatal("expected per-chunk Flush()")
	}
	if !firstCalled.Load() {
		t.Fatal("TTFT callback was not invoked")
	}
}

func TestStreamPipeStopsAtDone(t *testing.T) {
	up := &countingReader{data: "data: {\"a\":1}\n\ndata: [DONE]\n\nSHOULD-NOT-BE-READ\n"}
	rec := httptest.NewRecorder()
	p := NewStreamPipe(64, nil)
	if err := p.Forward(context.Background(), rec, up); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if strings.Contains(rec.Body.String(), "SHOULD-NOT-BE-READ") {
		t.Fatal("read past [DONE] marker")
	}
}

type countingReader struct {
	data string
	pos  int
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	if r.pos >= len(r.data) {
		return n, io.EOF
	}
	return n, nil
}

func TestStreamPipeContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr, pw := io.Pipe()
	defer pw.Close()
	rec := httptest.NewRecorder()
	p := NewStreamPipe(1024, nil)
	if err := p.Forward(ctx, rec, pr); err != context.Canceled {
		t.Fatalf("err=%v want context.Canceled", err)
	}
}

func TestStreamPipeNoFlusher(t *testing.T) {
	w := &noFlushWriter{h: make(http.Header)}
	p := NewStreamPipe(1024, nil)
	if err := p.Forward(context.Background(), w, strings.NewReader("data: x\n\n")); err != ErrNoFlusher {
		t.Fatalf("err=%v want ErrNoFlusher", err)
	}
}
