// 文件: kernel/pool.go
package kernel

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrPoolClosed 表示连接池已关闭。
var ErrPoolClosed = errors.New("kernel: connection pool closed")

// PooledConn 是池化连接的包装。
type PooledConn struct {
	Value    any
	created  time.Time
	lastUsed time.Time
}

// Pool 自研通用连接池：有界空闲队列 + 健康检查 + 最大寿命 + 关闭清理。
type Pool struct {
	mu          sync.Mutex
	idle        []*PooledConn
	maxIdle     int
	maxLifetime time.Duration
	newFn       func(context.Context) (any, error)
	closeFn     func(any) error
	healthFn    func(any) bool
	closed      bool
}

// NewPool 创建连接池。newFn 必须返回可用连接；closeFn 负责释放；
// healthFn 返回 false 时该连接被丢弃并重建（可为 nil 表示不做健康检查）。
func NewPool(maxIdle int, maxLifetime time.Duration,
	newFn func(context.Context) (any, error),
	closeFn func(any) error,
	healthFn func(any) bool) *Pool {
	if maxIdle < 1 {
		maxIdle = 8
	}
	if newFn == nil {
		newFn = func(context.Context) (any, error) { return struct{}{}, nil }
	}
	if closeFn == nil {
		closeFn = func(any) error { return nil }
	}
	return &Pool{
		idle:        make([]*PooledConn, 0, maxIdle),
		maxIdle:     maxIdle,
		maxLifetime: maxLifetime,
		newFn:       newFn,
		closeFn:     closeFn,
		healthFn:    healthFn,
	}
}

// Acquire 取一条健康连接；空闲队列无可复用连接时新建。
func (p *Pool) Acquire(ctx context.Context) (*PooledConn, error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, ErrPoolClosed
		}
		if n := len(p.idle); n > 0 {
			c := p.idle[n-1]
			p.idle = p.idle[:n-1]
			p.mu.Unlock()

			stale := p.maxLifetime > 0 && time.Since(c.created) > p.maxLifetime
			unhealthy := p.healthFn != nil && !p.healthFn(c.Value)
			if stale || unhealthy {
				_ = p.closeFn(c.Value)
				continue
			}
			c.lastUsed = time.Now()
			return c, nil
		}
		p.mu.Unlock()

		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v, err := p.newFn(ctx)
		if err != nil {
			return nil, err
		}
		return &PooledConn{Value: v, created: time.Now(), lastUsed: time.Now()}, nil
	}
}

// Release 归还连接；池满 / 已关闭 / 超寿命时直接释放。
func (p *Pool) Release(c *PooledConn) {
	if c == nil {
		return
	}
	p.mu.Lock()
	expired := p.maxLifetime > 0 && time.Since(c.created) > p.maxLifetime
	if p.closed || len(p.idle) >= p.maxIdle || expired {
		p.mu.Unlock()
		_ = p.closeFn(c.Value)
		return
	}
	c.lastUsed = time.Now()
	p.idle = append(p.idle, c)
	p.mu.Unlock()
}

// Len 返回当前空闲连接数。
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle)
}

// Close 关闭池并释放全部空闲连接。
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	conns := p.idle
	p.idle = nil
	p.mu.Unlock()
	for _, c := range conns {
		_ = p.closeFn(c.Value)
	}
	return nil
}
