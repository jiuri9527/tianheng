package redact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestThreeTiersCountedSeparately 三级来源分别计数（known / model / rule）。
func TestThreeTiersCountedSeparately(t *testing.T) {
	m := NewMasker(DefaultPolicy())
	m.RegisterKnown("sk-knownvalue0123456789", "jiuri@example.com")

	text := strings.Join([]string{
		"我的密钥是 sk-knownvalue0123456789",       // known
		"邮箱 jiuri@example.com",                  // known（同时也会被 rule 命中，但 known 优先）
		"顺手一个 token sk-live-abcdef1234567890", // rule（API_KEY 前缀）
		"内网地址 10.20.30.40",                     // rule（IP）
		"家目录 /Users/jiuri/project",              // rule（HOME）
		"我叫张伟，手机 13800138000",                 // model（PERSON + PHONE）
	}, "\n")

	out, rep := m.Mask(text)
	if rep.N == 0 {
		t.Fatal("应发生替换")
	}
	if rep.By[TierKnown] != 2 {
		t.Errorf("known 级应命中 2 处，实际 %d（%v）", rep.By[TierKnown], rep.By)
	}
	if rep.By[TierModel] < 2 {
		t.Errorf("model 级应至少命中 PERSON+PHONE，实际 %d", rep.By[TierModel])
	}
	if rep.By[TierRule] < 3 {
		t.Errorf("rule 级应至少命中 3 处，实际 %d", rep.By[TierRule])
	}
	if rep.Kinds[KindPerson] == 0 || rep.Kinds[KindPhone] == 0 {
		t.Errorf("个人标识应按种类计数: %v", rep.Kinds)
	}
	for _, secret := range []string{"sk-knownvalue0123456789", "jiuri@example.com", "sk-live-abcdef1234567890",
		"10.20.30.40", "/Users/jiuri/project", "13800138000"} {
		if strings.Contains(out, secret) {
			t.Errorf("原文 %q 仍出现在脱敏结果中:\n%s", secret, out)
		}
	}
}

// TestEmptyReportWhenDisabled 策略关闭时如实返回零统计（不假装做了事）。
func TestEmptyReportWhenDisabled(t *testing.T) {
	pol := DefaultPolicy()
	pol.Enabled = false
	m := NewMasker(pol)
	out, rep := m.Mask("密钥 sk-live-abcdef1234567890")
	if out != "密钥 sk-live-abcdef1234567890" {
		t.Fatal("关闭时不应改动文本")
	}
	if !rep.Empty() || len(rep.By) != 0 {
		t.Fatalf("关闭时应返回零统计，实际 %+v", rep)
	}
}

// TestRestoreRoundTrip 出站遮蔽 + 入站还原必须无损（否则用户看到的是乱码）。
func TestRestoreRoundTrip(t *testing.T) {
	m := NewMasker(DefaultPolicy())
	m.SetKey("req-abc")
	orig := "请用 sk-knownvalue0123456789 调用，服务器 10.20.30.40，家目录 /Users/jiuri/app"

	masked, rep := m.Mask(orig)
	if masked == orig {
		t.Fatal("应发生替换")
	}
	if strings.Contains(masked, "sk-knownvalue0123456789") || strings.Contains(masked, "10.20.30.40") {
		t.Fatalf("敏感串未遮蔽: %s", masked)
	}
	// 模拟模型回答里引用了占位符
	ph := ""
	for _, tok := range strings.Fields(masked) {
		if strings.Contains(tok, "API_KEY#") || strings.Contains(tok, "SECRET#") || strings.Contains(tok, "IP#") {
			ph = strings.Trim(tok, "，。,.")
			break
		}
	}
	if ph == "" {
		t.Fatalf("未找到占位符: %s", masked)
	}
	answer := "好的，我会使用 " + ph + " 来完成调用。"
	restored := m.Restore(answer)
	if strings.Contains(restored, ph) {
		t.Fatalf("占位符未还原: %s", restored)
	}
	if restored == answer {
		t.Fatal("还原应改变文本")
	}
	if rep.N == 0 || rep.Key != "req-abc" {
		t.Fatalf("统计与关联键不符: %+v", rep)
	}
}

// TestPlaceholderStableForSameValue 同一原值在整次会话内占位符稳定（模型可引用）。
func TestPlaceholderStableForSameValue(t *testing.T) {
	m := NewMasker(DefaultPolicy())
	v := "sk-stablevalue0123456789"
	a, _ := m.Mask("第一处 " + v)
	b, _ := m.Mask("第二处 " + v)
	if a == b {
		t.Fatal("两次输入不同，输出不应相同")
	}
	pa := extract(a)
	pb := extract(b)
	if pa == "" || pa != pb {
		t.Fatalf("同一值的占位符应稳定: %q vs %q", pa, pb)
	}
}

// TestWholeWordBoundary 整词匹配：嵌在长单词里的片段不被替换（避免误伤）。
func TestWholeWordBoundary(t *testing.T) {
	m := NewMasker(DefaultPolicy())
	m.RegisterKnown("abcdefghijklmnop") // 16 字节，够长
	// 独立出现 → 替换
	out, rep := m.Mask("值 abcdefghijklmnop 结束")
	if rep.N != 1 || strings.Contains(out, "abcdefghijklmnop") {
		t.Fatalf("独立出现应替换: %s %+v", out, rep)
	}
	// 嵌在更长单词里 → 不替换
	out2, rep2 := m.Mask("前缀XabcdefghijklmnopY后缀")
	if rep2.N != 0 || out2 != "前缀XabcdefghijklmnopY后缀" {
		t.Fatalf("嵌入长词不应替换: %s %+v", out2, rep2)
	}
}

// TestShortKnownIgnored 过短的已知值被忽略（否则整词匹配会误伤大量文本）。
func TestShortKnownIgnored(t *testing.T) {
	m := NewMasker(DefaultPolicy())
	m.RegisterKnown("ab", "a", "  ", "abcdefgh")
	out, rep := m.Mask("ab a abcdefgh")
	if strings.Contains(out, "abcdefgh") {
		t.Fatal("合法的已知值应被替换")
	}
	if rep.N != 1 {
		t.Fatalf("只应替换 1 处，实际 %d", rep.N)
	}
}

// TestNoOverlapDoubleMask 重叠区间只替换一次，输出中不出现嵌套占位符。
func TestNoOverlapDoubleMask(t *testing.T) {
	m := NewMasker(DefaultPolicy())
	m.RegisterKnown("/Users/jiuri/app") // 同时会被 HOME 规则命中
	out, rep := m.Mask("路径 /Users/jiuri/app 结束")
	if strings.Count(out, "«") != 1 {
		t.Fatalf("重叠区间应只替换一次: %s", out)
	}
	if rep.N != 1 {
		t.Fatalf("应计 1 处，实际 %d", rep.N)
	}
	// known 优先，因此种类应为 known 判定的 HOME
	if rep.By[TierKnown] != 1 {
		t.Fatalf("known 应优先于 rule: %v", rep.By)
	}
}

// TestKindToggle 逐类开关生效。
func TestKindToggle(t *testing.T) {
	pol := DefaultPolicy()
	pol.Kinds = map[string]bool{KindIP: false}
	m := NewMasker(pol)
	out, rep := m.Mask("地址 10.20.30.40 与密钥 sk-live-abcdef1234567890")
	if strings.Contains(out, "10.20.30.40") == false {
		t.Fatal("关闭 IP 类后不应遮蔽 IP")
	}
	if rep.Kinds[KindIP] != 0 {
		t.Fatalf("IP 类计数应为 0: %v", rep.Kinds)
	}
	if rep.Kinds[KindAPIKey] == 0 {
		t.Fatalf("API_KEY 仍应遮蔽: %v", rep.Kinds)
	}
}

// TestRecognizerPluginsable 可注入自定义 tier=model 识别器。
type fakeRec struct{}

func (fakeRec) Name() string { return "fake-recognizer" }
func (fakeRec) Find(text string) []Span {
	i := strings.Index(text, "TOPSECRET")
	if i < 0 {
		return nil
	}
	return []Span{{Start: i, End: i + len("TOPSECRET"), Kind: KindSecret}}
}

func TestRecognizerPluggable(t *testing.T) {
	m := NewMasker(DefaultPolicy())
	m.SetRecognizer(fakeRec{})
	out, rep := m.Mask("这里 TOPSECRET 出现")
	if strings.Contains(out, "TOPSECRET") {
		t.Fatalf("自定义识别器未生效: %s", out)
	}
	if rep.By[TierModel] != 1 || rep.Kinds[KindSecret] != 1 {
		t.Fatalf("统计不符: %+v", rep)
	}
}

// TestAuditJournalNeverWritesPlaintext 审计日志只写计数与字节数，绝不写原文。
func TestAuditJournalNeverWritesPlaintext(t *testing.T) {
	dir := t.TempDir()
	j := NewJournal(dir)
	rec := AuditRecord{
		Time: "2026-11-08T10:00:00+08:00", Key: "req-1",
		N: 3, Distinct: 2,
		Kinds: map[string]int{KindAPIKey: 1, KindSecret: 2},
		By:    map[string]int{TierKnown: 1, TierRule: 2},
		Bytes: 96,
	}
	if err := j.Append(rec); err != nil {
		t.Fatalf("Append: %v", err)
	}
	p := filepath.Join(dir, "masking", "2026-11-08.jsonl")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("审计文件应存在: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"n":3`) || !strings.Contains(s, `"API_KEY":1`) {
		t.Fatalf("审计内容不符: %s", s)
	}
	for _, bad := range []string{"sk-", "10.20.30.40", "jiuri", "password"} {
		if strings.Contains(s, bad) {
			t.Fatalf("审计日志不得含原文片段 %q: %s", bad, s)
		}
	}
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("审计文件权限应为 0600，实际 %v err=%v", st.Mode().Perm(), err)
	}
}

// TestStats 映射规模统计真实。
func TestStats(t *testing.T) {
	m := NewMasker(DefaultPolicy())
	m.Mask("a sk-aaaa111122223333 b sk-bbbb111122223333 c sk-aaaa111122223333")
	distinct, byKind := m.Stats()
	if distinct != 2 {
		t.Fatalf("不同原值应为 2，实际 %d", distinct)
	}
	if byKind[KindAPIKey] != 2 {
		t.Fatalf("API_KEY 类应有 2 个占位符: %v", byKind)
	}
}

// TestHashIsStableAndNotReversible 指纹稳定且不含原文。
func TestHashIsStableAndNotReversible(t *testing.T) {
	h1 := Hash("sk-secretvalue")
	h2 := Hash("sk-secretvalue")
	if h1 != h2 || len(h1) != 16 {
		t.Fatalf("指纹应稳定且为 16 位十六进制: %q %q", h1, h2)
	}
	if strings.Contains(h1, "secret") {
		t.Fatal("指纹不得含原文")
	}
}

func extract(s string) string {
	i := strings.Index(s, "«")
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], "»")
	if j < 0 {
		return ""
	}
	return s[i : i+j+len("»")]
}

// TestCJKBoundaryDoesNotDisableMasking 回归：中文环境下敏感串必须仍被遮蔽。
//
// 修复前：IP 后的「，」是 3 字节 UTF-8，被判成单词字符 → 边界不成立 →
// 中文技术文档里几乎所有敏感串都漏脱敏。此测试锁定该修复。
func TestCJKBoundaryDoesNotDisableMasking(t *testing.T) {
	m := NewMasker(DefaultPolicy())
	cases := []struct {
		name string
		in   string
		bad  string
	}{
		{"IP 后跟中文逗号", "服务器 10.20.30.40，请访问", "10.20.30.40"},
		{"IP 后跟汉字", "地址是 10.20.30.40然后重启", "10.20.30.40"},
		{"密钥后跟汉字", "用 sk-live-abcdef1234567890去调用", "sk-live-abcdef1234567890"},
		{"密钥后跟全角括号", "密钥（sk-live-abcdef1234567890）已配置", "sk-live-abcdef1234567890"},
		{"路径后跟汉字", "在 /Users/jiuri/app里运行", "/Users/jiuri/app"},
		{"邮箱后跟句号", "发给 jiuri@example.com。谢谢", "jiuri@example.com"},
	}
	for _, c := range cases {
		out, rep := m.Mask(c.in)
		if strings.Contains(out, c.bad) {
			t.Errorf("%s：未遮蔽 %q → %q", c.name, c.bad, out)
		}
		if rep.N == 0 {
			t.Errorf("%s：统计为 0", c.name)
		}
	}
}

// TestASCIIWordBoundaryStillHolds 边界仍须防止把长标识符的一部分当成秘密。
// 语义：已知值 "sk-abc123def456" 嵌在更长串 "Xsk-abc123def456Y" 里时不得替换。
func TestASCIIWordBoundaryStillHolds(t *testing.T) {
	m := NewMasker(DefaultPolicy())
	m.RegisterKnown("sk-abc123def456")

	out, rep := m.Mask("独立出现 sk-abc123def456 结束")
	if rep.N != 1 || strings.Contains(out, "sk-abc123def456") {
		t.Fatalf("独立出现应替换: %q %+v", out, rep)
	}

	embedded := "Xsk-abc123def456Y"
	out2, rep2 := m.Mask("嵌入 " + embedded)
	if rep2.N != 0 || out2 != "嵌入 "+embedded {
		t.Fatalf("嵌在更长 ASCII 串里不应替换: %q %+v", out2, rep2)
	}
}

// TestPhoneAndAnchoredPersonOnly 只识别手机号与「有锚点的人名」，不猜全篇人名。
func TestPhoneAndAnchoredPersonOnly(t *testing.T) {
	m := NewMasker(DefaultPolicy())

	// 有锚点 → 遮蔽
	out, _ := m.Mask("我叫张伟，请联系我")
	if strings.Contains(out, "张伟") {
		t.Errorf("锚点人名应被遮蔽: %s", out)
	}
	// 无锚点 → 不遮蔽（避免把「路径」「后缀」「家目录」误判成人名）
	for _, plain := range []string{"路径解析失败", "添加后缀", "家目录配置", "调用接口"} {
		out, rep := m.Mask(plain)
		if out != plain {
			t.Errorf("无锚点文本不应被误改: %q → %q (rep=%+v)", plain, out, rep)
		}
	}
	// 英文称谓锚点
	out, _ = m.Mask("Please ask Mr. Smith about it")
	if strings.Contains(out, "Smith") {
		t.Errorf("英文称谓后的人名应被遮蔽: %s", out)
	}
}

// TestEmailClassifiedAsEmailNotUserHost 回归：邮箱必须归类为 EMAIL。
// USERHOST 规则更宽松，若排在前面会把邮箱吞成 user@domain（实测缺陷）。
func TestEmailClassifiedAsEmailNotUserHost(t *testing.T) {
	m := NewMasker(DefaultPolicy())
	out, rep := m.Mask("联系 jiuri@example.com 处理")
	if strings.Contains(out, "jiuri@example.com") {
		t.Fatalf("邮箱未遮蔽: %s", out)
	}
	if rep.Kinds[KindEmail] != 1 {
		t.Fatalf("应归类为 EMAIL，实际 kinds=%v", rep.Kinds)
	}
	if rep.Kinds[KindUserHost] != 0 {
		t.Fatalf("不应归类为 USERHOST，实际 kinds=%v", rep.Kinds)
	}
}
