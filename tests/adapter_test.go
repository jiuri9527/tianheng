package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"llm-hub/src/hub"
)

func asMap(t *testing.T, b []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("JSON 解析失败: %v; body=%s", err, b)
	}
	return m
}

func asSlice(t *testing.T, v interface{}) []interface{} {
	t.Helper()
	s, ok := v.([]interface{})
	if !ok {
		t.Fatalf("期望数组，实际 %T (%v)", v, v)
	}
	return s
}

func asStringMap(t *testing.T, v interface{}) map[string]interface{} {
	t.Helper()
	m, ok := v.(map[string]interface{})
	if !ok {
		t.Fatalf("期望对象，实际 %T (%v)", v, v)
	}
	return m
}

func TestAdapterOpenAIToAnthropicRequest(t *testing.T) {
	in := `{
	  "model":"liquid/lfm-3-1.7b",
	  "messages":[
	    {"role":"system","content":"You are helpful"},
	    {"role":"user","content":"hi"},
	    {"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},
	    {"role":"tool","tool_call_id":"call_1","content":"sunny"}
	  ],
	  "tools":[{"type":"function","function":{"name":"get_weather","description":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],
	  "tool_choice":"auto","max_tokens":128,"temperature":0.5,"stream":true,"stop":["END"]
	}`
	out, err := hub.OpenAIToAnthropicRequest([]byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := asMap(t, out)
	// system 抽到顶层
	sys := asSlice(t, m["system"])
	if len(sys) != 1 || !strings.Contains(asStringMap(t, sys[0])["text"].(string), "helpful") {
		t.Fatalf("system 抽取错误: %v", m["system"])
	}
	if m["max_tokens"].(float64) != 128 {
		t.Fatalf("max_tokens 错误: %v", m["max_tokens"])
	}
	if m["stream"] != true {
		t.Fatal("stream 未透传")
	}
	stops := asSlice(t, m["stop_sequences"])
	if len(stops) != 1 || stops[0] != "END" {
		t.Fatalf("stop_sequences 错误: %v", m["stop_sequences"])
	}
	// tools -> input_schema
	tools := asSlice(t, m["tools"])
	tool := asStringMap(t, tools[0])
	if tool["name"] != "get_weather" {
		t.Fatalf("tool name 错误: %v", tool)
	}
	if asStringMap(t, tool["input_schema"])["type"] != "object" {
		t.Fatalf("input_schema 错误: %v", tool["input_schema"])
	}
	// messages：无 system；assistant tool_use；tool_result
	msgs := asSlice(t, m["messages"])
	sawToolUse, sawToolResult := false, false
	for _, raw := range msgs {
		msg := asStringMap(t, raw)
		if msg["role"] == "system" {
			t.Fatal("messages 中不应残留 system 角色")
		}
		blocks, ok := msg["content"].([]interface{})
		if !ok {
			continue
		}
		for _, br := range blocks {
			b := asStringMap(t, br)
			if b["type"] == "tool_use" {
				sawToolUse = true
				if b["id"] != "call_1" || b["name"] != "get_weather" {
					t.Fatalf("tool_use 映射错误: %v", b)
				}
				if asStringMap(t, b["input"])["city"] != "SF" {
					t.Fatalf("tool_use input 错误: %v", b["input"])
				}
			}
			if b["type"] == "tool_result" {
				sawToolResult = true
				if b["tool_use_id"] != "call_1" || b["content"] != "sunny" {
					t.Fatalf("tool_result 映射错误: %v", b)
				}
			}
		}
	}
	if !sawToolUse || !sawToolResult {
		t.Fatalf("缺少 tool_use/tool_result：use=%v result=%v msgs=%v", sawToolUse, sawToolResult, m["messages"])
	}
}

func TestAdapterAnthropicToOpenAIRequest(t *testing.T) {
	in := `{
	  "model":"liquid/lfm-3-1.7b","system":"Be brief",
	  "messages":[
	    {"role":"user","content":[{"type":"text","text":"weather?"}]},
	    {"role":"assistant","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"tu_1","name":"get_weather","input":{"city":"NY"}}]},
	    {"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"rain"}]}
	  ],
	  "tools":[{"name":"get_weather","description":"w","input_schema":{"type":"object","properties":{}}}],
	  "tool_choice":{"type":"tool","name":"get_weather"},
	  "max_tokens":256,"stop_sequences":["STOP"],"stream":false,"temperature":0.2
	}`
	out, err := hub.AnthropicToOpenAIRequest([]byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := asMap(t, out)
	msgs := asSlice(t, m["messages"])
	first := asStringMap(t, msgs[0])
	if first["role"] != "system" || first["content"] != "Be brief" {
		t.Fatalf("system 未转为首条 system 消息: %v", first)
	}
	if m["max_tokens"].(float64) != 256 {
		t.Fatalf("max_tokens 错误: %v", m["max_tokens"])
	}
	if asSlice(t, m["stop"])[0] != "STOP" {
		t.Fatalf("stop 错误: %v", m["stop"])
	}
	tools := asSlice(t, m["tools"])
	fn := asStringMap(t, asStringMap(t, tools[0])["function"])
	if asStringMap(t, fn["parameters"])["type"] != "object" {
		t.Fatalf("parameters 映射错误: %v", fn["parameters"])
	}
	tcName := asStringMap(t, asStringMap(t, m["tool_choice"])["function"])["name"]
	if tcName != "get_weather" {
		t.Fatalf("tool_choice 映射错误: %v", m["tool_choice"])
	}
	// assistant tool_calls 与 tool 角色
	sawToolCalls, sawToolRole := false, false
	for _, raw := range msgs {
		msg := asStringMap(t, raw)
		if tcs, ok := msg["tool_calls"].([]interface{}); ok && len(tcs) > 0 {
			sawToolCalls = true
			fn := asStringMap(t, asStringMap(t, tcs[0])["function"])
			if fn["name"] != "get_weather" || !strings.Contains(fn["arguments"].(string), "NY") {
				t.Fatalf("tool_calls 映射错误: %v", tcs[0])
			}
		}
		if msg["role"] == "tool" {
			sawToolRole = true
			if msg["tool_call_id"] != "tu_1" || msg["content"] != "rain" {
				t.Fatalf("tool 消息映射错误: %v", msg)
			}
		}
	}
	if !sawToolCalls || !sawToolRole {
		t.Fatalf("缺少 tool_calls/tool role: calls=%v role=%v msgs=%v", sawToolCalls, sawToolRole, m["messages"])
	}
}

func TestAdapterFinishStopAndUsageMapping(t *testing.T) {
	cases := []struct{ stop, finish string }{
		{"end_turn", "stop"}, {"max_tokens", "length"}, {"tool_use", "tool_calls"}, {"stop_sequence", "stop"},
	}
	for _, c := range cases {
		if got := hub.MapStopToFinish(c.stop); got != c.finish {
			t.Errorf("MapStopToFinish(%q)=%q want %q", c.stop, got, c.finish)
		}
	}
	if hub.MapFinishToStop("stop") != "end_turn" || hub.MapFinishToStop("length") != "max_tokens" || hub.MapFinishToStop("tool_calls") != "tool_use" {
		t.Fatal("MapFinishToStop 映射错误")
	}
	u := hub.AnthropicUsageToOpenAI(11, 7)
	if u.PromptTokens != 11 || u.CompletionTokens != 7 || u.TotalTokens != 18 {
		t.Fatalf("usage 映射错误: %+v", u)
	}
}

func TestAdapterAnthropicToOpenAIResponse(t *testing.T) {
	in := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-x",
	  "content":[{"type":"text","text":"Hello "},{"type":"text","text":"world"},
	             {"type":"tool_use","id":"tu_2","name":"calc","input":{"a":1}}],
	  "stop_reason":"tool_use","usage":{"input_tokens":11,"output_tokens":7}}`
	out, err := hub.AnthropicToOpenAIResponse(200, []byte(in))
	if err != nil {
		t.Fatalf("响应转换失败: %v", err)
	}
	m := asMap(t, out)
	if m["object"] != "chat.completion" || m["id"] != "msg_1" {
		t.Fatalf("基础字段错误: %v", m)
	}
	choice := asStringMap(t, asSlice(t, m["choices"])[0])
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason 错误: %v", choice["finish_reason"])
	}
	msg := asStringMap(t, choice["message"])
	if msg["content"] != nil {
		t.Fatalf("有 tool_calls 时 content 应为 null: %v", msg["content"])
	}
	tcs := asSlice(t, msg["tool_calls"])
	fn := asStringMap(t, asStringMap(t, tcs[0])["function"])
	if asStringMap(t, tcs[0])["id"] != "tu_2" || fn["name"] != "calc" || !strings.Contains(fn["arguments"].(string), `"a":1`) {
		t.Fatalf("tool_calls 错误: %v", tcs[0])
	}
	usage := asStringMap(t, m["usage"])
	if usage["prompt_tokens"].(float64) != 11 || usage["completion_tokens"].(float64) != 7 || usage["total_tokens"].(float64) != 18 {
		t.Fatalf("usage 错误: %v", usage)
	}
}

func TestAdapterOpenAIToAnthropicResponse(t *testing.T) {
	in := `{"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"gpt-x",
	  "choices":[{"index":0,"message":{"role":"assistant","content":"Hi there",
	    "tool_calls":[{"id":"call_9","type":"function","function":{"name":"f","arguments":"{\"x\":2}"}}]},
	    "finish_reason":"tool_calls"}],
	  "usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`
	out, err := hub.OpenAIToAnthropicResponse(200, []byte(in))
	if err != nil {
		t.Fatalf("响应转换失败: %v", err)
	}
	m := asMap(t, out)
	if m["type"] != "message" || m["role"] != "assistant" {
		t.Fatalf("基础字段错误: %v", m)
	}
	if m["stop_reason"] != "tool_use" {
		t.Fatalf("stop_reason 错误: %v", m["stop_reason"])
	}
	content := asSlice(t, m["content"])
	if asStringMap(t, content[0])["text"] != "Hi there" {
		t.Fatalf("text 块错误: %v", content[0])
	}
	tu := asStringMap(t, content[1])
	if tu["type"] != "tool_use" || tu["id"] != "call_9" || tu["name"] != "f" {
		t.Fatalf("tool_use 块错误: %v", tu)
	}
	if asStringMap(t, tu["input"])["x"].(float64) != 2 {
		t.Fatalf("tool_use input 错误: %v", tu["input"])
	}
	usage := asStringMap(t, m["usage"])
	if usage["input_tokens"].(float64) != 5 || usage["output_tokens"].(float64) != 3 {
		t.Fatalf("usage 错误: %v", usage)
	}
}

func TestAdapterToolsBidirectional(t *testing.T) {
	oa := json.RawMessage(`[{"type":"function","function":{"name":"f","description":"d","parameters":{"type":"object"}}}]`)
	an, err := hub.OpenAIToolsToAnthropic(oa)
	if err != nil {
		t.Fatal(err)
	}
	arr := sliceFromRaw(t, json.RawMessage(an))
	item := asStringMap(t, arr[0])
	if item["name"] != "f" || asStringMap(t, item["input_schema"])["type"] != "object" {
		t.Fatalf("OpenAI->Anthropic tools 错误: %s", an)
	}
	back, err := hub.AnthropicToolsToOpenAI(an)
	if err != nil {
		t.Fatal(err)
	}
	arr2 := sliceFromRaw(t, json.RawMessage(back))
	fn := asStringMap(t, asStringMap(t, arr2[0])["function"])
	if asStringMap(t, arr2[0])["type"] != "function" || fn["name"] != "f" {
		t.Fatalf("Anthropic->OpenAI tools 错误: %s", back)
	}
}

func sliceFromRaw(t *testing.T, raw json.RawMessage) []interface{} {
	t.Helper()
	var s []interface{}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("解析数组失败: %v (%s)", err, raw)
	}
	return s
}

func TestAdapterAnthropicStreamStateMachine(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_s","type":"message","role":"assistant","model":"claude-s","content":[],"usage":{"input_tokens":9}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_s","name":"search","input":{}}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":4}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	it := hub.AnthropicStream(strings.NewReader(sse))
	var role string
	var content string
	var toolID, toolName, args string
	var finish string
	var usage *hub.Usage
	done := false
	for i := 0; i < 50; i++ {
		uc := it.Next()
		if uc == nil {
			break
		}
		if uc.Err != nil {
			t.Fatalf("流式错误: %v", uc.Err)
		}
		if uc.Done {
			done = true
			break
		}
		if uc.Role != "" {
			role = uc.Role
		}
		content += uc.Content
		for _, tc := range uc.ToolCalls {
			if tc.ID != "" {
				toolID = tc.ID
			}
			if tc.Name != "" {
				toolName = tc.Name
			}
			args += tc.ArgsPart
		}
		if uc.FinishReason != "" {
			finish = uc.FinishReason
		}
		if uc.Usage != nil {
			u := *uc.Usage
			usage = &u
		}
	}
	if role != "assistant" {
		t.Fatalf("缺少 role 首块: %q", role)
	}
	if content != "Hello" {
		t.Fatalf("文本增量拼接错误: %q", content)
	}
	if toolID != "tu_s" || toolName != "search" || args != `{"q":` {
		t.Fatalf("工具增量错误: id=%q name=%q args=%q", toolID, toolName, args)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish_reason 错误: %q", finish)
	}
	if usage == nil || usage.PromptTokens != 9 || usage.CompletionTokens != 4 {
		t.Fatalf("usage 错误: %+v", usage)
	}
	if !done {
		t.Fatal("未收到 Done 块")
	}

	// 统一块序列化成 OpenAI chunk
	frame := hub.ChunkToOpenAISSE(&hub.UnifiedChunk{ID: "msg_s", Model: "claude-s", Content: "X"})
	mustContain(t, string(frame), `"object":"chat.completion.chunk"`)
	mustContain(t, string(frame), `"delta"`)
	mustContain(t, string(hub.ChunkToOpenAISSE(&hub.UnifiedChunk{Done: true})), "[DONE]")
}

func TestAdapterOpenAIStreamAndAnthropicWriter(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"A"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"content":"B"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	it := hub.OpenAIStream(strings.NewReader(sse))
	var content string
	var finish string
	var usage *hub.Usage
	done := false
	for i := 0; i < 20; i++ {
		uc := it.Next()
		if uc == nil {
			break
		}
		if uc.Done {
			done = true
			break
		}
		content += uc.Content
		if uc.FinishReason != "" {
			finish = uc.FinishReason
		}
		if uc.Usage != nil {
			u := *uc.Usage
			usage = &u
		}
	}
	if content != "AB" || finish != "stop" || !done {
		t.Fatalf("OpenAI 流解析错误: content=%q finish=%q done=%v", content, finish, done)
	}
	if usage == nil || usage.PromptTokens != 3 || usage.CompletionTokens != 2 {
		t.Fatalf("usage 错误: %+v", usage)
	}

	// 统一块 -> Anthropic SSE 事件序列
	w := hub.NewAnthropicStreamWriter()
	var b strings.Builder
	b.Write(w.Write(&hub.UnifiedChunk{ID: "msg_w", Model: "m", Role: "assistant"}))
	b.Write(w.Write(&hub.UnifiedChunk{Content: "Hi"}))
	b.Write(w.Write(&hub.UnifiedChunk{FinishReason: "stop", Usage: &hub.Usage{CompletionTokens: 1}}))
	b.Write(w.Write(&hub.UnifiedChunk{Done: true}))
	out := b.String()
	for _, ev := range []string{"event: message_start", "event: content_block_start", "event: content_block_delta", "text_delta", "event: message_delta", "stop_reason", "event: message_stop"} {
		mustContain(t, out, ev)
	}
}

func TestAdapterCanonicalizeIncoming(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}],"system":"S"}`)
	canonical, want, err := hub.CanonicalizeIncoming("/v1/messages", body)
	if err != nil {
		t.Fatal(err)
	}
	if want != "anthropic" {
		t.Fatalf("wantFormat 错误: %q", want)
	}
	m := asMap(t, canonical)
	if asSlice(t, m["messages"])[0].(map[string]interface{})["role"] != "system" {
		t.Fatal("/v1/messages 未规范化为 OpenAI 格式")
	}
}
