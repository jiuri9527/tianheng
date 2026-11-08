package tests

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-hub/src/hub"
)

func sseStream() string {
	return strings.Join([]string{
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"liquid/lfm-3-1.7b","choices":[{"index":0,"delta":{"role":"assistant","content":"HE"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"liquid/lfm-3-1.7b","choices":[{"index":0,"delta":{"content":"LLO"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
}

// 通过注入的 kernel.StreamPipe 做零缓冲 SSE 转发：逐块 Flush、[DONE] 透传。
func TestRelayForwardSSEZeroCopy(t *testing.T) {
	rec := httptest.NewRecorder()
	err := hub.ForwardSSE(context.Background(), rec, strings.NewReader(sseStream()))
	if err != nil {
		t.Fatalf("ForwardSSE: %v", err)
	}
	body := rec.Body.String()
	mustContain(t, body, `"content":"HE"`)
	mustContain(t, body, `"content":"LLO"`)
	mustContain(t, body, "data: [DONE]")
	if !rec.Flushed {
		t.Fatal("SSE 必须逐块 Flush（httptest.ResponseRecorder.Flushed 应为 true）")
	}
}

func TestRelayForwardSSECtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	err := hub.ForwardSSE(ctx, rec, strings.NewReader(sseStream()))
	if err == nil {
		t.Fatal("ctx 取消时应立即返回错误")
	}
}

func TestRelayUpstreamErrorFrame(t *testing.T) {
	// 上游中途错误帧应作为 SSE error 事件注入，不静默吞掉
	sse := "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"upstream exploded\"}}\n\n"
	rec := httptest.NewRecorder()
	_ = hub.ForwardSSE(context.Background(), rec, strings.NewReader(sse))
	mustContain(t, rec.Body.String(), "upstream exploded")
}

// 端到端：真实 SSE 上游 -> 反代理 -> 客户端，/v1/chat/completions stream=true。
func TestRelayFullSSEViaAPI(t *testing.T) {
	srv, _ := newServer(t, mockUpstream(sseStream(), true))
	jwt, sk := bootstrap(t, srv)
	createChannel(t, srv, jwt)

	body := `{"model":"liquid/lfm-3-1.7b","messages":[{"role":"user","content":"hi"}],"stream":true}`
	rec := do(srv, "POST", "/v1/chat/completions", body, "Bearer "+sk)
	if rec.Code != 200 {
		t.Fatalf("流式转发失败: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type 应为 text/event-stream: %q", ct)
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatal("缺少 X-Accel-Buffering: no")
	}
	out := rec.Body.String()
	mustContain(t, out, `"content":"HE"`)
	mustContain(t, out, `"content":"LLO"`)
	mustContain(t, out, "data: [DONE]")
	if !rec.Flushed {
		t.Fatal("客户端侧未逐块 Flush")
	}
}

// 端到端：非流式 + /v1/messages 跨协议适配（Claude 格式 -> OpenAI 兼容上游 -> Claude 格式响应）。
func TestRelayNonStreamAndCrossProtocol(t *testing.T) {
	chat := `{"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"liquid/lfm-3-1.7b",
	  "choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],
	  "usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
	srv, _ := newServer(t, mockUpstream(chat, false))
	jwt, sk := bootstrap(t, srv)
	createChannel(t, srv, jwt)

	// OpenAI 风格
	rec := do(srv, "POST", "/v1/chat/completions",
		`{"model":"liquid/lfm-3-1.7b","messages":[{"role":"user","content":"hi"}],"stream":false}`, "Bearer "+sk)
	if rec.Code != 200 {
		t.Fatalf("chat/completions 失败: %d %s", rec.Code, rec.Body.String())
	}
	m := decode(t, rec)
	if m["object"] != "chat.completion" {
		t.Fatalf("响应格式错误: %v", m)
	}
	choices := asSlice(t, m["choices"])
	msg := asStringMap(t, asStringMap(t, choices[0])["message"])
	if msg["content"] != "OK" {
		t.Fatalf("未转发真实上游输出: %v", msg)
	}

	// Anthropic Messages 风格入口指向同一 OpenAI 兼容上游
	rec = do(srv, "POST", "/v1/messages",
		`{"model":"liquid/lfm-3-1.7b","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`, "Bearer "+sk)
	if rec.Code != 200 {
		t.Fatalf("/v1/messages 跨协议失败: %d %s", rec.Code, rec.Body.String())
	}
	am := decode(t, rec)
	if am["type"] != "message" || am["stop_reason"] != "end_turn" {
		t.Fatalf("Anthropic 响应格式错误: %v", am)
	}
	content := asSlice(t, am["content"])
	if asStringMap(t, content[0])["text"] != "OK" {
		t.Fatalf("跨协议输出错误: %v", content)
	}
}
