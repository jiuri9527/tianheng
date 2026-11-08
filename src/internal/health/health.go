// Package health 实现定时健康检查与周期模型同步（F8 健康检查 / F3 定时同步）。
// 健康检查任务通过 kernel.Scheduler 接口注入提交（团队5/rt 实现）。
package health

import (
	"context"
	"sync"
	"time"

	"llm-hub/kernel"
	"llm-hub/src/internal/discovery"
	"llm-hub/src/internal/router"
	"llm-hub/src/internal/store"
)

// Checker 定时健康检查器。
type Checker struct {
	St         *store.Store
	Discoverer *discovery.Discoverer
	Scheduler  kernel.Scheduler
	Router     *router.Router
	Interval   time.Duration
	stopCh     chan struct{}
	wg         sync.WaitGroup
	mu         sync.Mutex
	running    bool
}

// New 构造检查器。
func New(st *store.Store, disc *discovery.Discoverer, sched kernel.Scheduler, r *router.Router, interval time.Duration) *Checker {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &Checker{
		St: st, Discoverer: disc, Scheduler: sched, Router: r,
		Interval: interval, stopCh: make(chan struct{}),
	}
}

// Start 启动定时循环。
func (c *Checker) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return
	}
	c.running = true
	c.stopCh = make(chan struct{})
	c.wg.Add(1)
	go func(stop <-chan struct{}) {
		defer c.wg.Done()
		ticker := time.NewTicker(c.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				c.CheckAll(context.Background())
			}
		}
	}(c.stopCh)
}

// Stop 停止定时循环。
func (c *Checker) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	close(c.stopCh)
	c.mu.Unlock()
	c.wg.Wait()
}

// CheckAll 对所有 active 渠道做一轮轻量 /models 健康检查。
func (c *Checker) CheckAll(ctx context.Context) int {
	channels := c.St.ListChannels()
	count := 0
	for _, ch := range channels {
		if ch.Status != "active" {
			continue
		}
		count++
		one := ch
		task := &kernel.Task{
			ID: "health:" + one.ID, Priority: 0, Created: time.Now(),
			Fn: func(taskCtx context.Context) error {
				c.checkChannel(taskCtx, one)
				return nil
			},
		}
		if c.Scheduler != nil {
			if err := c.Scheduler.Submit(task); err == nil {
				continue
			}
		}
		// 调度器不可用/队列满则直接本地执行
		c.checkChannel(ctx, one)
	}
	return count
}

func (c *Checker) checkChannel(ctx context.Context, ch *store.Channel) {
	if c.Discoverer == nil {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	models, err := c.Discoverer.ListModels(checkCtx, ch)
	c.mu.Lock()
	defer c.mu.Unlock()
	failed := err != nil
	// store 锁内只改渠道字段；Router.SetCooldown 内部也会取 store 锁，
	// 必须放在锁外调用，避免 self-deadlock。
	c.St.UpdateChannelLocked(ch.ID, func(cc *store.Channel) {
		cc.HealthCheckedAt = time.Now().Unix()
		if failed {
			cc.FailCount++
			cc.LastError = err.Error()
			cc.CooldownUntil = time.Now().Add(30 * time.Second).Unix()
		} else {
			cc.FailCount = 0
			cc.LastError = ""
			cc.CooldownUntil = 0
			_ = models
		}
	})
	if failed && c.Router != nil {
		c.Router.SetCooldown(ch.ID, time.Now().Add(30*time.Second))
	}
}
