package adapter

import (
	"encoding/json"
	"strings"
	"time"
)

// maxUsageTokens 上游 usage 字段不可信：钳制到合法范围，防止负数/溢出污染计费。
const maxUsageTokens = 1 << 31

func safeUsageInt(n int) int {
	if n < 0 || n > maxUsageTokens {
		return 0
	}
	return n
}

// ---------- finish_reason / stop_reason 映射 ----------

// MapFinishToStop 把 OpenAI finish_reason 映射为 Anthropic stop_reason。
func MapFinishToStop(finish string) string {
	switch finish {
	case "length":
		return "max_tokens"
	case "tool_calls", "tool_use":
		return "tool_use"
	case "stop", "content_filter", "stop_sequence":
		return "end_turn"
	default:
		if finish == "" {
			return "end_turn"
		}
		return "end_turn"
	}
}

// MapStopToFinish 把 Anthropic stop_reason 映射为 OpenAI finish_reason。
func MapStopToFinish(stop string) string {
	switch stop {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "end_turn", "stop_sequence", "pause_turn", "refusal":
		return "stop"
	default:
		return "stop"
	}
}

// AnthropicUsageToOpenAI 把 input_tokens/output_tokens 映射为 OpenAI usage。
func AnthropicUsageToOpenAI(inputTokens, outputTokens int) Usage {
	in, out := safeUsageInt(inputTokens), safeUsageInt(outputTokens)
	return Usage{PromptTokens: in, CompletionTokens: out, TotalTokens: in + out}
}

// ---------- tools 双向映射 ----------

// OpenAIToolsToAnthropic: OpenAI tools[].function -> Anthropic tools[].input_schema。
func OpenAIToolsToAnthropic(toolsRaw json.RawMessage) (json.RawMessage, error) {
	if len(toolsRaw) == 0 || string(toolsRaw) == "null" {
		return json.RawMessage("[]"), nil
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(toolsRaw, &tools); err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		fn := map[string]json.RawMessage{}
		if fraw, ok := t["function"]; ok {
			_ = json.Unmarshal(fraw, &fn)
		}
		name, _ := strField(fn, "name")
		desc, _ := strField(fn, "description")
		params := fn["parameters"]
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		item := map[string]interface{}{
			"name": name, "description": desc, "input_schema": params,
		}
		out = append(out, item)
	}
	return json.Marshal(out)
}

// AnthropicToolsToOpenAI: Anthropic tools[].input_schema -> OpenAI tools[].function.parameters。
func AnthropicToolsToOpenAI(toolsRaw json.RawMessage) (json.RawMessage, error) {
	if len(toolsRaw) == 0 || string(toolsRaw) == "null" {
		return json.RawMessage("[]"), nil
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(toolsRaw, &tools); err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		name, _ := strField(t, "name")
		desc, _ := strField(t, "description")
		schema := t["input_schema"]
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		fn := map[string]interface{}{"name": name, "description": desc, "parameters": schema}
		out = append(out, map[string]interface{}{"type": "function", "function": fn})
	}
	return json.Marshal(out)
}

func strField(m map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := m[key]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

func parseJSONArgs(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return map[string]interface{}{}
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return map[string]interface{}{}
	}
	return v
}

// ---------- 请求：OpenAI -> Anthropic ----------

// OpenAIToAnthropicRequest 把 OpenAI chat.completions 请求体转为 Anthropic /messages 请求体。
func OpenAIToAnthropicRequest(raw []byte) ([]byte, error) {
	m, err := asRawMap(raw)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{}

	if v, ok := m["model"]; ok {
		var model string
		_ = json.Unmarshal(v, &model)
		out["model"] = model
	}
	// system 抽取 + messages 角色/工具转换
	if msgs, ok := m["messages"]; ok {
		sys, conv, err := openAIMessagesToAnthropic(msgs)
		if err != nil {
			return nil, err
		}
		if len(sys) > 0 {
			out["system"] = sys
		}
		out["messages"] = conv
	} else {
		out["messages"] = []interface{}{}
	}
	// max_tokens 必填
	maxTokens := 1024
	if v, ok := m["max_tokens"]; ok {
		_ = json.Unmarshal(v, &maxTokens)
	} else if v, ok := m["max_completion_tokens"]; ok {
		_ = json.Unmarshal(v, &maxTokens)
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	out["max_tokens"] = maxTokens
	// tools / tool_choice
	if v, ok := m["tools"]; ok {
		conv, err := OpenAIToolsToAnthropic(v)
		if err != nil {
			return nil, err
		}
		out["tools"] = conv
	}
	if v, ok := m["tool_choice"]; ok {
		out["tool_choice"] = openAIToolChoiceToAnthropic(v)
	}
	// 直接透传的其他参数
	for _, k := range []string{"temperature", "top_p", "stream", "metadata"} {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	if v, ok := m["top_k"]; ok {
		out["top_k"] = v
	}
	if v, ok := m["stop"]; ok {
		var stops []string
		if err := json.Unmarshal(v, &stops); err == nil {
			out["stop_sequences"] = stops
		} else {
			var one string
			if err := json.Unmarshal(v, &one); err == nil {
				out["stop_sequences"] = []string{one}
			}
		}
	}
	return json.Marshal(out)
}

func openAIToolChoiceToAnthropic(v json.RawMessage) interface{} {
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		switch s {
		case "auto", "none":
			return map[string]interface{}{"type": "auto"}
		case "required":
			return map[string]interface{}{"type": "any"}
		default:
			return map[string]interface{}{"type": "tool", "name": s}
		}
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(v, &m); err == nil {
		if fnraw, ok := m["function"]; ok {
			var fn map[string]json.RawMessage
			_ = json.Unmarshal(fnraw, &fn)
			if name, ok := strField(fn, "name"); ok {
				return map[string]interface{}{"type": "tool", "name": name}
			}
		}
	}
	return map[string]interface{}{"type": "auto"}
}

// openAIMessagesToAnthropic 处理 system 抽取、assistant tool_calls->tool_use、
// tool role->tool_result（连续 tool 消息合并进同一条 user 消息）。
func openAIMessagesToAnthropic(msgsRaw json.RawMessage) (system []map[string]interface{}, out []interface{}, err error) {
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(msgsRaw, &msgs); err != nil {
		return nil, nil, err
	}
	out = []interface{}{}
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		role, _ := strField(m, "role")
		content := m["content"]
		switch role {
		case "system", "developer":
			system = append(system, textBlocks(content)...)
		case "tool":
			// 收集连续的 tool 结果，合并为一条 user/tool_result 消息
			blocks := []map[string]interface{}{}
			for i < len(msgs) {
				r, _ := strField(msgs[i], "role")
				if r != "tool" {
					break
				}
				tcid, _ := strField(msgs[i], "tool_call_id")
				blocks = append(blocks, map[string]interface{}{
					"type": "tool_result", "tool_use_id": tcid, "content": normalizeToolResultContent(msgs[i]["content"]),
				})
				i++
			}
			i--
			out = append(out, map[string]interface{}{"role": "user", "content": blocks})
		case "assistant":
			blocks := textBlocks(content)
			if tc, ok := m["tool_calls"]; ok {
				var calls []map[string]json.RawMessage
				_ = json.Unmarshal(tc, &calls)
				for _, c := range calls {
					id, _ := strField(c, "id")
					var fn map[string]json.RawMessage
					if fraw, ok := c["function"]; ok {
						_ = json.Unmarshal(fraw, &fn)
					}
					name, _ := strField(fn, "name")
					argsRaw, _ := strField(fn, "arguments")
					blocks = append(blocks, map[string]interface{}{
						"type": "tool_use", "id": id, "name": name, "input": parseJSONArgs(argsRaw),
					})
				}
			}
			if len(blocks) == 0 {
				blocks = append(blocks, map[string]interface{}{"type": "text", "text": ""})
			}
			out = append(out, map[string]interface{}{"role": "assistant", "content": blocks})
		default: // user 及未知角色
			conv := openAIContentToAnthropic(content)
			out = append(out, map[string]interface{}{"role": "user", "content": conv})
		}
	}
	return system, out, nil
}

// textBlocks 把 content（字符串或数组）转成 Anthropic text block 数组。
func textBlocks(content json.RawMessage) []map[string]interface{} {
	if len(content) == 0 || string(content) == "null" {
		return []map[string]interface{}{}
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return []map[string]interface{}{{"type": "text", "text": s}}
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(content, &parts); err == nil {
		out := []map[string]interface{}{}
		for _, p := range parts {
			t, _ := strField(p, "type")
			if t == "text" {
				txt, _ := strField(p, "text")
				out = append(out, map[string]interface{}{"type": "text", "text": txt})
			}
		}
		return out
	}
	return []map[string]interface{}{}
}

// openAIContentToAnthropic 把 user content（字符串 / 多模态数组）转为 Anthropic content。
func openAIContentToAnthropic(content json.RawMessage) interface{} {
	if len(content) == 0 || string(content) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s // Anthropic 接受纯字符串
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(content, &parts); err != nil {
		return string(content)
	}
	blocks := []map[string]interface{}{}
	for _, p := range parts {
		t, _ := strField(p, "type")
		switch t {
		case "text":
			txt, _ := strField(p, "text")
			blocks = append(blocks, map[string]interface{}{"type": "text", "text": txt})
		case "image_url":
			var iu map[string]json.RawMessage
			_ = json.Unmarshal(p["image_url"], &iu)
			url, _ := strField(iu, "url")
			blocks = append(blocks, imageSource(url))
		default:
			// 未知类型按文本透传
			blocks = append(blocks, map[string]interface{}{"type": "text", "text": string(p["text"])})
		}
	}
	return blocks
}

func imageSource(url string) map[string]interface{} {
	src := map[string]interface{}{}
	if strings.HasPrefix(url, "data:") {
		// data:image/png;base64,XXXX
		if i := strings.IndexByte(url, ','); i > 0 {
			meta := url[5:i]
			media := "image/png"
			if semi := strings.IndexByte(meta, ';'); semi > 0 {
				media = meta[:semi]
			}
			src = map[string]interface{}{"type": "base64", "media_type": media, "data": url[i+1:]}
		}
	} else {
		src = map[string]interface{}{"type": "url", "url": url}
	}
	return map[string]interface{}{"type": "image", "source": src}
}

func normalizeToolResultContent(content json.RawMessage) interface{} {
	if len(content) == 0 || string(content) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	return content
}

// ---------- 请求：Anthropic -> OpenAI ----------

// AnthropicToOpenAIRequest 把 Anthropic /messages 请求体转为 OpenAI chat.completions 请求体。
func AnthropicToOpenAIRequest(raw []byte) ([]byte, error) {
	m, err := asRawMap(raw)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{}
	if v, ok := m["model"]; ok {
		var model string
		_ = json.Unmarshal(v, &model)
		out["model"] = model
	}
	// system -> 首条 system 消息
	msgs := []interface{}{}
	if v, ok := m["system"]; ok {
		msgs = append(msgs, map[string]interface{}{"role": "system", "content": anthropicSystemToText(v)})
	}
	if v, ok := m["messages"]; ok {
		conv, err := anthropicMessagesToOpenAI(v)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, conv...)
	}
	out["messages"] = msgs
	// max_tokens
	if v, ok := m["max_tokens"]; ok {
		out["max_tokens"] = v
	}
	// tools / tool_choice
	if v, ok := m["tools"]; ok {
		conv, err := AnthropicToolsToOpenAI(v)
		if err != nil {
			return nil, err
		}
		out["tools"] = conv
	}
	if v, ok := m["tool_choice"]; ok {
		out["tool_choice"] = anthropicToolChoiceToOpenAI(v)
	}
	for _, k := range []string{"temperature", "top_p", "top_k", "stream"} {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	if v, ok := m["stop_sequences"]; ok {
		out["stop"] = v
	}
	return json.Marshal(out)
}

func anthropicSystemToText(v json.RawMessage) interface{} {
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(v, &blocks); err == nil {
		parts := []string{}
		for _, b := range blocks {
			t, _ := strField(b, "text")
			parts = append(parts, t)
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func anthropicToolChoiceToOpenAI(v json.RawMessage) interface{} {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(v, &m); err != nil {
		return v
	}
	t, _ := strField(m, "type")
	switch t {
	case "tool":
		name, _ := strField(m, "name")
		return map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": name}}
	case "any":
		return "required"
	default:
		return "auto"
	}
}

func anthropicMessagesToOpenAI(raw json.RawMessage) ([]interface{}, error) {
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return nil, err
	}
	out := []interface{}{}
	for _, m := range msgs {
		role, _ := strField(m, "role")
		content := m["content"]
		// content 为字符串：直接透传
		var s string
		if err := json.Unmarshal(content, &s); err == nil {
			out = append(out, map[string]interface{}{"role": role, "content": s})
			continue
		}
		var blocks []map[string]json.RawMessage
		if err := json.Unmarshal(content, &blocks); err != nil {
			return nil, err
		}
		textParts := []string{}
		toolCalls := []map[string]interface{}{}
		toolResults := []interface{}{}
		for _, b := range blocks {
			t, _ := strField(b, "type")
			switch t {
			case "text":
				txt, _ := strField(b, "text")
				textParts = append(textParts, txt)
			case "tool_use":
				id, _ := strField(b, "id")
				name, _ := strField(b, "name")
				input := b["input"]
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				toolCalls = append(toolCalls, map[string]interface{}{
					"id": id, "type": "function",
					"function": map[string]interface{}{"name": name, "arguments": string(input)},
				})
			case "tool_result":
				tcid, _ := strField(b, "tool_use_id")
				toolResults = append(toolResults, map[string]interface{}{
					"role": "tool", "tool_call_id": tcid, "content": normalizeToolResultContent(b["content"]),
				})
			}
		}
		if len(toolResults) > 0 {
			// tool_result 属于 user 消息，拆成独立 tool 消息
			out = append(out, toolResults...)
			continue
		}
		text := strings.Join(textParts, "")
		msg := map[string]interface{}{"role": role}
		if len(toolCalls) > 0 {
			msg["content"] = nil
			msg["tool_calls"] = toolCalls
		} else {
			msg["content"] = text
		}
		out = append(out, msg)
	}
	return out, nil
}

// ---------- 响应：Anthropic -> OpenAI ----------

// AnthropicToOpenAIResponse 把 Anthropic /messages 非流式响应转为 OpenAI chat.completion。
func AnthropicToOpenAIResponse(status int, raw []byte) ([]byte, error) {
	m, err := asRawMap(raw)
	if err != nil {
		return nil, err
	}
	id, _ := strField(m, "id")
	model, _ := strField(m, "model")
	stopReason, _ := strField(m, "stop_reason")
	var contentBlocks []map[string]json.RawMessage
	if v, ok := m["content"]; ok {
		_ = json.Unmarshal(v, &contentBlocks)
	}
	textParts := []string{}
	toolCalls := []map[string]interface{}{}
	for _, b := range contentBlocks {
		t, _ := strField(b, "type")
		switch t {
		case "text":
			txt, _ := strField(b, "text")
			textParts = append(textParts, txt)
		case "tool_use":
			cid, _ := strField(b, "id")
			name, _ := strField(b, "name")
			input := b["input"]
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			toolCalls = append(toolCalls, map[string]interface{}{
				"id": cid, "type": "function",
				"function": map[string]interface{}{"name": name, "arguments": string(input)},
			})
		}
	}
	text := strings.Join(textParts, "")
	message := map[string]interface{}{"role": "assistant"}
	if len(toolCalls) > 0 {
		message["content"] = nil
		message["tool_calls"] = toolCalls
	} else {
		message["content"] = text
	}
	var inTok, outTok int
	if v, ok := m["usage"]; ok {
		var u map[string]json.RawMessage
		_ = json.Unmarshal(v, &u)
		_ = json.Unmarshal(u["input_tokens"], &inTok)
		_ = json.Unmarshal(u["output_tokens"], &outTok)
	}
	usage := AnthropicUsageToOpenAI(safeUsageInt(inTok), safeUsageInt(outTok))
	resp := map[string]interface{}{
		"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": model,
		"choices": []interface{}{map[string]interface{}{
			"index": 0, "message": message, "finish_reason": MapStopToFinish(stopReason),
		}},
		"usage": usage,
	}
	return json.Marshal(resp)
}

// ---------- 响应：OpenAI -> Anthropic ----------

// OpenAIToAnthropicResponse 把 OpenAI chat.completion 非流式响应转为 Anthropic message。
func OpenAIToAnthropicResponse(status int, raw []byte) ([]byte, error) {
	m, err := asRawMap(raw)
	if err != nil {
		return nil, err
	}
	id, _ := strField(m, "id")
	model, _ := strField(m, "model")
	var choices []map[string]json.RawMessage
	if v, ok := m["choices"]; ok {
		_ = json.Unmarshal(v, &choices)
	}
	content := []map[string]interface{}{}
	finish := "stop"
	if len(choices) > 0 {
		finish, _ = strField(choices[0], "finish_reason")
		var msg map[string]json.RawMessage
		_ = json.Unmarshal(choices[0]["message"], &msg)
		if c, ok := msg["content"]; ok {
			var text string
			if err := json.Unmarshal(c, &text); err == nil && text != "" {
				content = append(content, map[string]interface{}{"type": "text", "text": text})
			}
		}
		if tc, ok := msg["tool_calls"]; ok {
			var calls []map[string]json.RawMessage
			_ = json.Unmarshal(tc, &calls)
			for _, c := range calls {
				cid, _ := strField(c, "id")
				var fn map[string]json.RawMessage
				_ = json.Unmarshal(c["function"], &fn)
				name, _ := strField(fn, "name")
				args, _ := strField(fn, "arguments")
				content = append(content, map[string]interface{}{
					"type": "tool_use", "id": cid, "name": name, "input": parseJSONArgs(args),
				})
			}
		}
	}
	if len(content) == 0 {
		content = append(content, map[string]interface{}{"type": "text", "text": ""})
	}
	var pt, ct int
	if v, ok := m["usage"]; ok {
		var u map[string]json.RawMessage
		_ = json.Unmarshal(v, &u)
		_ = json.Unmarshal(u["prompt_tokens"], &pt)
		_ = json.Unmarshal(u["completion_tokens"], &ct)
	}
	resp := map[string]interface{}{
		"id": id, "type": "message", "role": "assistant", "model": model,
		"content": content, "stop_reason": MapFinishToStop(finish),
		"stop_sequence": nil,
		"usage": map[string]interface{}{"input_tokens": safeUsageInt(pt), "output_tokens": safeUsageInt(ct)},
	}
	return json.Marshal(resp)
}

// CanonicalizeIncoming 把不同数据面入口的原始请求统一成 OpenAI chat.completions 规范 body。
// wantFormat 记录客户端期望的响应格式。
func CanonicalizeIncoming(entrypoint string, raw []byte) (canonical []byte, wantFormat string, err error) {
	switch entrypoint {
	case "/v1/messages", "messages":
		canonical, err = AnthropicToOpenAIRequest(raw)
		return canonical, "anthropic", err
	default: // chat/completions, completions, responses, embeddings 等一律 OpenAI 规范
		return raw, "openai", nil
	}
}

// FormatResponse 把规范 OpenAI 响应按客户端期望格式转换。
func FormatResponse(wantFormat string, status int, canonical []byte) ([]byte, error) {
	if wantFormat == "anthropic" {
		return OpenAIToAnthropicResponse(status, canonical)
	}
	return canonical, nil
}

// ParseUsageFromCanonical 从 OpenAI 规范响应中抽取 usage（上游 usage 优先）。
func ParseUsageFromCanonical(raw []byte) Usage {
	m, err := asRawMap(raw)
	if err != nil {
		return Usage{}
	}
	v, ok := m["usage"]
	if !ok {
		return Usage{}
	}
	var u map[string]json.RawMessage
	_ = json.Unmarshal(v, &u)
	var pt, ct, tt int
	_ = json.Unmarshal(u["prompt_tokens"], &pt)
	_ = json.Unmarshal(u["completion_tokens"], &ct)
	_ = json.Unmarshal(u["total_tokens"], &tt)
	pt, ct = safeUsageInt(pt), safeUsageInt(ct)
	tt = safeUsageInt(tt)
	if tt == 0 {
		tt = pt + ct
	}
	return Usage{PromptTokens: pt, CompletionTokens: ct, TotalTokens: tt}
}
