// Package relay 实现 F6 反代理与流式 SSE 零缓冲转发：重写上游鉴权头、透传 body、
// 逐 data 块 Flush、ctx 取消关闭上游、[DONE]、Anthropic 多行 event、统一错误包装。
package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"llm-hub/kernel"
	"llm-hub/src/internal/adapter"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/crypto"
	"llm-hub/src/internal/ssrf"
	"llm-hub/src/internal/store"
)

// Doer 是可注入的 HTTP 执行器（与 discovery.Doer 结构相同）。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Attempt 是一次上游调用的完整构造结果（明文密钥仅在内存）。
type Attempt struct {
	Channel *store.Channel
	Adapter adapter.ProviderAdapter
	URL     string
	Headers http.Header
	Body    []byte
}

// Outcome 是转发结果，供 api 计量/日志使用。
type Outcome struct {
	StatusCode       int
	Stream           bool
	PromptTokens     int
	CompletionTokens int
	TTFTMs           int
	LatencyMs        int
	OutputText       string
	// RawCanonical 是上游响应的规范化 JSON 原文（非流式）。用于解析完整 usage
	// （含缓存字段）—— OutputText 只是抽取出来的正文，不含 usage。
	RawCanonical []byte
	UpstreamError    string
	Err              error
}

// Relay 反代理。
type Relay struct {
	St         *store.Store
	Cipher     *crypto.Cipher
	Cfg        *config.Config
	Doer       Doer
	StreamPipe kernel.StreamPipe // 通过接口注入团队5/rt 的实现
}

// New 构造反代理，默认使用带超时的 http.Client；StreamPipe 需由装配层注入。
func New(st *store.Store, cipher *crypto.Cipher, cfg *config.Config) *Relay {
	return &Relay{
		St: st, Cipher: cipher, Cfg: cfg,
		// 反代理出站同样必须防 DNS rebinding 与内网重定向。
		Doer: ssrf.SafeClient(180*time.Second, nil),
	}
}

func (r *Relay) SetDoer(d Doer) *Relay                       { r.Doer = d; return r }
func (r *Relay) SetStreamPipe(sp kernel.StreamPipe) *Relay   { r.StreamPipe = sp; return r }

func (r *Relay) clientTimeout() time.Duration {
	if r.Cfg != nil && r.Cfg.DefaultTimeout > 0 {
		return r.Cfg.DefaultTimeout
	}
	return 180 * time.Second
}

// BuildAttempt 解密 api_key、选择适配器、构造上游请求（含 OpenAI↔Anthropic 转换）。
func (r *Relay) BuildAttempt(ch *store.Channel, canonicalBody []byte) (*Attempt, error) {	key, err := r.Cipher.Decrypt(ch.APIKeyCipher, ch.APIKeyIV, ch.APIKeyTag)
	if err != nil {
		return nil, fmt.Errorf("渠道 %s api_key 解密失败: %w", ch.ID, err)
	}
	cx := adapter.ChannelCtx{
		BaseURI: ch.BaseURI, PlainKey: string(key), ProviderType: ch.ProviderType,
		AuthStyle: ch.AuthStyle, APIVersion: ch.APIVersion,
		CustomHeaders: ch.CustomHeaders, ModelsPath: ch.ModelsPath, ChatPath: ch.ChatPath,
	}
	ad := adapter.ForProvider(ch.ProviderType)
	url, headers, body, err := ad.ChatRequest(cx, canonicalBody)
	if err != nil {
		return nil, err
	}
	return &Attempt{Channel: ch, Adapter: ad, URL: url, Headers: headers, Body: body}, nil
}

// BuildRawAttempt 为 embeddings/images/audio/responses 等能力位构造不做协议转换的上游请求。
func (r *Relay) BuildRawAttempt(ch *store.Channel, body []byte, path string) (*Attempt, error) {
	key, err := r.Cipher.Decrypt(ch.APIKeyCipher, ch.APIKeyIV, ch.APIKeyTag)
	if err != nil {
		return nil, fmt.Errorf("渠道 %s api_key 解密失败: %w", ch.ID, err)
	}
	cx := adapter.ChannelCtx{
		BaseURI: ch.BaseURI, PlainKey: string(key), ProviderType: ch.ProviderType,
		AuthStyle: ch.AuthStyle, APIVersion: ch.APIVersion,
		CustomHeaders: ch.CustomHeaders, ModelsPath: ch.ModelsPath, ChatPath: path,
	}
	url, headers, outBody, err := adapter.RawRequest(cx, body, path)
	if err != nil {
		return nil, err
	}
	return &Attempt{Channel: ch, Adapter: adapter.ForProvider("openai"),
		URL: url, Headers: headers, Body: outBody}, nil
}

// Send 发起一次上游调用，返回原始响应（body 未读），供 api 判定 failover。
func (r *Relay) Send(ctx context.Context, a *Attempt) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL, bytes.NewReader(a.Body))
	if err != nil {
		return nil, err
	}
	for k, vals := range a.Headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	// 流式请求声明接受 SSE
	if strings.Contains(string(a.Body), `"stream":true`) || strings.Contains(string(a.Body), `"stream": true`) {
		req.Header.Set("Accept", "text/event-stream")
	}
	return r.Doer.Do(req)
}

// ForwardRawSSE 通过注入的 kernel.StreamPipe 做零拷贝 SSE 转发。
func (r *Relay) ForwardRawSSE(ctx context.Context, w http.ResponseWriter, up io.Reader) error {
	if r.StreamPipe == nil {
		return fmt.Errorf("relay: StreamPipe 未注入")
	}
	return r.StreamPipe.Forward(ctx, w, up)
}

// Forward 把一次 2xx 上游响应转发给客户端（流式/非流式 + 双向协议转换 + ctx 取消）。
func (r *Relay) Forward(ctx context.Context, w http.ResponseWriter, resp *http.Response,
	a *Attempt, wantFormat string, isStream bool, start time.Time) *Outcome {

	// ctx 取消时立即关闭上游连接，解除 Read 阻塞
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			resp.Body.Close()
		case <-done:
		}
	}()
	defer close(done)

	out := &Outcome{StatusCode: http.StatusOK, Stream: isStream}
	if isStream {
		r.forwardStream(ctx, w, resp, a, wantFormat, start, out)
	} else {
		r.forwardNonStream(w, resp, a, wantFormat, start, out)
	}
	out.LatencyMs = int(time.Since(start).Milliseconds())
	return out
}

func (r *Relay) forwardNonStream(w http.ResponseWriter, resp *http.Response,
	a *Attempt, wantFormat string, start time.Time, out *Outcome) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		out.Err = err
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write(ErrorJSON("upstream_error", "upstream_read_error", err.Error(), ""))
		return
	}
	canonical := body
	if a.Adapter.IsAnthropic() {
		canonical, err = a.Adapter.ResponseToCanonical(resp.StatusCode, body)
		if err != nil {
			out.Err = err
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			w.Write(ErrorJSON("upstream_error", "upstream_parse_error", err.Error(), ""))
			return
		}
	}
	formatted, err := adapter.FormatResponse(wantFormat, resp.StatusCode, canonical)
	if err != nil {
		formatted = canonical
	}
	usage := adapter.ParseUsageFromCanonical(canonical)
	out.PromptTokens = usage.PromptTokens
	out.CompletionTokens = usage.CompletionTokens
	out.OutputText = extractText(canonical)
	out.RawCanonical = append([]byte(nil), canonical...)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(formatted)
}

func (r *Relay) forwardStream(ctx context.Context, w http.ResponseWriter, resp *http.Response,
	a *Attempt, wantFormat string, start time.Time, out *Outcome) {

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// 纯 OpenAI->OpenAI 流式：零拷贝透传（保留 grok reasoning_content），TeeReader 旁路计量。
	if !a.Adapter.IsAnthropic() && wantFormat == "openai" && r.StreamPipe != nil {
		cap := &cappedBuffer{cap: 4 << 20}
		tee := io.TeeReader(resp.Body, cap)
		err := r.ForwardRawSSE(ctx, w, tee)
		if err != nil && ctx.Err() == nil {
			out.Err = err
		}
		out.PromptTokens, out.CompletionTokens = parseSSEForUsage(cap.buf.Bytes())
		return
	}

	// 需要协议转换：经统一块迭代器 + 序列化状态机逐块 Flush。
	it := a.Adapter.StreamIterator(resp.Body)
	writer := adapter.NewAnthropicStreamWriter()
	firstContent := true
	promptTokens, completionTokens := 0, 0
	for {
		select {
		case <-ctx.Done():
			out.Err = ctx.Err()
			return
		default:
		}
		uc := it.Next()
		if uc == nil {
			break
		}
		if uc.Err != nil {
			out.Err = uc.Err
			// 注入一个上游错误帧（OpenAI 客户端）或 message_delta 错误（Anthropic 客户端）
			if wantFormat == "openai" {
				errObj, _ := json.Marshal(map[string]interface{}{
					"error": map[string]interface{}{"message": uc.Err.Error(), "type": "upstream_error"},
				})
				_, _ = w.Write([]byte("event: error\ndata: " + string(errObj) + "\n\n"))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			break
		}
		if uc.Done {
			break
		}
		var frame []byte
		if wantFormat == "openai" {
			frame = adapter.ChunkToOpenAISSE(uc)
		} else {
			frame = writer.Write(uc)
		}
		if len(frame) > 0 {
			if _, err := w.Write(frame); err != nil {
				out.Err = err
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if firstContent && (uc.Content != "" || len(uc.ToolCalls) > 0) {
			out.TTFTMs = int(time.Since(start).Milliseconds())
			firstContent = false
		}
		if uc.Usage != nil {
			promptTokens = uc.Usage.PromptTokens
			completionTokens = uc.Usage.CompletionTokens
		}
	}
	out.PromptTokens, out.CompletionTokens = promptTokens, completionTokens
	// 结束帧
	if wantFormat == "openai" {
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	} else {
		_, _ = w.Write(writer.Write(&adapter.UnifiedChunk{Done: true}))
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// ---------- 辅助 ----------

func extractText(canonical []byte) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &m); err != nil {
		return ""
	}
	var choices []map[string]json.RawMessage
	_ = json.Unmarshal(m["choices"], &choices)
	if len(choices) == 0 {
		return ""
	}
	var msg map[string]json.RawMessage
	_ = json.Unmarshal(choices[0]["message"], &msg)
	var text string
	_ = json.Unmarshal(msg["content"], &text)
	return text
}

// cappedBuffer 有上限的旁路缓冲，供 TeeReader 计量用（不阻塞客户端流）。
type cappedBuffer struct {
	buf bytes.Buffer
	cap int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len() >= c.cap {
		return len(p), nil
	}
	remaining := c.cap - c.buf.Len()
	if len(p) > remaining {
		c.buf.Write(p[:remaining])
		return len(p), nil
	}
	return c.buf.Write(p)
}

// parseSSEForUsage 从透传捕获的 SSE 字节里抽取上游 usage（估算兜底由 metering 负责）。
func parseSSEForUsage(data []byte) (pt, ct int) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimSuffix(sc.Text(), "\r"))
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			continue
		}
		var u map[string]json.RawMessage
		_ = json.Unmarshal(m["usage"], &u)
		var p, c int
		_ = json.Unmarshal(u["prompt_tokens"], &p)
		_ = json.Unmarshal(u["completion_tokens"], &c)
		if p > 0 {
			pt = p
		}
		if c > 0 {
			ct = c
		}
	}
	return pt, ct
}

// ---------- 统一错误体 ----------

type errorEnvelope struct {
	Error errorDetail `json:"error"`
}
type errorDetail struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Code      string `json:"code"`
	RequestID string `json:"request_id"`
}

// ErrorJSON 生成统一错误体 JSON。
func ErrorJSON(errType, code, message, requestID string) []byte {
	b, _ := json.Marshal(errorEnvelope{Error: errorDetail{
		Message: message, Type: errType, Code: code, RequestID: requestID,
	}})
	return b
}

// WriteJSONError 写统一错误响应。
func WriteJSONError(w http.ResponseWriter, status int, errType, code, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(ErrorJSON(errType, code, message, requestID))
}
