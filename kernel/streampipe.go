// 文件: kernel/streampipe.go
package kernel

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// ErrNoFlusher 表示 ResponseWriter 不支持流式刷新。
var ErrNoFlusher = errors.New("kernel: ResponseWriter does not implement http.Flusher")

type streamPipe struct {
	bufSize      int
	onFirstChunk func(time.Duration)
}

// NewStreamPipe 创建 SSE 零拷贝背压转发器；bufSize 为单次读缓冲，
// onFirstChunk 回调用于 TTFT（首字延迟）打点。
func NewStreamPipe(bufSize int, onFirstChunk func(time.Duration)) StreamPipe {
	if bufSize <= 0 {
		bufSize = 64 * 1024
	}
	return &streamPipe{bufSize: bufSize, onFirstChunk: onFirstChunk}
}

// Forward 逐行读上游并立刻 Flush 到下游，不缓冲整块、不重组 SSE event。
// 支持 : ping 注释行、data: [DONE] 终止、Anthropic 多行 event 原样透传；
// ctx 取消时关闭上游 reader（若实现 io.Closer）并立即返回。
func (p *streamPipe) Forward(ctx context.Context, w http.ResponseWriter, up io.Reader) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return ErrNoFlusher
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			if c, ok := up.(io.Closer); ok {
				_ = c.Close()
			}
		case <-done:
		}
	}()

	br := bufio.NewReaderSize(up, p.bufSize)
	start := time.Now()
	var first atomic.Bool
	doneMarker := []byte("[DONE]")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 {
			// SSE 把 \r 也视为行分隔符；剔除裸 CR，防止恶意上游向下游注入
			// 伪造的 data:/event: 帧（CRLF 归一为 LF，不影响正常 SSE）。
			chunk = bytes.ReplaceAll(chunk, []byte("\r"), nil)
			if _, werr := w.Write(chunk); werr != nil {
				return werr
			}
			flusher.Flush()
			if first.CompareAndSwap(false, true) && p.onFirstChunk != nil {
				p.onFirstChunk(time.Since(start))
			}
			trimmed := bytes.TrimSpace(chunk)
			if bytes.HasPrefix(trimmed, []byte("data:")) && bytes.Contains(trimmed, doneMarker) {
				return nil
			}
		}
		if err == nil {
			continue
		}
		if err == bufio.ErrBufferFull {
			// 超长行已分段写出，继续读取剩余部分
			continue
		}
		if err == io.EOF {
			return nil
		}
		return err
	}
}
