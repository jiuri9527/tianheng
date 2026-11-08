package api

import (
	"fmt"
	"strings"
	"sync/atomic"

	"llm-hub/src/internal/rt"
)

// collectMetrics 把 rt 全局原子指标渲染为 Prometheus 文本。
func collectMetrics(b *strings.Builder) {
	rt.GlobalMetrics.RequestsTotal.Range(func(k, v interface{}) bool {
		key, _ := k.(string)
		parts := strings.SplitN(key, "|", 3)
		model, channel, status := "", "", ""
		if len(parts) > 0 {
			model = parts[0]
		}
		if len(parts) > 1 {
			channel = parts[1]
		}
		if len(parts) > 2 {
			status = parts[2]
		}
		val := atomic.LoadInt64(v.(*int64))
		fmt.Fprintf(b, "llmhub_requests_total{model=%q,channel=%q,status=%q} %d\n", model, channel, status, val)
		return true
	})

	b.WriteString("# HELP llmhub_ttft_seconds Time to first token (sum/count).\n")
	b.WriteString("# TYPE llmhub_ttft_seconds summary\n")
	ttftSum := atomic.LoadInt64(&rt.GlobalMetrics.TTFTNanosTotal)
	ttftCount := atomic.LoadInt64(&rt.GlobalMetrics.TTFTCount)
	fmt.Fprintf(b, "llmhub_ttft_seconds_sum %f\n", float64(ttftSum)/1e9)
	fmt.Fprintf(b, "llmhub_ttft_seconds_count %d\n", ttftCount)

	b.WriteString("# HELP llmhub_tokens_total Total tokens by type.\n")
	b.WriteString("# TYPE llmhub_tokens_total counter\n")
	rt.GlobalMetrics.TokensTotal.Range(func(k, v interface{}) bool {
		kind, _ := k.(string)
		val := atomic.LoadInt64(v.(*int64))
		fmt.Fprintf(b, "llmhub_tokens_total{type=%q} %d\n", kind, val)
		return true
	})
}
