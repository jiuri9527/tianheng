package grouping

import (
	"os"
	"path/filepath"
	"testing"
)

func boolp(b bool) *bool { return &b }

// TestMemberMatch 成员表达式匹配：精确渠道、类型前缀、模型通配、裸模型名。
func TestMemberMatch(t *testing.T) {
	cases := []struct {
		expr     string
		channel  string
		provider string
		model    string
		alias    string
		want     bool
	}{
		{"chan_ab12/deepseek-v4.1-flash", "chan_ab12", "openai", "deepseek-v4.1-flash", "df", true},
		{"chan_ab12/other", "chan_ab12", "openai", "deepseek-v4.1-flash", "df", false},
		{"*/deepseek-*", "chan_zz", "openai", "deepseek-v4.1-flash", "", true},
		{"*/deepseek-*", "chan_zz", "openai", "glm-5.3", "", false},
		{"openai/gpt-*", "chan_q", "openai", "gpt-6.1-terra", "", true},
		{"openai/gpt-*", "chan_q", "anthropic", "gpt-6.1-terra", "", false},
		{"anthropic/*", "chan_a", "anthropic", "claude-haiku-5.5", "", true},
		{"deepseek-v4.1-flash", "chan_a", "openai", "deepseek-v4.1-flash", "", true},
		{"df", "chan_a", "openai", "deepseek-v4.1-flash", "df", true},   // 别名
		{"*/*", "chan_a", "openai", "any-model", "", true},              // 全通配
		{"*/deepseek/deepseek-v4.1-flash", "c", "openai", "deepseek/deepseek-v4.1-flash", "", true}, // 含斜杠真实 id
	}
	for _, c := range cases {
		got := MatchMember(c.expr, c.channel, c.provider, c.model, c.alias)
		if got != c.want {
			t.Errorf("MatchMember(%q, ch=%s, prov=%s, model=%s, alias=%s) = %v, want %v",
				c.expr, c.channel, c.provider, c.model, c.alias, got, c.want)
		}
	}
}

// TestResolveExpandsSubGroups 子分组递归展开，且环上只展开一次。
func TestResolveExpandsSubGroups(t *testing.T) {
	c := NewCatalog()
	c.Add(Group{ID: "flash", Ref: "auto/flash", Members: []string{"*/flash-*"}})
	c.Add(Group{
		ID: "big", Ref: "auto/dfmodel", Name: "DeepSeek 家族",
		Members: []string{"*/deepseek-v4.1-flash"},
		Subs:    []SubRef{{ID: "flash", Ref: "auto/flash"}},
	})
	sel, err := c.Resolve("auto/dfmodel", Features{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Members) != 2 {
		t.Fatalf("应展开 2 个成员，实际 %v", sel.Members)
	}
	if sel.Why != "default" {
		t.Fatalf("无规则命中时 why 应为 default，实际 %s", sel.Why)
	}

	// 自环：只展开一次，不无限递归
	c2 := NewCatalog()
	c2.Add(Group{ID: "self", Ref: "auto/self", Members: []string{"*/x"}, Subs: []SubRef{{ID: "self"}}})
	sel2, err := c2.Resolve("auto/self", Features{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sel2.Members) != 1 {
		t.Fatalf("自环应只展开一次，实际 %v", sel2.Members)
	}
}

// TestRulePinsFirst 条件规则把指定成员提到最前，其余候选保留（保住失败切换）。
func TestRulePinsFirst(t *testing.T) {
	c := NewCatalog()
	c.Add(Group{
		ID: "cheap-first", Ref: "auto/cheap", Name: "便宜优先",
		Members: []string{"a/m1", "b/m2", "c/m3"},
		Rules: []Rule{
			{Use: "c/m3", Effort: "high"},
			{Use: "b/m2", Images: boolp(true)},
		},
	})

	// 无特征命中 → 原序
	sel, _ := c.Resolve("auto/cheap", Features{})
	if sel.Why != "default" || sel.Members[0] != "a/m1" {
		t.Fatalf("无命中应保持原序: %+v", sel)
	}

	// effort=high → c/m3 提到最前
	sel, _ = c.Resolve("auto/cheap", Features{Effort: "high"})
	if sel.Why != "rule" || sel.Members[0] != "c/m3" {
		t.Fatalf("effort=high 应把 c/m3 提前: %+v", sel)
	}
	if len(sel.Members) != 3 {
		t.Fatalf("其余候选必须保留以支持失败切换，实际 %v", sel.Members)
	}
	if len(sel.Rules) != 1 || sel.Rules[0] != "use=c/m3 effort=high" {
		t.Fatalf("规则描述不符: %v", sel.Rules)
	}

	// images=true → b/m2 提到最前
	sel, _ = c.Resolve("auto/cheap", Features{HasImages: true})
	if sel.Members[0] != "b/m2" {
		t.Fatalf("含图片应把 b/m2 提前: %v", sel.Members)
	}

	// effort=low + images=true → 只有 images 规则命中
	sel, _ = c.Resolve("auto/cheap", Features{Effort: "low", HasImages: true})
	if sel.Members[0] != "b/m2" {
		t.Fatalf("应命中 images 规则: %v", sel.Members)
	}
}

// TestRuleMinMaxInputTokens token 区间规则。
func TestRuleMinMaxInputTokens(t *testing.T) {
	c := NewCatalog()
	c.Add(Group{
		ID: "long", Ref: "auto/long", Members: []string{"small/s", "big/b"},
		Rules: []Rule{{Use: "big/b", MinInputTokens: 100000}},
	})
	sel, _ := c.Resolve("auto/long", Features{InputTokens: 99999})
	if sel.Why != "default" {
		t.Fatalf("未达阈值不应命中，实际 %s", sel.Why)
	}
	sel, _ = c.Resolve("auto/long", Features{InputTokens: 200000})
	if sel.Members[0] != "big/b" {
		t.Fatalf("超阈值应命中长上下文成员: %v", sel.Members)
	}
}

// TestEmptyGroupReportsNoMembers 空分组如实报告 no_members，不虚构成员。
func TestEmptyGroupReportsNoMembers(t *testing.T) {
	c := NewCatalog()
	c.Add(Group{ID: "empty", Ref: "auto/empty"})
	sel, err := c.Resolve("auto/empty", Features{})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Why != "no_members" || len(sel.Members) != 0 {
		t.Fatalf("空分组应如实报告: %+v", sel)
	}
}

// TestUnknownGroupErrors 未知分组报错，不静默返回空集（避免把配置错误伪装成无可用上游）。
func TestUnknownGroupErrors(t *testing.T) {
	c := NewCatalog()
	if _, err := c.Resolve("nope", Features{}); err == nil {
		t.Fatal("未知分组应报错")
	}
}

// TestAutoDeriveFromRealRows 按真实模型 id 自动成组（零配置可用）。
func TestAutoDeriveFromRealRows(t *testing.T) {
	c := NewCatalog()
	n := c.AutoDerive([]Row{
		{ChannelID: "chan_a", ProviderType: "openai", UpstreamModelID: "deepseek-v4.1-flash"},
		{ChannelID: "chan_b", ProviderType: "openai_compatible", UpstreamModelID: "deepseek-v4.1-flash"},
		{ChannelID: "chan_c", ProviderType: "anthropic", UpstreamModelID: "claude-haiku-5.5"},
		{ChannelID: "", UpstreamModelID: "should-be-skipped"},
		{ChannelID: "chan_d", UpstreamModelID: ""},
	})
	if n != 2 {
		t.Fatalf("应派生 2 个分组，实际 %d", n)
	}
	sel, err := c.Resolve("auto/model/deepseek-v4.1-flash", Features{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Members) != 2 {
		t.Fatalf("同一真实模型应聚齐 2 个渠道，实际 %v", sel.Members)
	}
	// 重复调用不应重复建组
	if again := c.AutoDerive([]Row{{ChannelID: "chan_a", UpstreamModelID: "deepseek-v4.1-flash"}}); again != 0 {
		t.Fatalf("已存在分组不应重复派生，实际新增 %d", again)
	}
}

// TestSaveLoadRoundTrip 分组目录原子落盘 0600 / 目录 0700 / 重启可读。
func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := NewCatalog()
	c.Add(Group{
		ID: "g1", Ref: "auto/dfmodel", Name: "DeepSeek V4.1 flash",
		Members: []string{"*/deepseek-*"},
		Rules:   []Rule{{Use: "a/m", Effort: "max", Note: "最高档位"}},
	})
	if _, err := c.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if mode := fileMode(t, filepath.Join(dir, "groups.json")); mode != 0o600 {
		t.Fatalf("groups.json 权限应为 0600，实际 %o", mode)
	}
	if mode := fileMode(t, dir); mode != 0o700 {
		t.Fatalf("目录权限应为 0700，实际 %o", mode)
	}

	c2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	g, ok := c2.Get("auto/dfmodel")
	if !ok {
		t.Fatal("重启后应能按 Ref 寻址")
	}
	if len(g.Rules) != 1 || g.Rules[0].Effort != "max" || g.Rules[0].Note != "最高档位" {
		t.Fatalf("规则未正确往返: %+v", g.Rules)
	}
	// 按 ID 也能寻址
	if _, ok := c2.Get("g1"); !ok {
		t.Fatal("重启后应能按 ID 寻址")
	}
}

// TestLoadMissingFileIsEmptyCatalog 首次运行无 groups.json 属正常，返回空目录。
func TestLoadMissingFileIsEmptyCatalog(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("缺文件不应报错: %v", err)
	}
	if len(c.Groups) != 0 {
		t.Fatal("应为空目录")
	}
}

// TestLoadRejectsWrongFormat 格式标识不符时拒绝载入。
func TestLoadRejectsWrongFormat(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "groups.json"), []byte(`{"format":"other","groups":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("格式不符应报错")
	}
}

// TestListIsStableAndSorted 列表按 Ref 稳定排序（面板展示不抖动）。
func TestListIsStableAndSorted(t *testing.T) {
	c := NewCatalog()
	c.Add(Group{ID: "z", Ref: "z/zz"})
	c.Add(Group{ID: "a", Ref: "a/aa"})
	c.Add(Group{ID: "m", Ref: "m/mm"})
	l := c.List()
	if l[0].Ref != "a/aa" || l[1].Ref != "m/mm" || l[2].Ref != "z/zz" {
		t.Fatalf("应按 Ref 排序: %+v", l)
	}
}

func fileMode(t *testing.T, p string) os.FileMode {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	return st.Mode().Perm()
}

// TestResolveOrAutoMakesZeroConfigGroupsTransparent 零配置分组的透明生效：
// 调用方用裸模型名，系统自动命中 auto/model/<id> 分组。
func TestResolveOrAutoMakesZeroConfigGroupsTransparent(t *testing.T) {
	c := NewCatalog()
	c.AutoDerive([]Row{
		{ChannelID: "ch1", UpstreamModelID: "liquid/lfm-3-1.7b"},
		{ChannelID: "ch2", UpstreamModelID: "liquid/lfm-3-1.7b"},
	})

	sel, found, err := c.ResolveOrAuto("liquid/lfm-3-1.7b", Features{})
	if err != nil || !found {
		t.Fatalf("应命中自动分组: found=%v err=%v", found, err)
	}
	if len(sel.Members) != 2 {
		t.Fatalf("应聚齐该真实模型的两个渠道，实际 %v", sel.Members)
	}
	if sel.GroupRef != "auto/model/liquid/lfm-3-1.7b" {
		t.Fatalf("分组 ref 不符: %s", sel.GroupRef)
	}

	// 显式分组优先于自动分组
	c.Add(Group{ID: "manual", Ref: "liquid/lfm-3-1.7b", Members: []string{"ch9/x"}})
	sel2, found2, _ := c.ResolveOrAuto("liquid/lfm-3-1.7b", Features{})
	if !found2 || sel2.GroupRef != "liquid/lfm-3-1.7b" || len(sel2.Members) != 1 {
		t.Fatalf("显式分组应优先: %+v", sel2)
	}

	// 完全未知的模型：found=false，交由裸模型路由兜底
	if _, found3, _ := c.ResolveOrAuto("nope/never", Features{}); found3 {
		t.Fatal("未知模型不应命中分组")
	}
}
