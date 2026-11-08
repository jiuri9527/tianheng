// Package adapter 定义统一内部结构体与 ProviderAdapter 接口，并实现
// OpenAI 兼容 / Anthropic / Gemini / 自定义 四种适配器，含 F7 要求的
// OpenAI↔Anthropic 请求/响应/流式双向转换与状态机。
package adapter

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// 上游真实端点常量（贡献渠道时的下拉提示来源），逐字来自需求文档。
const (
	OpenAIBaseURI    = "https://api.openai.com/v1"
	XAIBaseURI       = "https://api.x.ai/v1"
	AnthropicBaseURI = "https://api.anthropic.com/v1"
	GeminiBaseURI    = "https://generativelanguage.googleapis.com/v1beta/openai"
	AnthropicVersion = "2023-06-01"
)

// ProviderBaseURI 返回供应商类型的默认 base_uri。
func ProviderBaseURI(providerType string) string {
	switch providerType {
	case "openai":
		return OpenAIBaseURI
	case "xai":
		return XAIBaseURI
	case "anthropic":
		return AnthropicBaseURI
	case "gemini":
		return GeminiBaseURI
	default:
		return ""
	}
}

// AuthStyle 返回供应商默认鉴权风格。
func AuthStyle(providerType string) string {
	if providerType == "anthropic" {
		return "x-api-key"
	}
	return "bearer"
}

// ChannelCtx 是已解析的渠道连接上下文（明文密钥仅在内存中使用，绝不落库/回显）。
type ChannelCtx struct {
	BaseURI       string
	PlainKey      string
	ProviderType  string
	AuthStyle     string
	APIVersion    string
	CustomHeaders map[string]string
	ModelsPath    string
	ChatPath      string
}

// DiscoveredModel 是从真实上游 GET /models 响应解析得到的模型，
// Raw 保留逐字原始对象，参数只来自该对象，绝不编造。
type DiscoveredModel struct {
	ID               string
	Name             string
	Raw              json.RawMessage
	ContextLength    int
	MaxOutputTokens  int
	InputModalities  []string
	OutputModalities []string
	PromptPrice      float64
	CompletionPrice  float64
	SupportsTools    bool
	SupportsVision   bool
	SupportsStream   bool
	SupportsReasoning bool
}

// Usage 统一用量。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// UnifiedRequest 统一请求（内部规范格式，所有适配器只与此结构打交道）。
type UnifiedRequest struct {
	Model       string          `json:"model"`
	Messages    json.RawMessage `json:"messages"`
	Stream      bool            `json:"stream"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Raw         json.RawMessage `json:"-"` // 原始 OpenAI 规范 body，用于透传
}

// UnifiedResponse 统一响应（OpenAI chat.completion 规范）。
type UnifiedResponse struct {
	ID               string          `json:"id"`
	Object           string          `json:"object"`
	Created          int64           `json:"created"`
	Model            string          `json:"model"`
	Choices          json.RawMessage `json:"choices"`
	Usage            *Usage          `json:"usage"`
	PromptTokens     int
	CompletionTokens int
}

// ChunkToolCall 流式增量工具调用。
type ChunkToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Name     string `json:"name,omitempty"`
	ArgsPart string `json:"args_part,omitempty"`
}

// UnifiedChunk 统一流式块，所有适配器的 StreamIterator 都产出它。
type UnifiedChunk struct {
	ID           string
	Model        string
	Role         string // 首个 assistant 角色块
	Content      string
	ToolCalls    []ChunkToolCall
	FinishReason string
	Usage        *Usage
	Raw          string // OpenAI 兼容上游的原始 data 载荷，用于逐字透传（保留 grok reasoning_content 等）
	Done         bool
	Err          error
}

// StreamIterator 产出统一块；Next 返回 nil 表示流结束。
type StreamIterator interface {
	Next() *UnifiedChunk
}

// ProviderAdapter 所有供应商适配器实现的接口。
type ProviderAdapter interface {
	Name() string
	IsAnthropic() bool
	ModelsRequest(cx ChannelCtx) (url string, headers http.Header, err error)
	ParseModels(status int, body []byte) ([]DiscoveredModel, error)
	ChatRequest(cx ChannelCtx, canonicalOpenAIBody []byte) (url string, headers http.Header, body []byte, err error)
	ResponseToCanonical(status int, body []byte) ([]byte, error)
	StreamIterator(up io.Reader) StreamIterator
}

// ForProvider 依据供应商类型返回适配器。
func ForProvider(providerType string) ProviderAdapter {
	switch providerType {
	case "anthropic":
		return &AnthropicAdapter{}
	case "gemini":
		return &GeminiAdapter{}
	case "openai_compatible":
		return &OpenAICompatibleAdapter{}
	default: // openai / xai / 其它一律 OpenAI 兼容
		return &OpenAICompatAdapter{}
	}
}

// joinURL 拼接 base_uri 与 path，避免双斜杠。
func joinURL(base, path string) string {
	b := strings.TrimRight(base, "/")
	if path == "" {
		return b
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return b + path
}

// authHeaders 依据鉴权风格注入上游所需 header，并合并自定义 header。
func authHeaders(cx ChannelCtx) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	if cx.AuthStyle == "x-api-key" {
		h.Set("x-api-key", cx.PlainKey)
		ver := cx.APIVersion
		if ver == "" {
			ver = AnthropicVersion
		}
		h.Set("anthropic-version", ver)
	} else {
		h.Set("Authorization", "Bearer "+cx.PlainKey)
	}
	// 自定义 header 不得覆盖鉴权/传输关键头，且不得含 CR/LF/NUL（防头注入与日志注入）。
	for k, v := range cx.CustomHeaders {
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "" || forbiddenHeader(lk) || strings.ContainsAny(k+v, "\r\n\x00") {
			continue
		}
		h.Set(k, v)
	}
	return h
}

// forbiddenHeader 列出不允许由渠道贡献者覆盖的 hop-by-hop 与鉴权头。
func forbiddenHeader(k string) bool {
	switch k {
	case "authorization", "x-api-key", "anthropic-version", "host", "content-length",
		"transfer-encoding", "connection", "proxy-authorization", "cookie", "accept":
		return true
	}
	return false
}

// asRawMap 把 JSON 对象解析为 map；非对象返回错误。
func asRawMap(b []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
