// Package kernel —— llm-hub 自研超级并发内核的基线契约。
// 本文件由 Lead 冻结：只定义对外接口，具体实现由「团队5 自研超级并发内核」
// 在 kernel/ 的其他文件中完成，禁止删除或改名本文件中的类型与方法。
package kernel

import (
	"context"
	"io"
	"net/http"
	"time"
)

// Task 是提交给调度器的最小执行单元。Priority 数值越大优先级越高。
type Task struct {
	ID       string
	Priority int
	Fn       func(context.Context) error
	Created  time.Time
}

// Stats 是调度器的原子快照。
type Stats struct {
	Submitted  uint64
	Completed  uint64
	Failed     uint64
	InFlight   int64
	Workers    int
	QueueDepth int
}

// Scheduler 有界优先级任务调度器。
type Scheduler interface {
	Submit(*Task) error
	Start()
	Stop()
	Stats() Stats
}

// Channel 是参与负载均衡选择的上游渠道视图。
type Channel struct {
	ID            string
	Weight        int
	Priority      int
	InFlight      int64
	Healthy       bool
	CooldownUntil time.Time
}

// Result 是一次上游调用的结果，用于熔断与在途计数回写。
type Result struct {
	OK      bool
	Latency time.Duration
	Status  int
	Err     error
}

// Selector 加权轮询 / 最少在途 / 优先级 + 熔断的渠道选择器。
type Selector interface {
	Pick(candidates []Channel) (Channel, error)
	Report(Channel, Result)
}

// StreamPipe SSE 零拷贝背压转发，ctx 取消时立即关闭上游。
type StreamPipe interface {
	Forward(ctx context.Context, w http.ResponseWriter, up io.Reader) error
}

// Limiter 令牌桶限流。
type Limiter interface {
	Allow(key string, cost int64) (bool, error)
}
