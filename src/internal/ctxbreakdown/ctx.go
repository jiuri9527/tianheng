// Package ctxbreakdown 原生实现「上下文构成分解」原理。
//
// ── 原理来源 ────────────────────────────────────────────────────────────────
// 取自 AI 供应商聚合层 Magpie 的路由决策记录里的 prompt 字段：
//
//	prompt: { window: 1000000, tokens: 516345, counted: true, turns: 82,
//	          parts: [ {kind:"system", tokens:26354, items:[{name:"prompt",tokens:26354}]},
//	                   {kind:"tools",  tokens:...,    items:[...]},
//	                   {kind:"messages", ...} ] }
//
// 提炼出的原理：
//  1. **把请求拆成有语义的部分**（system / tools / messages / other），
//     而不是只报一个总数 —— 才能回答「这个会话为什么涨到 51 万 token」。
//  2. **counted 标记诚实性**：有真实 tokenizer 时 counted=true；只能估算时
//     counted=false。绝不把估算值伪装成精确值。
//  3. **window 对比**：与模型真实上下文窗口比对，提前预警超限。
//  4. **turns 轮数**：多轮会话的增长趋势（配合亲和性看缓存收益）。
//
// ── 天衡的落法 ──────────────────────────────────────────────────────────────
// 输入是网关规范化后的请求体（canonical），同时兼容 OpenAI 与 Anthropic 两种形状。
// 天衡离线可用、零第三方依赖，因此不含 BPE 词表；本包**明确报告 counted=false**，
// 并给出可复现的估算口径（见 EstimateTokens 注释）。若上层接入了真实 tokenizer，
// 通过 Counter 接口注入即可让 counted=true。
package ctxbreakdown

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"
)

// Part 是上下文的一个语义部分。
type Part struct {
	Kind   string `json:"kind"`   // system | tools | messages | other
	Tokens int    `json:"tokens"` // 该部分 token 数
	Bytes  int    `json:"bytes"`  // 该部分原始字节数（真实值，不做估算）
	Items  []Item `json:"items"`  // 进一步细分
}

// Item 是部分内部的明细项。
type Item struct {
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
}

// Breakdown 是一次请求的上下文构成。
type Breakdown struct {
	Window   int    `json:"window"`    // 模型真实上下文窗口（来自真实数据集）
	Tokens   int    `json:"tokens"`    // 估算总 token
	Counted  bool   `json:"counted"`   // 是否有真实 tokenizer（天衡默认 false）
	Method   string `json:"method"`    // 估算口径说明
	Turns    int    `json:"turns"`     // 消息条数（含 system）
	Parts    []Part `json:"parts"`     // 语义分解
	Bytes    int    `json:"bytes"`     // 请求体总字节
	Overflow bool   `json:"overflow"`  // 是否超过 window
	Headroom int    `json:"headroom"`  // window - tokens（可为负）
	Model    string `json:"model"`     // 逻辑模型名（若有）
}

// Counter 是可注入的真实 tokenizer。
type Counter interface {
	Count(text string) int
	Name() string
}

// Analyze 分析规范化请求体，产出上下文构成。
//
// window 传模型的真实上下文长度（0 表示未知，此时不判定 overflow）。
// c 传 nil 时使用内置估算口径。
func Analyze(body []byte, model string, window int, c Counter) Breakdown {
	b := Breakdown{
		Window:  window, Model: model, Parts: []Part{}, Bytes: len(body),
		Method:  "estimate: ascii≈bytes/4, cjk≈bytes/2.2 (no tokenizer bundled)",
		Counted: false,
	}
	if c != nil {
		b.Method = "tokenizer: " + c.Name()
		b.Counted = true
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		b.Parts = append(b.Parts, Part{Kind: "other", Bytes: len(body)})
		b.Tokens = count(string(body), c, &b)
		return finish(b)
	}

	// ---- system ----
	sysText, sysItems := extractSystem(raw)
	if sysText != "" {
		p := Part{Kind: "system", Bytes: len(sysText)}
		for _, it := range sysItems {
			p.Items = append(p.Items, Item{Name: it.name, Tokens: count(it.text, c, &b)})
		}
		if len(p.Items) == 0 {
			p.Items = append(p.Items, Item{Name: "prompt", Tokens: count(sysText, c, &b)})
		}
		for _, it := range p.Items {
			p.Tokens += it.Tokens
		}
		b.Parts = append(b.Parts, p)
		b.Turns++
	}

	// ---- tools ----
	if tv, ok := raw["tools"]; ok && len(tv) > 0 && string(tv) != "null" {
		tools, names := extractTools(tv)
		if tools > 0 {
			p := Part{Kind: "tools", Bytes: len(tv)}
			for _, n := range names {
				p.Items = append(p.Items, Item{Name: n, Tokens: count(n, c, &b)})
			}
			p.Tokens = tools
			if len(p.Items) == 0 {
				p.Items = append(p.Items, Item{Name: "tools", Tokens: tools})
			}
			b.Parts = append(b.Parts, p)
		}
	}

	// ---- messages ----
	msgText, kept := extractMessages(raw)
	if len(kept) > 0 {
		p := Part{Kind: "messages"}
		for _, m := range kept {
			t := count(m.text, c, &b)
			p.Items = append(p.Items, Item{Name: m.role, Tokens: t})
			p.Tokens += t
			p.Bytes += len(m.text)
			b.Turns++
		}
		b.Parts = append(b.Parts, p)
	} else if msgText != "" {
		t := count(msgText, c, &b)
		b.Parts = append(b.Parts, Part{Kind: "messages", Tokens: t, Bytes: len(msgText),
			Items: []Item{{Name: "messages", Tokens: t}}})
		b.Turns++
	}

	// ---- other：请求体里未被上面覆盖的部分（temperature / stop / 自定义字段…）----
	covered := 0
	for _, p := range b.Parts {
		covered += p.Bytes
	}
	if rest := len(body) - covered; rest > 64 {
		p := Part{Kind: "other", Bytes: rest}
		p.Items = append(p.Items, Item{Name: "params", Tokens: count(strings.Repeat("x", rest), c, &b)})
		p.Tokens = p.Items[0].Tokens
		b.Parts = append(b.Parts, p)
	}

	// 总和用各部分求和，保证「分解」与「总数」自洽（可被面板交叉核对）。
	b.Tokens = 0
	for _, p := range b.Parts {
		b.Tokens += p.Tokens
	}
	return finish(b)
}

func finish(b Breakdown) Breakdown {
	if b.Window > 0 {
		b.Headroom = b.Window - b.Tokens
		b.Overflow = b.Tokens > b.Window
	}
	sort.SliceStable(b.Parts, func(i, j int) bool { return partRank(b.Parts[i].Kind) < partRank(b.Parts[j].Kind) })
	return b
}

func partRank(k string) int {
	switch k {
	case "system":
		return 0
	case "tools":
		return 1
	case "messages":
		return 2
	default:
		return 3
	}
}

// EstimateTokens 是天衡默认的估算口径，规则固定、可复现、可核对：
//
//	ASCII 可打印字符：每 4 字节 ≈ 1 token（业界常用的 chars/4 近似）
//	非 ASCII（CJK 等）：每 2.2 字节 ≈ 1 token
//	（CJK 一般 3 字节 1 个字 ≈ 1~1.5 token，取 2.2 字节/token 偏保守）
//
// 该口径**只是估算**，调用方必须通过 Breakdown.Counted=false 如实告知用户。
func EstimateTokens(s string) int {
	ascii, nonASCII := 0, 0
	for _, r := range s {
		if r < utf8.RuneSelf {
			ascii++
		} else {
			nonASCII += utf8.RuneLen(r)
		}
	}
	t := float64(ascii)/4.0 + float64(nonASCII)/2.2
	if t < 0 {
		return 0
	}
	return int(t + 0.5)
}

// count 统计一段文本的 token：有真实 tokenizer 就用它，否则估算。
func count(s string, c Counter, b *Breakdown) int {
	if s == "" {
		return 0
	}
	if c != nil {
		return c.Count(s)
	}
	return EstimateTokens(s)
}

// ---------- 形状提取（兼容 OpenAI 与 Anthropic）----------

type sysItem struct{ name, text string }
type msgItem struct{ role, text string }

func extractSystem(raw map[string]json.RawMessage) (string, []sysItem) {
	// Anthropic: "system" 可以是字符串或 [{type:"text",text:...}]
	if v, ok := raw["system"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			return s, []sysItem{{name: "prompt", text: s}}
		}
		var arr []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(v, &arr) == nil && len(arr) > 0 {
			var sb strings.Builder
			items := make([]sysItem, 0, len(arr))
			for _, a := range arr {
				if a.Text == "" {
					continue
				}
				sb.WriteString(a.Text)
				name := a.Type
				if name == "" {
					name = "prompt"
				}
				items = append(items, sysItem{name: name, text: a.Text})
			}
			return sb.String(), items
		}
	}
	// OpenAI 兼容：system 也可能出现在 messages[0].role == "system"，
	// 但那种情况按 messages 统计更自然，这里不重复计入。
	return "", nil
}

func extractTools(v json.RawMessage) (int, []string) {
	var arr []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if json.Unmarshal(v, &arr) != nil || len(arr) == 0 {
		return 0, nil
	}
	total := 0
	names := make([]string, 0, len(arr))
	for _, t := range arr {
		// 每个工具的 schema 与描述都真实计入上下文。
		body, _ := json.Marshal(t)
		total += EstimateTokens(string(body))
		n := t.Function.Name
		if n == "" {
			n = t.Name
		}
		if n == "" {
			n = t.Type
		}
		if n == "" {
			n = "tool"
		}
		names = append(names, n)
	}
	return total, names
}

func extractMessages(raw map[string]json.RawMessage) (string, []msgItem) {
	mv, ok := raw["messages"]
	if !ok {
		if pv, ok2 := raw["prompt"]; ok2 {
			var s string
			if json.Unmarshal(pv, &s) == nil {
				return s, []msgItem{{role: "prompt", text: s}}
			}
			var arr []string
			if json.Unmarshal(pv, &arr) == nil {
				return strings.Join(arr, "\n"), []msgItem{{role: "prompt", text: strings.Join(arr, "\n")}}
			}
		}
		return "", nil
	}
	var arr []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(mv, &arr) != nil {
		return "", nil
	}
	var sb strings.Builder
	out := make([]msgItem, 0, len(arr))
	for _, m := range arr {
		text := contentText(m.Content)
		if text == "" {
			continue
		}
		sb.WriteString(text)
		role := m.Role
		if role == "" {
			role = "user"
		}
		out = append(out, msgItem{role: role, text: text})
	}
	return sb.String(), out
}

// contentText 把 content（字符串或分块数组）拍平成文本。
func contentText(v json.RawMessage) string {
	if len(v) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string          `json:"type"`
		Text string          `json:"text"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(v, &blocks) == nil {
		var sb strings.Builder
		for _, bl := range blocks {
			if bl.Text != "" {
				sb.WriteString(bl.Text)
				continue
			}
			// 非文本块（图片等）按真实字节规模折算，避免低估上下文占用。
			if len(bl.Data) > 0 {
				sb.WriteString(strings.Repeat("x", len(bl.Data)/8))
			}
		}
		return sb.String()
	}
	return ""
}
