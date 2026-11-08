package affinity

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func now() time.Time { return time.Unix(1794100000, 0) }

// TestFirstTurnHasNoAffinity 首次出现的会话不粘任何上游。
func TestFirstTurnHasNoAffinity(t *testing.T) {
	s := New(t.TempDir())
	d := s.Decide("conv-1", "auto/dfmodel", "", nil, now())
	if d.Kept {
		t.Fatal("首次会话不应粘住上游")
	}
	if d.Why != WhyFirst {
		t.Fatalf("why 应为 %s，实际 %s", WhyFirst, d.Why)
	}
}

// TestStickyAcrossTurns 多轮会话必须粘住同一上游，并如实累计轮数与缓存量。
func TestStickyAcrossTurns(t *testing.T) {
	s := New(t.TempDir())
	probe := func(id string) (string, string, bool) { return "Trae CN", "deepseek-v4.1-flash-max", true }

	s.Bind("conv-1", "auto/dfmodel", "ch-trae", "Trae CN", "deepseek-v4.1-flash-max", now())
	s.ObserveCache("conv-1", "auto/dfmodel", 0, 515871) // 上游真实回报的缓存命中

	d := s.Decide("conv-1", "auto/dfmodel", "", probe, now().Add(time.Second))
	if !d.Kept {
		t.Fatalf("应继续粘住，why=%s", d.Why)
	}
	if d.Last != "ch-trae" {
		t.Fatalf("last 应为 ch-trae，实际 %s", d.Last)
	}
	if d.Turn != 2 {
		t.Fatalf("轮数应为 2，实际 %d", d.Turn)
	}
	if d.CacheRead != 515871 {
		t.Fatalf("缓存命中应透传，实际 %d", d.CacheRead)
	}
	if d.Why != WhyCache {
		t.Fatalf("有缓存命中时 why 应为 %s，实际 %s", WhyCache, d.Why)
	}
}

// TestKeepAcrossManyTurns 连续 80 轮都粘住（对齐原理来源观测到的 turn=82）。
func TestKeepAcrossManyTurns(t *testing.T) {
	s := New(t.TempDir())
	probe := func(string) (string, string, bool) { return "ZCode", "GLM-5.3", true }
	s.Bind("c", "g", "ch-z", "ZCode", "GLM-5.3", now())

	// 真实调用序列：Decide（判定）→ 发请求 → Bind（成功后推进轮数）
	kept := 1
	for i := 0; i < 80; i++ {
		ts := now().Add(time.Duration(i+1) * time.Second)
		d := s.Decide("c", "g", "", probe, ts)
		if !d.Kept {
			t.Fatalf("第 %d 轮应保持粘住，why=%s", i+2, d.Why)
		}
		kept++
		s.Bind("c", "g", "ch-z", "ZCode", "GLM-5.3", ts)
	}
	if kept != 81 {
		t.Fatalf("80 轮全部应保持粘住，实际 kept=%d", kept)
	}
	e, _ := s.Get("c", "g")
	if e.Turn != 81 {
		t.Fatalf("轮数应累计，实际 %d", e.Turn)
	}
}

// TestUpstreamGoneBreaksAffinity 上游失效时放弃粘性，并如实给出 why=gone。
func TestUpstreamGoneBreaksAffinity(t *testing.T) {
	s := New(t.TempDir())
	s.Bind("c", "g", "ch-x", "X", "m", now())
	probe := func(string) (string, string, bool) { return "", "", false }
	d := s.Decide("c", "g", "", probe, now())
	if d.Kept || d.Why != WhyGone {
		t.Fatalf("上游失效应放弃粘性 why=gone，实际 kept=%v why=%s", d.Kept, d.Why)
	}
}

// TestTTLExpiryRotatesUpstream 超过 TTL 主动轮换，避免流量长期偏向单一上游。
func TestTTLExpiryRotatesUpstream(t *testing.T) {
	s := New(t.TempDir())
	s.SetTTL(30 * time.Minute)
	probe := func(string) (string, string, bool) { return "X", "m", true }
	s.Bind("c", "g", "ch-x", "X", "m", now())

	if d := s.Decide("c", "g", "", probe, now().Add(29*time.Minute)); !d.Kept {
		t.Fatalf("TTL 内应保持粘性，why=%s", d.Why)
	}
	if d := s.Decide("c", "g", "", probe, now().Add(31*time.Minute)); d.Kept || d.Why != WhyTTL {
		t.Fatalf("超过 TTL 应放弃粘性 why=ttl，实际 kept=%v why=%s", d.Kept, d.Why)
	}
}

// TestTurnLimitRotatesUpstream 达到轮数上限后主动轮换。
func TestTurnLimitRotatesUpstream(t *testing.T) {
	s := New(t.TempDir())
	s.SetTurnLimit(3)
	probe := func(string) (string, string, bool) { return "X", "m", true }
	s.Bind("c", "g", "ch-x", "X", "m", now())
	s.Bind("c", "g", "ch-x", "X", "m", now())
	s.Bind("c", "g", "ch-x", "X", "m", now()) // turn=3 达上限
	d := s.Decide("c", "g", "", probe, now())
	if d.Kept || d.Why != WhyLimit {
		t.Fatalf("达轮数上限应轮换 why=limit，实际 kept=%v why=%s", d.Kept, d.Why)
	}
}

// TestModeOffDisablesAffinity 显式关闭粘性（会话维度，不是用户配额）。
func TestModeOffDisablesAffinity(t *testing.T) {
	s := New(t.TempDir())
	s.Bind("c", "g", "ch-x", "X", "m", now())
	d := s.Decide("c", "g", "off", nil, now())
	if d.Kept || d.Why != WhyDisabled {
		t.Fatalf("mode=off 应关闭粘性，实际 kept=%v why=%s", d.Kept, d.Why)
	}
}

// TestUpstreamChangeResetsTurn 换上游后轮数重新计数，不把两个上游的轮数累加。
func TestUpstreamChangeResetsTurn(t *testing.T) {
	s := New(t.TempDir())
	s.Bind("c", "g", "ch-a", "A", "model-a", now())
	s.Bind("c", "g", "ch-a", "A", "model-a", now()) // turn=2
	e := s.Bind("c", "g", "ch-b", "B", "model-b", now())
	if e.Turn != 1 {
		t.Fatalf("换上游后轮数应重置为 1，实际 %d", e.Turn)
	}
	if e.CacheReadTok != 0 || e.CacheWriteTok != 0 {
		t.Fatal("换上游后缓存计数应清零")
	}
}

// TestPersistAndReload 亲和状态原子落盘 0600、目录 0700、重启后可恢复。
func TestPersistAndReload(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	s.Bind("conv-a", "auto/dfmodel", "ch-trae", "Trae CN", "deepseek-v4.1-flash-max", now())
	s.ObserveCache("conv-a", "auto/dfmodel", 1234, 5678)
	sum, err := s.Save(now())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(sum) != 64 {
		t.Fatalf("应返回 SHA-256，实际 %q", sum)
	}
	if mode, err := s.DirMode(); err != nil || mode != 0o700 {
		t.Fatalf("目录权限应为 0700，实际 %o err=%v", mode, err)
	}

	// 文件权限 0600
	if st, err := statFile(s.Path()); err != nil || st != 0o600 {
		t.Fatalf("affinity.json 权限应为 0600，实际 %o err=%v", st, err)
	}

	s2 := New(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	e, ok := s2.Get("conv-a", "auto/dfmodel")
	if !ok {
		t.Fatal("重启后应恢复绑定")
	}
	if e.ChannelID != "ch-trae" || e.CacheReadTok != 5678 || e.Turn != 1 {
		t.Fatalf("恢复内容不符: %+v", e)
	}
}

// TestLoadRejectsWrongFormat 格式标识不符时拒绝载入，不静默接受可疑文件。
func TestLoadRejectsWrongFormat(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if _, err := writeRaw(s.Path(), `{"format":"something-else","entries":[]}`); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(); err == nil {
		t.Fatal("格式不符应报错")
	}
}

// TestStatsAndPrune 统计数字真实、过期清理有效。
func TestStatsAndPrune(t *testing.T) {
	s := New(t.TempDir())
	s.SetTTL(time.Minute)
	s.Bind("c1", "g1", "ch-a", "A", "m", now())
	s.Bind("c1", "g1", "ch-a", "A", "m", now())
	s.ObserveCache("c1", "g1", 100, 900)
	s.Bind("c2", "g2", "ch-b", "B", "m", now())

	st := s.Stats()
	if st.Entries != 2 || st.Groups != 2 || st.Channels != 2 {
		t.Fatalf("统计不符: %+v", st)
	}
	if st.MaxTurn != 2 || st.TotalTurns != 3 {
		t.Fatalf("轮数统计不符: %+v", st)
	}
	if st.CacheWrite != 100 || st.CacheRead != 900 {
		t.Fatalf("缓存统计不符: %+v", st)
	}
	if st.CacheHitPct < 89.9 || st.CacheHitPct > 90.1 {
		t.Fatalf("缓存命中率应约 90%%，实际 %.2f", st.CacheHitPct)
	}

	n := s.Prune(now().Add(5 * time.Minute)) // 超过 2*TTL
	if n != 2 {
		t.Fatalf("应清理 2 条，实际 %d", n)
	}
	if s.Stats().Entries != 0 {
		t.Fatal("清理后应为空")
	}
}

// TestSnapshotOrderedByRecency 快照按最近使用倒序，供面板展示。
func TestSnapshotOrderedByRecency(t *testing.T) {
	s := New(t.TempDir())
	s.Bind("old", "g", "ch-a", "A", "m", now())
	s.Bind("new", "g", "ch-b", "B", "m", now().Add(time.Hour))
	snap := s.Snapshot(10)
	if len(snap) != 2 || snap[0].ConversationID != "new" {
		t.Fatalf("快照应按最近使用倒序: %+v", snap)
	}
}

// ---------- 测试辅助 ----------

func statFile(p string) (os.FileMode, error) {
	st, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return st.Mode().Perm(), nil
}

func writeRaw(p, content string) (int, error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return 0, err
	}
	return len(content), os.WriteFile(p, []byte(content), 0o600)
}
