// Package discovery · upstream.go
// 真实上游 GET {base_uri}/models 的完整解析。所有字段逐字来自上游响应对象，
// 绝不内置能力表、绝不编造任何模型 id / 名称 / 参数 / 价格 / 能力位。
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"llm-hub/src/internal/adapter"
	"llm-hub/src/internal/store"
)

// AutoModel 是真实上游 /models 响应中一条模型记录的完整投影。
// Raw 保留逐字原始对象，供前端「参数卡片」渲染任意上游字段。
type AutoModel struct {
	ID                  string
	Name                string
	CanonicalSlug       string
	Created             int64
	Description         string
	OwnedBy             string
	ContextLength       int
	MaxOutputTokens     int
	Modality            string
	InputModalities     []string
	OutputModalities    []string
	PromptPrice         float64
	CompletionPrice     float64
	SupportedParameters []string
	SupportsVision      bool
	SupportsTools       bool
	SupportsStream      bool
	SupportsReasoning   bool
	TopProvider         json.RawMessage
	Pricing             json.RawMessage
	Architecture        json.RawMessage
	Raw                 json.RawMessage
}

// isAnthropic 判定渠道是否走 Anthropic 鉴权风格（x-api-key + anthropic-version）。
func isAnthropic(cx adapter.ChannelCtx) bool {
	return cx.ProviderType == "anthropic" || cx.AuthStyle == "x-api-key"
}

// autoModelString 从原始对象取一个字符串字段，缺失/类型不符返回 ""，绝不猜测。
func autoModelString(m map[string]json.RawMessage, key string) string {
	raw, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func autoModelInt(m map[string]json.RawMessage, key string) int {
	raw, ok := m[key]
	if !ok {
		return 0
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0
	}
	return n
}

// safePrice 校验上游定价：NaN/Inf/负数/异常量级一律不采信（污染计费与落盘 JSON）。
func safePrice(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f >= 1 {
		return 0
	}
	return f
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// parseAutoModel 解析单条真实上游对象。返回 false 表示该条无合法 id，直接丢弃。
func parseAutoModel(item json.RawMessage, anthropic bool) (AutoModel, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(item, &m); err != nil {
		return AutoModel{}, false
	}
	id := autoModelString(m, "id")
	if strings.TrimSpace(id) == "" {
		return AutoModel{}, false
	}
	a := AutoModel{
		ID:            id,
		Name:          firstNonEmpty(autoModelString(m, "name"), autoModelString(m, "display_name"), id),
		CanonicalSlug: autoModelString(m, "canonical_slug"),
		Created:       int64(autoModelInt(m, "created")),
		Description:   autoModelString(m, "description"),
		OwnedBy:       autoModelString(m, "owned_by"),
		ContextLength: autoModelInt(m, "context_length"),
		TopProvider:   m["top_provider"],
		Pricing:       m["pricing"],
		Architecture:  m["architecture"],
		Raw:           item,
	}
	if anthropic {
		// Anthropic /models 的显示名在 display_name 字段
		a.Name = firstNonEmpty(autoModelString(m, "display_name"), autoModelString(m, "name"), id)
	}
	if a.OwnedBy == "" && strings.Contains(id, "/") {
		a.OwnedBy = id[:strings.IndexByte(id, '/')]
	}

	// max_completion_tokens / max_tokens / max_output_tokens / top_provider.max_completion_tokens
	for _, k := range []string{"max_completion_tokens", "max_tokens", "max_output_tokens"} {
		if n := autoModelInt(m, k); n > 0 {
			a.MaxOutputTokens = n
			break
		}
	}
	if len(a.TopProvider) > 0 {
		var tp map[string]json.RawMessage
		if err := json.Unmarshal(a.TopProvider, &tp); err == nil {
			if a.MaxOutputTokens == 0 {
				a.MaxOutputTokens = autoModelInt(tp, "max_completion_tokens")
			}
			if a.ContextLength == 0 {
				a.ContextLength = autoModelInt(tp, "context_length")
			}
		}
	}
	if len(a.Architecture) > 0 {
		var arch map[string]json.RawMessage
		if err := json.Unmarshal(a.Architecture, &arch); err == nil {
			a.Modality = autoModelString(arch, "modality")
			_ = json.Unmarshal(arch["input_modalities"], &a.InputModalities)
			_ = json.Unmarshal(arch["output_modalities"], &a.OutputModalities)
			for _, mod := range a.InputModalities {
				if strings.Contains(mod, "image") || strings.Contains(mod, "video") {
					a.SupportsVision = true
				}
			}
		}
	}
	if len(a.Pricing) > 0 {
		var pr map[string]json.RawMessage
		if err := json.Unmarshal(a.Pricing, &pr); err == nil {
			a.PromptPrice = safePrice(autoModelString(pr, "prompt"))
			a.CompletionPrice = safePrice(autoModelString(pr, "completion"))
		}
	}
	if v, ok := m["supported_parameters"]; ok && len(v) > 0 {
		_ = json.Unmarshal(v, &a.SupportedParameters)
		for _, p := range a.SupportedParameters {
			switch p {
			case "tools", "tool_choice":
				a.SupportsTools = true
			case "stream":
				a.SupportsStream = true
			case "reasoning", "include_reasoning":
				a.SupportsReasoning = true
			}
		}
	}
	return a, true
}

// parseUpstreamModels 解析 {"data":[...]} / {"models":[...]} / 裸数组三种真实响应。
// 非 2xx、非 JSON、空数组都不产生任何占位模型；空数组返回空列表与 nil error。
func parseUpstreamModels(body []byte, status int, anthropic bool) ([]AutoModel, error) {
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("上游 /models 返回 HTTP %d: %s", status, trunc(string(body), 300))
	}
	var items []json.RawMessage
	var env map[string]json.RawMessage
	if err := json.Unmarshal(body, &env); err == nil {
		dataRaw, ok := env["data"]
		if !ok {
			dataRaw, ok = env["models"]
		}
		if ok {
			if err := json.Unmarshal(dataRaw, &items); err != nil {
				return nil, fmt.Errorf("上游 /models data 不是数组: %w", err)
			}
		} else {
			// 对象但没有 data/models：尝试整体按数组解析
			if err := json.Unmarshal(body, &items); err != nil {
				return nil, fmt.Errorf("上游 /models 响应缺少 data 字段且不是数组: %s", trunc(string(body), 300))
			}
		}
	} else if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("上游 /models 响应不是 JSON: %w", err)
	}
	out := make([]AutoModel, 0, len(items))
	for _, item := range items {
		if a, ok := parseAutoModel(item, anthropic); ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// fetchModels 用该渠道的 api_key 对真实上游发起一次 GET /models。
// OpenAI 兼容系：Authorization: Bearer <key>；
// Anthropic：x-api-key: <key> + anthropic-version: 2023-06-01。
func (d *Discoverer) fetchModels(ctx context.Context, ch *store.Channel) ([]AutoModel, error) {
	cx, err := d.channelCtx(ch)
	if err != nil {
		return nil, err
	}
	ad := adapter.ForProvider(ch.ProviderType)
	url, headers, err := ad.ModelsRequest(cx)
	if err != nil {
		return nil, err
	}
	if isAnthropic(cx) {
		headers.Set("x-api-key", cx.PlainKey)
		ver := cx.APIVersion
		if ver == "" {
			ver = adapter.AnthropicVersion // 2023-06-01
		}
		headers.Set("anthropic-version", ver)
	}
	headers.Set("Accept", "application/json")
	status, body, err := d.do(ctx, http.MethodGet, url, headers, nil)
	if err != nil {
		return nil, fmt.Errorf("请求上游 /models 失败: %w", err)
	}
	return parseUpstreamModels(body, status, isAnthropic(cx))
}
