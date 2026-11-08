// 文件: kernel/metrics.go
package kernel

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

type metricKey struct {
	name   string
	labels string
}

type atomicCounter struct{ v atomic.Uint64 }

// Metrics 自研原子指标集：计数器与 gauge 全部走 atomic，
// Render 输出 Prometheus 文本格式（/metrics 直接返回，无需第三方 client）。
type Metrics struct {
	mu       sync.RWMutex
	counters map[metricKey]*atomicCounter
	gauges   map[string]*atomic.Int64
}

// NewMetrics 创建空指标集。
func NewMetrics() *Metrics {
	return &Metrics{
		counters: make(map[metricKey]*atomicCounter),
		gauges:   make(map[string]*atomic.Int64),
	}
}

func (m *Metrics) counter(name, labels string) *atomicCounter {
	k := metricKey{name: name, labels: labels}
	m.mu.RLock()
	c, ok := m.counters[k]
	m.mu.RUnlock()
	if ok {
		return c
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok = m.counters[k]; ok {
		return c
	}
	c = &atomicCounter{}
	m.counters[k] = c
	return c
}

// Inc 对指定 name+labels 计数器 +1。
func (m *Metrics) Inc(name, labels string) { m.counter(name, labels).v.Add(1) }

// Add 对指定 name+labels 计数器 +n。
func (m *Metrics) Add(name, labels string, n uint64) { m.counter(name, labels).v.Add(n) }

// Get 读取计数器当前值（不存在返回 0）。
func (m *Metrics) Get(name, labels string) uint64 {
	m.mu.RLock()
	c, ok := m.counters[metricKey{name: name, labels: labels}]
	m.mu.RUnlock()
	if !ok {
		return 0
	}
	return c.v.Load()
}

// Set 设置 gauge 值。
func (m *Metrics) Set(name string, v int64) {
	m.mu.RLock()
	g, ok := m.gauges[name]
	m.mu.RUnlock()
	if ok {
		g.Store(v)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok = m.gauges[name]; ok {
		g.Store(v)
		return
	}
	g = &atomic.Int64{}
	g.Store(v)
	m.gauges[name] = g
}

// escapeLabel 只处理反斜杠与换行；labels 参数是调用方已构造好的
// Prometheus label 列表（形如 `model="x",status="200"`），其中的双引号是
// 结构字符，不能被转义，否则会破坏 exposition 格式。
func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// Render 输出完整的 Prometheus 文本 exposition，同名指标合并 TYPE 行。
func (m *Metrics) Render() string {
	m.mu.RLock()
	type row struct {
		name   string
		labels string
		value  uint64
	}
	rows := make([]row, 0, len(m.counters))
	for k, c := range m.counters {
		rows = append(rows, row{name: k.name, labels: k.labels, value: c.v.Load()})
	}
	gauges := make(map[string]int64, len(m.gauges))
	for k, g := range m.gauges {
		gauges[k] = g.Load()
	}
	m.mu.RUnlock()

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].name != rows[j].name {
			return rows[i].name < rows[j].name
		}
		return rows[i].labels < rows[j].labels
	})

	var b strings.Builder
	lastType := ""
	for _, r := range rows {
		if r.name != lastType {
			fmt.Fprintf(&b, "# TYPE %s counter\n", r.name)
			lastType = r.name
		}
		if r.labels == "" {
			fmt.Fprintf(&b, "%s %d\n", r.name, r.value)
		} else {
			fmt.Fprintf(&b, "%s{%s} %d\n", r.name, escapeLabel(r.labels), r.value)
		}
	}
	gnames := make([]string, 0, len(gauges))
	for k := range gauges {
		gnames = append(gnames, k)
	}
	sort.Strings(gnames)
	for _, k := range gnames {
		fmt.Fprintf(&b, "# TYPE %s gauge\n%s %d\n", k, k, gauges[k])
	}
	return b.String()
}
