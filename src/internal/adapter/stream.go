package adapter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	maxSSEEventBytes = 4 << 20
	maxSSEDataBytes  = 4 << 20
)

// sanitizeRawData 剔除上游 data 行中的 CR/LF（SSE 把 \r 也视为行分隔符，
// 恶意上游可借此向下游注入伪造的 data:/event: 帧），并校验为合法 JSON。
func sanitizeRawData(s string) (string, bool) {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	if len(s) > maxSSEDataBytes {
		return "", false
	}
	if !json.Valid([]byte(s)) {
		return "", false
	}
	return s, true
}

// ---------- 通用 SSE 事件读取 ----------

type sseEvent struct {
	event string
	data  string
}

// newSSEScanner 构造一个足够大缓冲的逐行扫描器（SSE 单行可能很大）。
func newSSEScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	return sc
}

// nextEvent 从扫描器读取一个完整 SSE 事件（空行分隔）。
func nextEvent(sc *bufio.Scanner) (sseEvent, bool) {
	event := ""
	var dataLines []string
	sawAny := false
	dataBytes := 0
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if line == "" {
			if sawAny {
				return sseEvent{event: event, data: strings.Join(dataLines, "\n")}, true
			}
			continue // 忽略连续空行
		}
		sawAny = true
		if strings.HasPrefix(line, ":") {
			continue // 注释 / : ping keep-alive
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			dataBytes += len(d)
			dataLines = append(dataLines, d)
		} else if strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") {
			// 忽略 id/retry
		} else {
			// 无前缀的数据行也纳入
			dataBytes += len(line)
			dataLines = append(dataLines, line)
		}
		if dataBytes > maxSSEEventBytes {
			return sseEvent{event: "error", data: "SSE event exceeds size limit"}, true
		}
	}
	if sawAny {
		return sseEvent{event: event, data: strings.Join(dataLines, "\n")}, true
	}
	return sseEvent{}, false
}

// ---------- OpenAI SSE -> UnifiedChunk 迭代器 ----------

type openAIStreamIterator struct {
	sc      *bufio.Scanner
	exhausted bool
}

func (it *openAIStreamIterator) Next() *UnifiedChunk {
	if it.exhausted {
		return &UnifiedChunk{Done: true}
	}
	for {
		ev, ok := nextEvent(it.sc)
		if !ok {
			it.exhausted = true
			if err := it.sc.Err(); err != nil {
				return &UnifiedChunk{Err: fmt.Errorf("SSE 扫描失败: %w", err)}
			}
			return &UnifiedChunk{Done: true}
		}
		if ev.event == "error" {
			return &UnifiedChunk{Err: fmt.Errorf("上游流式错误帧: %s", ev.data)}
		}
		data := strings.TrimSpace(ev.data)
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			it.exhausted = true
			return &UnifiedChunk{Done: true}
		}
		uc := parseOpenAIChunk(data)
		if uc != nil {
			uc.Raw = data // 逐字保留，供 passthrough 复用
			return uc
		}
	}
}

func parseOpenAIChunk(data string) *UnifiedChunk {
	m, err := asRawMap([]byte(data))
	if err != nil {
		return nil
	}
	uc := &UnifiedChunk{}
	uc.ID, _ = strField(m, "id")
	uc.Model, _ = strField(m, "model")
	var choices []map[string]json.RawMessage
	if v, ok := m["choices"]; ok {
		_ = json.Unmarshal(v, &choices)
	}
	if len(choices) > 0 {
		uc.FinishReason, _ = strField(choices[0], "finish_reason")
		var delta map[string]json.RawMessage
		_ = json.Unmarshal(choices[0]["delta"], &delta)
		uc.Role, _ = strField(delta, "role")
		uc.Content, _ = strField(delta, "content")
		if tc, ok := delta["tool_calls"]; ok {
			uc.ToolCalls = parseChunkToolCalls(tc)
		}
	}
	if v, ok := m["usage"]; ok {
		var u map[string]json.RawMessage
		_ = json.Unmarshal(v, &u)
		var pt, ct int
		_ = json.Unmarshal(u["prompt_tokens"], &pt)
		_ = json.Unmarshal(u["completion_tokens"], &ct)
		pt, ct = safeUsageInt(pt), safeUsageInt(ct)
		uc.Usage = &Usage{PromptTokens: pt, CompletionTokens: ct, TotalTokens: pt + ct}
	}
	return uc
}

func parseChunkToolCalls(raw json.RawMessage) []ChunkToolCall {
	var calls []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil
	}
	out := make([]ChunkToolCall, 0, len(calls))
	for _, c := range calls {
		ct := ChunkToolCall{}
		_ = json.Unmarshal(c["index"], &ct.Index)
		ct.ID, _ = strField(c, "id")
		ct.Type, _ = strField(c, "type")
		if fn, ok := c["function"]; ok {
			var f map[string]json.RawMessage
			_ = json.Unmarshal(fn, &f)
			ct.Name, _ = strField(f, "name")
			ct.ArgsPart, _ = strField(f, "arguments")
		}
		out = append(out, ct)
	}
	return out
}

// ---------- Anthropic SSE -> UnifiedChunk 状态机 ----------

type anthropicStreamIterator struct {
	sc           *bufio.Scanner
	id           string
	model        string
	promptTokens int
	outputTokens int
	blockType    map[int]string // content_block index -> "text"|"tool_use"
	toolIndex    map[int]int    // content_block index -> OpenAI tool_call index
	nextToolIdx  int
	exhausted    bool
}

func (it *anthropicStreamIterator) Next() *UnifiedChunk {
	if it.exhausted {
		return &UnifiedChunk{Done: true}
	}
	for {
		ev, ok := nextEvent(it.sc)
		if !ok {
			it.exhausted = true
			if err := it.sc.Err(); err != nil {
				return &UnifiedChunk{Err: fmt.Errorf("SSE 扫描失败: %w", err)}
			}
			return &UnifiedChunk{Done: true}
		}
		switch ev.event {
		case "ping":
			continue
		case "error":
			return &UnifiedChunk{Err: fmt.Errorf("Anthropic 流式错误: %s", ev.data)}
		case "message_start":
			var d struct {
				Message struct {
					ID    string `json:"id"`
					Model string `json:"model"`
					Usage struct {
						InputTokens int `json:"input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(ev.data), &d); err == nil {
				it.id, it.model = d.Message.ID, d.Message.Model
				it.promptTokens = d.Message.Usage.InputTokens
			}
			// OpenAI 规范首块带 role
			return &UnifiedChunk{ID: it.id, Model: it.model, Role: "assistant"}
		case "content_block_start":
			var d struct {
				Index        int             `json:"index"`
				ContentBlock json.RawMessage `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(ev.data), &d); err != nil {
				continue
			}
			// 上游可控 index 不得无限扩张内部状态（内存耗尽）。
			if d.Index < 0 || d.Index > 1024 || len(it.blockType) > 1024 {
				continue
			}
			var cb map[string]json.RawMessage
			_ = json.Unmarshal(d.ContentBlock, &cb)
			t, _ := strField(cb, "type")
			it.blockType[d.Index] = t
			if t == "tool_use" {
				id, _ := strField(cb, "id")
				name, _ := strField(cb, "name")
				idx := it.nextToolIdx
				it.nextToolIdx++
				it.toolIndex[d.Index] = idx
				return &UnifiedChunk{ID: it.id, Model: it.model, ToolCalls: []ChunkToolCall{
					{Index: idx, ID: id, Type: "function", Name: name},
				}}
			}
			continue // text 块起始无增量
		case "content_block_delta":
			var d struct {
				Index int             `json:"index"`
				Delta json.RawMessage `json:"delta"`
			}
			if err := json.Unmarshal([]byte(ev.data), &d); err != nil {
				continue
			}
			var delta map[string]json.RawMessage
			_ = json.Unmarshal(d.Delta, &delta)
			dt, _ := strField(delta, "type")
			switch dt {
			case "text_delta":
				txt, _ := strField(delta, "text")
				return &UnifiedChunk{ID: it.id, Model: it.model, Content: txt}
			case "input_json_delta":
				part, _ := strField(delta, "partial_json")
				idx := it.toolIndex[d.Index]
				return &UnifiedChunk{ID: it.id, Model: it.model, ToolCalls: []ChunkToolCall{
					{Index: idx, ArgsPart: part},
				}}
			}
			continue
		case "content_block_stop":
			continue
		case "message_delta":
			var d struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(ev.data), &d); err != nil {
				continue
			}
			it.outputTokens = d.Usage.OutputTokens
			u := AnthropicUsageToOpenAI(it.promptTokens, it.outputTokens)
			return &UnifiedChunk{ID: it.id, Model: it.model,
				FinishReason: MapStopToFinish(d.Delta.StopReason), Usage: &u}
		case "message_stop":
			it.exhausted = true
			return &UnifiedChunk{Done: true}
		default:
			// 无 event 类型但 data 是 message_start 之类的兼容兜底：尝试按 data 中的 type 判定
			if strings.Contains(ev.data, `"type":"message_start"`) {
				var d struct {
					Message struct {
						ID    string `json:"id"`
						Model string `json:"model"`
					} `json:"message"`
				}
				_ = json.Unmarshal([]byte(ev.data), &d)
				it.id, it.model = d.Message.ID, d.Message.Model
				continue
			}
			continue
		}
	}
}

// ---------- UnifiedChunk -> OpenAI SSE ----------

// ChunkToOpenAISSE 把统一块序列化为 OpenAI chat.completion.chunk SSE 帧。
// 若上游本身就是 OpenAI 兼容格式（Raw 非空），逐字复用原始载荷，保留 grok reasoning_content 等字段。
func ChunkToOpenAISSE(uc *UnifiedChunk) []byte {
	if uc == nil {
		return []byte("data: [DONE]\n\n")
	}
	if uc.Done {
		return []byte("data: [DONE]\n\n")
	}
	if uc.Raw != "" && uc.Content == "" && len(uc.ToolCalls) == 0 && uc.FinishReason == "" {
		raw, ok := sanitizeRawData(uc.Raw)
		if !ok {
			return []byte("data: {}\n\n")
		}
		return []byte("data: " + raw + "\n\n")
	}
	id, model := uc.ID, uc.Model
	if id == "" {
		id = "chatcmpl-llmhub"
	}
	if model == "" {
		model = "llm-hub"
	}
	delta := map[string]interface{}{}
	if uc.Role != "" {
		delta["role"] = uc.Role
	}
	if uc.Content != "" {
		delta["content"] = uc.Content
	}
	if len(uc.ToolCalls) > 0 {
		tcs := make([]map[string]interface{}, 0, len(uc.ToolCalls))
		for _, t := range uc.ToolCalls {
			entry := map[string]interface{}{"index": t.Index}
			if t.ID != "" {
				entry["id"] = t.ID
			}
			if t.Type != "" {
				entry["type"] = t.Type
			}
			if t.Name != "" || t.ArgsPart != "" {
				fn := map[string]interface{}{}
				if t.Name != "" {
					fn["name"] = t.Name
				}
				if t.ArgsPart != "" {
					fn["arguments"] = t.ArgsPart
				}
				entry["function"] = fn
			}
			tcs = append(tcs, entry)
		}
		delta["tool_calls"] = tcs
	}
	choice := map[string]interface{}{"index": 0, "delta": delta}
	if uc.FinishReason != "" {
		choice["finish_reason"] = uc.FinishReason
	} else {
		choice["finish_reason"] = nil
	}
	chunk := map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(),
		"model": model, "choices": []interface{}{choice},
	}
	if uc.Usage != nil {
		chunk["usage"] = uc.Usage
	}
	b, _ := json.Marshal(chunk)
	return []byte("data: " + string(b) + "\n\n")
}

// ---------- UnifiedChunk -> Anthropic SSE（面向 /v1/messages 流式客户端） ----------

// AnthropicStreamWriter 把统一块序列转成 Anthropic Messages SSE 事件序列（状态机）。
type AnthropicStreamWriter struct {
	started    bool
	msgID      string
	model      string
	blockIndex int
	blockOpen  bool
	blockType  string // "text" | "tool_use"
	toolBlocks map[int]bool
	inputTok   int
	outputTok  int
}

// NewAnthropicStreamWriter 构造写入器。
func NewAnthropicStreamWriter() *AnthropicStreamWriter {
	return &AnthropicStreamWriter{toolBlocks: map[int]bool{}}
}

func sseEventBytes(event string, payload interface{}) []byte {
	b, _ := json.Marshal(payload)
	return []byte("event: " + event + "\ndata: " + string(b) + "\n\n")
}

// Write 消费一个统一块，返回要写给客户端的 Anthropic SSE 字节（可能含多个事件）。
func (w *AnthropicStreamWriter) Write(uc *UnifiedChunk) []byte {
	if uc == nil || uc.Done {
		return w.finish("end_turn")
	}
	var out []byte
	if !w.started {
		w.started = true
		w.msgID = uc.ID
		if w.msgID == "" {
			w.msgID = "msg_llmhub"
		}
		w.model = uc.Model
		if w.model == "" {
			w.model = "llm-hub"
		}
		out = append(out, sseEventBytes("message_start", map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id": w.msgID, "type": "message", "role": "assistant", "model": w.model,
				"content": []interface{}{}, "stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
			},
		})...)
		// 打开首个文本块
		w.blockOpen = true
		w.blockType = "text"
		out = append(out, sseEventBytes("content_block_start", map[string]interface{}{
			"type": "content_block_start", "index": w.blockIndex,
			"content_block": map[string]interface{}{"type": "text", "text": ""},
		})...)
	}
	if uc.Content != "" {
		if w.blockType != "text" {
			// 结束工具块并新开文本块
			out = append(out, w.closeBlock()...)
			w.blockOpen, w.blockType = true, "text"
			out = append(out, sseEventBytes("content_block_start", map[string]interface{}{
				"type": "content_block_start", "index": w.blockIndex,
				"content_block": map[string]interface{}{"type": "text", "text": ""},
			})...)
		}
		out = append(out, sseEventBytes("content_block_delta", map[string]interface{}{
			"type": "content_block_delta", "index": w.blockIndex,
			"delta": map[string]interface{}{"type": "text_delta", "text": uc.Content},
		})...)
	}
	for _, t := range uc.ToolCalls {
		if t.ID != "" && t.Name != "" {
			// 新工具块
			if w.blockOpen {
				out = append(out, w.closeBlock()...)
			}
			w.blockIndex++
			w.blockOpen, w.blockType = true, "tool_use"
			out = append(out, sseEventBytes("content_block_start", map[string]interface{}{
				"type": "content_block_start", "index": w.blockIndex,
				"content_block": map[string]interface{}{
					"type": "tool_use", "id": t.ID, "name": t.Name, "input": map[string]interface{}{},
				},
			})...)
		}
		if t.ArgsPart != "" {
			out = append(out, sseEventBytes("content_block_delta", map[string]interface{}{
				"type": "content_block_delta", "index": w.blockIndex,
				"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": t.ArgsPart},
			})...)
		}
	}
	if uc.Usage != nil {
		w.inputTok = uc.Usage.PromptTokens
		w.outputTok = uc.Usage.CompletionTokens
	}
	if uc.FinishReason != "" {
		return append(out, w.finish(MapFinishToStop(uc.FinishReason))...)
	}
	return out
}

func (w *AnthropicStreamWriter) closeBlock() []byte {
	if !w.blockOpen {
		return nil
	}
	w.blockOpen = false
	return sseEventBytes("content_block_stop", map[string]interface{}{
		"type": "content_block_stop", "index": w.blockIndex,
	})
}

func (w *AnthropicStreamWriter) finish(stopReason string) []byte {
	if !w.started {
		return nil
	}
	var out []byte
	out = append(out, w.closeBlock()...)
	out = append(out, sseEventBytes("message_delta", map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": map[string]interface{}{"output_tokens": w.outputTok},
	})...)
	out = append(out, sseEventBytes("message_stop", map[string]interface{}{"type": "message_stop"})...)
	return out
}
