// Package discovery · auto.go
// 渠道提交后的自动发现编排：自动真实探测、增量同步后的探测补齐、定时刷新。
// 全部模型数据来自真实上游响应；失败一律留空，绝不编造。
package discovery

import (
	"context"
	"os"
	"strconv"
	"sync"
	"time"

	"llm-hub/src/internal/store"
)

// AutoDiscoverOptions 控制「提交渠道后自动发现」是否附带真实探测。
type AutoDiscoverOptions struct {
	Probe bool
}

// AutoDiscoverResult 是一次自动发现的完整结果。
type AutoDiscoverResult struct {
	OK           bool
	Discovered   int
	Models       []*store.Model
	ProbeStarted bool
	Error        string
}

// probeEnabled 读取 F4 探测开关（PROBE_ENABLED，默认开启）。
func (d *Discoverer) probeEnabled() bool {
	return d.Cfg == nil || d.Cfg.ProbeEnabled
}

// AutoDiscover 供 channel.Create / channel.Update 在保存成功后调用：
// 同步真实 /models -> 增量 upsert -> 后台对每条新模型做一次真实极小探测。
// 同步失败时 Discovered=0、OK=false、Models 为空，渠道 probe_status=unavailable。
func (d *Discoverer) AutoDiscover(ctx context.Context, ch *store.Channel, opts AutoDiscoverOptions) AutoDiscoverResult {
	res := d.SyncChannel(ctx, ch)
	out := AutoDiscoverResult{
		OK:         res.OK,
		Discovered: res.Discovered,
		Models:     res.Models,
		Error:      res.Error,
	}
	if res.OK && opts.Probe && d.probeEnabled() && len(res.Models) > 0 {
		d.AutoProbe(ch)
		out.ProbeStarted = true
	}
	return out
}

// ProbeJob 是一次后台真实探测任务，Wait 可等待完成（测试/手动同步用）。
type ProbeJob struct {
	channelID string
	done      chan struct{}
	probed    int
	failed    int
}

func (j *ProbeJob) Wait() (probed, failed int) {
	<-j.done
	return j.probed, j.failed
}

// AutoProbe 对渠道下「尚无真实探测样例」的模型各发一次真实极小请求，
// 采集真实输入 / 输出 / 用量 / 延迟。同一渠道不并发重复探测。
// 使用独立于请求 ctx 的后台 context，客户端断开不会中断真实采集。
func (d *Discoverer) AutoProbe(ch *store.Channel) *ProbeJob {
	job := &ProbeJob{channelID: ch.ID, done: make(chan struct{})}
	d.probeMu.Lock()
	if d.probing == nil {
		d.probing = map[string]bool{}
	}
	if d.probing[ch.ID] {
		d.probeMu.Unlock()
		close(job.done)
		return job
	}
	d.probing[ch.ID] = true
	d.probeMu.Unlock()

	go func() {
		defer func() {
			d.probeMu.Lock()
			delete(d.probing, ch.ID)
			d.probeMu.Unlock()
			close(job.done)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		for _, m := range d.St.ListModelsByChannel(ch.ID) {
			// 已采集过真实样例的模型不重复调用上游，避免浪费真实额度。
			if m.ProbeStatus == "ok" && m.ProbedAt > 0 {
				continue
			}
			one, cancelOne := context.WithTimeout(ctx, 15*time.Second)
			r := d.ProbeModel(one, ch, m)
			cancelOne()
			if r.OK {
				job.probed++
			} else {
				job.failed++
			}
			if ctx.Err() != nil {
				break
			}
		}
	}()
	return job
}

// RefreshInterval 定时刷新周期，来自 MODEL_AUTO_REFRESH_SEC（默认 6 小时）。
// 与 F8 的 60s 健康检查解耦，避免对上游产生高频真实探测调用。
func RefreshInterval() time.Duration {
	sec := 21600
	if v := os.Getenv("MODEL_AUTO_REFRESH_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			sec = n
		}
	}
	return time.Duration(sec) * time.Second
}

// AutoRefresher 定时对所有 active 渠道做一轮真实 /models 同步与探测补齐。
type AutoRefresher struct {
	d        *Discoverer
	interval time.Duration
	mu       sync.Mutex
	stopCh   chan struct{}
	wg       sync.WaitGroup
	started  bool
}

// NewAutoRefresher 构造定时刷新器；interval<=0 时使用默认周期。
func NewAutoRefresher(d *Discoverer, interval time.Duration) *AutoRefresher {
	if interval <= 0 {
		interval = RefreshInterval()
	}
	return &AutoRefresher{d: d, interval: interval, stopCh: make(chan struct{})}
}

// Start 启动后台刷新循环（幂等）。
func (r *AutoRefresher) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return
	}
	r.started = true
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.stopCh:
				return
			case <-ticker.C:
				r.RefreshAll(context.Background())
			}
		}
	}()
}

// Stop 停止后台刷新循环。
func (r *AutoRefresher) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	r.started = false
	close(r.stopCh)
	r.mu.Unlock()
	r.wg.Wait()
}

// RefreshAll 立即对需要刷新的 active 渠道做一轮真实同步；返回 (渠道数, 发现模型数)。
func (r *AutoRefresher) RefreshAll(ctx context.Context) (channels, discovered int) {
	now := time.Now().Unix()
	budget := int64(r.interval.Seconds())
	for _, ch := range r.d.St.ListChannels() {
		if ch.Status != "active" {
			continue
		}
		if ch.HealthCheckedAt > 0 && now-ch.HealthCheckedAt < budget {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		res := r.d.SyncChannel(cctx, ch)
		cancel()
		channels++
		discovered += res.Discovered
		if res.OK && r.d.probeEnabled() && len(res.Models) > 0 {
			r.d.AutoProbe(ch)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return channels, discovered
}
