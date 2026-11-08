package ctxbreakdown

import (
	"testing"
)

func partsByKind(b Breakdown) map[string]Part {
	m := map[string]Part{}
	for _, p := range b.Parts {
		m[p.Kind] = p
	}
	return m
}

// TestOpenAIShapeDecomposed OpenAI 形状：system 在 messages 里、含 tools。
func TestOpenAIShapeDecomposed(t *testing.T) {
	body := []byte(`{
	  "model": "openai/gpt-6.1-terra",
	  "temperature": 0.7,
	  "tools": [
	    {"type":"function","function":{"name":"get_weather","description":"查天气","parameters":{"type":"object"}}},
	    {"type":"function","function":{"name":"run_sql","description":"执行查询","parameters":{"type":"object"}}}
	  ],
	  "messages": [
	    {"role":"system","content":"你是天衡的助手，请用中文回答。"},
	    {"role":"user","content":"北京天气如何？"},
	    {"role":"assistant","content":"我帮你查一下。"}
	  ]
	}`)
	b := Analyze(body, "openai/gpt-6.1-terra", 500000, nil)

	if b.Counted {
		t.Fatal("未注入 tokenizer 时必须如实标记 counted=false")
	}
	if b.Tokens <= 0 {
		t.Fatal("应估算出非零 token")
	}
	if b.Window != 500000 || b.Overflow {
		t.Fatalf("窗口判定不符: window=%d overflow=%v", b.Window, b.Overflow)
	}
	if b.Headroom != 500000-b.Tokens {
		t.Fatalf("headroom 应为 window-tokens: %d", b.Headroom)
	}
	pk := partsByKind(b)
	if _, ok := pk["tools"]; !ok {
		t.Fatalf("应识别出 tools 部分: %+v", b.Parts)
	}
	if _, ok := pk["messages"]; !ok {
		t.Fatalf("应识别出 messages 部分: %+v", b.Parts)
	}
	if len(pk["tools"].Items) != 2 {
		t.Fatalf("工具明细应有 2 项: %+v", pk["tools"].Items)
	}
	if pk["messages"].Items[0].Name != "system" {
		t.Fatalf("messages 明细应按角色列出: %+v", pk["messages"].Items)
	}
	if b.Turns < 3 {
		t.Fatalf("轮数统计不符: %d", b.Turns)
	}
	// 分解求和必须与总数自洽（面板会交叉核对）
	sum := 0
	for _, p := range b.Parts {
		sum += p.Tokens
	}
	if sum != b.Tokens {
		t.Fatalf("各部分之和(%d)应等于总数(%d)", sum, b.Tokens)
	}
}

// TestAnthropicShapeDecomposed Anthropic 形状：system 是顶层分块数组。
func TestAnthropicShapeDecomposed(t *testing.T) {
	body := []byte(`{
	  "model": "anthropic/claude-haiku-5.5",
	  "max_tokens": 1024,
	  "system": [{"type":"text","text":"你是天衡。"},{"type":"text","text":"请简洁回答。"}],
	  "messages": [{"role":"user","content":[{"type":"text","text":"你好"}]}]
	}`)
	b := Analyze(body, "anthropic/claude-haiku-5.5", 200000, nil)
	pk := partsByKind(b)
	sys, ok := pk["system"]
	if !ok {
		t.Fatalf("应识别顶层 system: %+v", b.Parts)
	}
	if len(sys.Items) != 2 {
		t.Fatalf("system 分块应逐块列出: %+v", sys.Items)
	}
	if pk["messages"].Items[0].Name != "user" {
		t.Fatalf("分块 content 应拍平统计: %+v", pk["messages"].Items)
	}
}

// TestOverflowDetected 超窗口必须被真实标记。
func TestOverflowDetected(t *testing.T) {
	long := make([]byte, 100000)
	for i := range long {
		long[i] = 'a'
	}
	body := []byte(`{"messages":[{"role":"user","content":"` + string(long) + `"}]}`)
	b := Analyze(body, "m", 4000, nil)
	if !b.Overflow {
		t.Fatalf("应判定超窗口: tokens=%d window=%d", b.Tokens, b.Window)
	}
	if b.Headroom >= 0 {
		t.Fatalf("超限时 headroom 应为负: %d", b.Headroom)
	}
}

// TestUnknownWindowSkipsOverflow window=0（未知）时不判定超限。
func TestUnknownWindowSkipsOverflow(t *testing.T) {
	b := Analyze([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), "m", 0, nil)
	if b.Overflow || b.Headroom != 0 {
		t.Fatalf("未知窗口不应判定: overflow=%v headroom=%d", b.Overflow, b.Headroom)
	}
}

// TestRealTokenizerMakesCountedTrue 注入真实 tokenizer 后 counted=true 且口径可追溯。
type fakeCounter struct{}

func (fakeCounter) Name() string      { return "fake-bpe" }
func (fakeCounter) Count(s string) int { return len(s) } // 1 字节 1 token，便于断言

func TestRealTokenizerMakesCountedTrue(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"abcd"}]}`)
	b := Analyze(body, "m", 100, fakeCounter{})
	if !b.Counted {
		t.Fatal("注入 tokenizer 后 counted 应为 true")
	}
	if b.Method != "tokenizer: fake-bpe" {
		t.Fatalf("估算口径应可追溯: %s", b.Method)
	}
	pk := partsByKind(b)
	if pk["messages"].Tokens != 4 {
		t.Fatalf("应为 4（fake tokenizer 1 字节 1 token），实际 %d", pk["messages"].Tokens)
	}
}

// TestEstimateTokensCJKAware CJK 的估算口径与 ASCII 明显不同（不得按字节一刀切）。
func TestEstimateTokensCJKAware(t *testing.T) {
	ascii := EstimateTokens("aaaaaaaa")
	cjk := EstimateTokens("你好世界")
	if ascii != 2 {
		t.Fatalf("8 个 ASCII 字节应约 2 token，实际 %d", ascii)
	}
	if cjk < 4 || cjk > 7 {
		t.Fatalf("4 个汉字应约 5 token（12 字节 / 2.2），实际 %d", cjk)
	}
	if EstimateTokens("") != 0 {
		t.Fatal("空串应为 0")
	}
}

// TestMalformedBodyStillReported 非法 JSON 也要如实报告，不 panic、不虚构分解。
func TestMalformedBodyStillReported(t *testing.T) {
	b := Analyze([]byte("not json at all"), "m", 1000, nil)
	if b.Tokens <= 0 {
		t.Fatal("仍应给出真实文本的估算")
	}
	if len(b.Parts) != 1 || b.Parts[0].Kind != "other" {
		t.Fatalf("非法请求体应归入 other: %+v", b.Parts)
	}
	if b.Bytes != len("not json at all") {
		t.Fatalf("字节数应真实: %d", b.Bytes)
	}
}

// TestImageBlockCounted 非文本分块按真实字节折算，不做零计（避免低估）。
func TestImageBlockCounted(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[
	  {"type":"text","text":"这张图是什么"},
	  {"type":"image","data":"` + repeat("QUJDRA", 500) + `"}
	]}]}`)
	b := Analyze(body, "m", 100000, nil)
	pk := partsByKind(b)
	if pk["messages"].Tokens < 100 {
		t.Fatalf("图片分块应计入上下文，实际 %d", pk["messages"].Tokens)
	}
}

// TestPartsOrderStable 部分顺序稳定（system → tools → messages → other），面板不抖动。
func TestPartsOrderStable(t *testing.T) {
	body := []byte(`{"temperature":0.1,"system":"s","tools":[{"type":"function","function":{"name":"f"}}],
	  "messages":[{"role":"user","content":"u"}]}`)
	b := Analyze(body, "m", 1000, nil)
	want := []string{"system", "tools", "messages", "other"}
	got := []string{}
	for _, p := range b.Parts {
		got = append(got, p.Kind)
	}
	i := 0
	for _, w := range want {
		for _, g := range got {
			if g == w {
				got[i] = w
				i++
				break
			}
		}
	}
	if i != len(want) {
		t.Fatalf("部分顺序不符: %v", got)
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
