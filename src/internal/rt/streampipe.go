package rt

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"llm-hub/kernel"
)

// ZeroCopyStreamPipe 实现 kernel.StreamPipe：逐块零缓冲转发 SSE，ctx 取消立即返回。
type ZeroCopyStreamPipe struct {
	bufSize int
}

// NewStreamPipe 构造流式转发器。
func NewStreamPipe() *ZeroCopyStreamPipe { return &ZeroCopyStreamPipe{bufSize: 32 * 1024} }

// Forward 从 up 逐块读取并写入 w，每块立即 Flush；ctx 取消时返回 ctx.Err()。
// 上游连接的关闭由调用方在 ctx.Done() 时完成（relay 层负责），从而解除 Read 阻塞。
func (p *ZeroCopyStreamPipe) Forward(ctx context.Context, w http.ResponseWriter, up io.Reader) error {
	flusher, _ := w.(http.Flusher)
	reader := bufio.NewReaderSize(up, p.bufSize)
	buf := make([]byte, p.bufSize)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := reader.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

var _ kernel.StreamPipe = (*ZeroCopyStreamPipe)(nil)

// ---------- 原子指标计数器（供 /metrics） ----------

// Metrics 全进程原子指标。
type Metrics struct {
	RequestsTotal  sync.Map // key: model|channel|status -> *int64 计数
	TokensTotal    sync.Map // key: prompt|completion -> *int64
	TTFTNanosTotal int64
	TTFTCount      int64
}

// GlobalMetrics 单例。
var GlobalMetrics = &Metrics{}

const (
	maxMetricPartLen = 128
	maxMetricKeys    = 10000
)

var metricKeyCount int64

// metricPart 清洗指标键分量：剔除分隔符与控制字符并限长，防止键注入与无界增长。
func metricPart(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '|' || r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, s)
	if len(s) > maxMetricPartLen {
		s = s[:maxMetricPartLen]
	}
	return s
}

// AddRequest 累加请求计数（键基数有上限，超过归入 other 桶）。
func (m *Metrics) AddRequest(model, channel, status string) {
	key := metricPart(model) + "|" + metricPart(channel) + "|" + metricPart(status)
	v, loaded := m.RequestsTotal.LoadOrStore(key, new(int64))
	if !loaded {
		if atomic.AddInt64(&metricKeyCount, 1) > maxMetricKeys {
			m.RequestsTotal.Delete(key)
			atomic.AddInt64(&metricKeyCount, -1)
			v, _ = m.RequestsTotal.LoadOrStore("other|other|other", new(int64))
		}
	}
	addInt64(v.(*int64), 1)
}

// AddTokens 累加 token 计数；负数用量不得回冲指标。
func (m *Metrics) AddTokens(kind string, n int64) {
	if n < 0 {
		return
	}
	v, _ := m.TokensTotal.LoadOrStore(metricPart(kind), new(int64))
	addInt64(v.(*int64), n)
}

// AddTTFT 记录首个 token 时间。
func (m *Metrics) AddTTFT(nanos int64) {
	addInt64(&m.TTFTNanosTotal, nanos)
	addInt64(&m.TTFTCount, 1)
}

func addInt64(p *int64, delta int64) {
	atomic.AddInt64(p, delta)
}
