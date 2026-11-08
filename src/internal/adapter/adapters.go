package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// ---------- 真实上游 /models 解析（参数补全只取自原始响应对象） ----------

// discoverParams 从真实上游原始对象提取参数；上游未返回的字段保持零值，绝不编造。
func discoverParams(raw json.RawMessage, dm *DiscoveredModel) {
	m, err := asRawMap(raw)
	if err != nil {
		return
	}
	if v, ok := m["context_length"]; ok {
		_ = json.Unmarshal(v, &dm.ContextLength)
	}
	for _, k := range []string{"max_completion_tokens", "max_tokens", "max_output_tokens"} {
		if v, ok := m[k]; ok {
			_ = json.Unmarshal(v, &dm.MaxOutputTokens)
			if dm.MaxOutputTokens > 0 {
				break
			}
		}
	}
	if v, ok := m["top_provider"]; ok {
		var tp map[string]json.RawMessage
		_ = json.Unmarshal(v, &tp)
		if dm.MaxOutputTokens == 0 {
			_ = json.Unmarshal(tp["max_completion_tokens"], &dm.MaxOutputTokens)
		}
		if dm.ContextLength == 0 {
			_ = json.Unmarshal(tp["context_length"], &dm.ContextLength)
		}
	}
	if v, ok := m["architecture"]; ok {
		var arch map[string]json.RawMessage
		_ = json.Unmarshal(v, &arch)
		_ = json.Unmarshal(arch["input_modalities"], &dm.InputModalities)
		_ = json.Unmarshal(arch["output_modalities"], &dm.OutputModalities)
		for _, mod := range dm.InputModalities {
			if strings.Contains(mod, "image") || strings.Contains(mod, "video") {
				dm.SupportsVision = true
			}
		}
	}
	if v, ok := m["pricing"]; ok {
		var pr map[string]json.RawMessage
		_ = json.Unmarshal(v, &pr)
		var ps, cs string
		_ = json.Unmarshal(pr["prompt"], &ps)
		_ = json.Unmarshal(pr["completion"], &cs)
		// 上游定价不可信：NaN/Inf/负数/异常量级会污染计费并使落盘 JSON 编码失败。
		if p, err := strconv.ParseFloat(ps, 64); err == nil && !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 && p < 1 {
			dm.PromptPrice = p
		}
		if p, err := strconv.ParseFloat(cs, 64); err == nil && !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 && p < 1 {
			dm.CompletionPrice = p
		}
	}
	if v, ok := m["supported_parameters"]; ok {
		var params []string
		_ = json.Unmarshal(v, &params)
		for _, p := range params {
			switch p {
			case "tools", "tool_choice":
				dm.SupportsTools = true
			case "stream":
				dm.SupportsStream = true
			case "reasoning", "include_reasoning":
				dm.SupportsReasoning = true
			}
		}
	}
}

// parseModelsList 解析 {"data":[...]} 通用结构，id 字段名可指定（openai/anthropic 均为 id）。
func parseModelsList(status int, body []byte, nameFromDisplay bool) ([]DiscoveredModel, error) {
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("上游 /models 返回 HTTP %d", status)
	}
	env, err := asRawMap(body)
	if err != nil {
		return nil, fmt.Errorf("上游 /models 响应不是 JSON 对象: %w", err)
	}
	dataRaw, ok := env["data"]
	if !ok {
		return nil, fmt.Errorf("上游 /models 响应缺少 data 字段")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(dataRaw, &items); err != nil {
		return nil, fmt.Errorf("上游 /models data 不是数组: %w", err)
	}
	out := make([]DiscoveredModel, 0, len(items))
	for _, item := range items {
		obj, err := asRawMap(item)
		if err != nil {
			continue
		}
		id, ok := strField(obj, "id")
		if !ok || id == "" {
			continue
		}
		name := id
		if nameFromDisplay {
			if dn, ok := strField(obj, "display_name"); ok && dn != "" {
				name = dn
			}
		}
		dm := DiscoveredModel{ID: id, Name: name, Raw: item, SupportsStream: true}
		discoverParams(item, &dm)
		out = append(out, dm)
	}
	return out, nil
}

// RawRequest 不做任何协议转换，把 body 逐字发到指定上游路径（embeddings/images/audio/responses 等能力位）。
func RawRequest(cx ChannelCtx, body []byte, path string) (string, http.Header, []byte, error) {
	if path == "" {
		path = "/chat/completions"
	}
	return joinURL(cx.BaseURI, path), authHeaders(cx), body, nil
}

// ---------- OpenAICompatAdapter（openai / xai / 默认） ----------

// OpenAICompatAdapter 透传 OpenAI 兼容上游（保留 grok reasoning_content/live_search 差异字段）。
type OpenAICompatAdapter struct{}

func (a *OpenAICompatAdapter) Name() string       { return "openai_compat" }
func (a *OpenAICompatAdapter) IsAnthropic() bool  { return false }

func (a *OpenAICompatAdapter) ModelsRequest(cx ChannelCtx) (string, http.Header, error) {
	path := cx.ModelsPath
	if path == "" {
		path = "/models"
	}
	return joinURL(cx.BaseURI, path), authHeaders(cx), nil
}

func (a *OpenAICompatAdapter) ParseModels(status int, body []byte) ([]DiscoveredModel, error) {
	return parseModelsList(status, body, false)
}

func (a *OpenAICompatAdapter) ChatRequest(cx ChannelCtx, canonical []byte) (string, http.Header, []byte, error) {
	path := cx.ChatPath
	if path == "" {
		path = "/chat/completions"
	}
	return joinURL(cx.BaseURI, path), authHeaders(cx), canonical, nil
}

func (a *OpenAICompatAdapter) ResponseToCanonical(status int, body []byte) ([]byte, error) {
	return body, nil // 已是 OpenAI 规范
}

func (a *OpenAICompatAdapter) StreamIterator(up io.Reader) StreamIterator {
	return &openAIStreamIterator{sc: newSSEScanner(up)}
}

// ---------- AnthropicAdapter（双向转换） ----------

// AnthropicAdapter Anthropic /messages 适配器。
type AnthropicAdapter struct{}

func (a *AnthropicAdapter) Name() string      { return "anthropic" }
func (a *AnthropicAdapter) IsAnthropic() bool { return true }

func (a *AnthropicAdapter) ModelsRequest(cx ChannelCtx) (string, http.Header, error) {
	path := cx.ModelsPath
	if path == "" {
		path = "/models"
	}
	return joinURL(cx.BaseURI, path), authHeaders(cx), nil
}

func (a *AnthropicAdapter) ParseModels(status int, body []byte) ([]DiscoveredModel, error) {
	return parseModelsList(status, body, true)
}

func (a *AnthropicAdapter) ChatRequest(cx ChannelCtx, canonical []byte) (string, http.Header, []byte, error) {
	path := cx.ChatPath
	if path == "" {
		path = "/messages"
	}
	body, err := OpenAIToAnthropicRequest(canonical)
	if err != nil {
		return "", nil, nil, err
	}
	return joinURL(cx.BaseURI, path), authHeaders(cx), body, nil
}

func (a *AnthropicAdapter) ResponseToCanonical(status int, body []byte) ([]byte, error) {
	return AnthropicToOpenAIResponse(status, body)
}

func (a *AnthropicAdapter) StreamIterator(up io.Reader) StreamIterator {
	return &anthropicStreamIterator{
		sc: newSSEScanner(up), blockType: map[int]string{}, toolIndex: map[int]int{},
	}
}

// ---------- GeminiAdapter（OpenAI 兼容层） ----------

// GeminiAdapter 走 Gemini 的 OpenAI 兼容层。原生 generateContent 为可选分支，
// 这里统一走 /chat/completions 兼容路径以保证聚合出口一致。
type GeminiAdapter struct{ OpenAICompatAdapter }

func (a *GeminiAdapter) Name() string { return "gemini" }

// ---------- OpenAICompatibleAdapter（自定义路径/自定义 header 变体） ----------

// OpenAICompatibleAdapter 支持 models_path / chat_path / custom_headers 覆盖。
type OpenAICompatibleAdapter struct{ OpenAICompatAdapter }

func (a *OpenAICompatibleAdapter) Name() string { return "openai_compatible" }

func (a *OpenAICompatibleAdapter) ModelsRequest(cx ChannelCtx) (string, http.Header, error) {
	path := cx.ModelsPath
	if path == "" {
		path = "/models"
	}
	return joinURL(cx.BaseURI, path), authHeaders(cx), nil
}

func (a *OpenAICompatibleAdapter) ChatRequest(cx ChannelCtx, canonical []byte) (string, http.Header, []byte, error) {
	path := cx.ChatPath
	if path == "" {
		path = "/chat/completions"
	}
	return joinURL(cx.BaseURI, path), authHeaders(cx), canonical, nil
}
