// Package discovery 实现 F3 真实模型参数发现与 F4 真实模型输入输出采集。
// 所有渠道模型必须来自真实上游 GET {base_uri}/models 与真实探测调用；
// 拉取失败时列表为空且 probe_status=unavailable，绝不填充占位/编造模型。
package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"llm-hub/src/internal/adapter"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/crypto"
	"llm-hub/src/internal/ssrf"
	"llm-hub/src/internal/store"
)

// Doer 是可注入的 HTTP 执行器，便于离线单元测试与真实联调共用同一套逻辑。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Discoverer 持有依赖。
type Discoverer struct {
	St     *store.Store
	Cipher *crypto.Cipher
	Cfg    *config.Config
	Doer   Doer

	probeMu sync.Mutex
	probing map[string]bool // channelID -> 后台真实探测进行中
}

// New 构造发现器，默认使用带超时的 http.Client。
func New(st *store.Store, cipher *crypto.Cipher, cfg *config.Config) *Discoverer {
	return &Discoverer{
		St: st, Cipher: cipher, Cfg: cfg,
		// 出站必须走 SSRF 安全客户端：拨号前 IP 校验 + 禁止内网重定向。
		Doer: ssrf.SafeClient(15*time.Second, nil),
	}
}

// SetDoer 注入自定义 HTTP 执行器（测试用）。
func (d *Discoverer) SetDoer(doer Doer) *Discoverer {
	d.Doer = doer
	return d
}

// channelCtx 解密 api_key 并组装适配器上下文。明文密钥仅在内存中使用。
func (d *Discoverer) channelCtx(ch *store.Channel) (adapter.ChannelCtx, error) {
	key, err := d.Cipher.Decrypt(ch.APIKeyCipher, ch.APIKeyIV, ch.APIKeyTag)
	if err != nil {
		return adapter.ChannelCtx{}, fmt.Errorf("渠道 %s api_key 解密失败: %w", ch.ID, err)
	}
	return adapter.ChannelCtx{
		BaseURI: ch.BaseURI, PlainKey: string(key), ProviderType: ch.ProviderType,
		AuthStyle: ch.AuthStyle, APIVersion: ch.APIVersion,
		CustomHeaders: ch.CustomHeaders, ModelsPath: ch.ModelsPath, ChatPath: ch.ChatPath,
	}, nil
}

func (d *Discoverer) do(ctx context.Context, method, url string, headers http.Header, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	for k, vals := range headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	resp, err := d.Doer.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}

// ListModels 对真实上游发起 GET /models，解析出真实模型清单。
// 任何失败（连接错误/非 2xx/解析错误）都返回空列表与错误，不产生任何占位模型。
func (d *Discoverer) ListModels(ctx context.Context, ch *store.Channel) ([]adapter.DiscoveredModel, error) {
	autos, err := d.fetchModels(ctx, ch)
	if err != nil {
		return nil, err
	}
	out := make([]adapter.DiscoveredModel, 0, len(autos))
	for _, a := range autos {
		out = append(out, adapter.DiscoveredModel{
			ID: a.ID, Name: a.Name, Raw: a.Raw,
			ContextLength: a.ContextLength, MaxOutputTokens: a.MaxOutputTokens,
			InputModalities: a.InputModalities, OutputModalities: a.OutputModalities,
			PromptPrice: a.PromptPrice, CompletionPrice: a.CompletionPrice,
			SupportsTools: a.SupportsTools, SupportsVision: a.SupportsVision,
			SupportsStream: a.SupportsStream, SupportsReasoning: a.SupportsReasoning,
		})
	}
	return out, nil
}

// SyncResult 是一次真实同步的结果。
type SyncResult struct {
	OK         bool
	Models     []*store.Model // 拉取失败时为空
	Discovered int
	Error      string
}

// SyncChannel 执行 F3：真实 GET /models -> 完整参数解析 -> 增量 upsert models/aliases。
// 拉取失败时清空该渠道模型、返回空列表、标记 unavailable，绝不编造。
// 成功但尚无真实探测样例的模型保持 probe_status=unavailable。
func (d *Discoverer) SyncChannel(ctx context.Context, ch *store.Channel) SyncResult {
	autos, err := d.fetchModels(ctx, ch)
	if err != nil {
		d.St.DeleteModelsByChannel(ch.ID)
		d.St.UpdateChannelLocked(ch.ID, func(c *store.Channel) {
			c.ProbeStatus = "unavailable"
			c.LastError = err.Error()
			c.HealthCheckedAt = time.Now().Unix()
		})
		return SyncResult{OK: false, Models: []*store.Model{}, Error: err.Error()}
	}
	if len(autos) == 0 {
		// 上游返回空 data 数组：列表保持为空，标记 unavailable，不填充任何占位模型。
		d.St.DeleteModelsByChannel(ch.ID)
		d.St.UpdateChannelLocked(ch.ID, func(c *store.Channel) {
			c.ProbeStatus = "unavailable"
			c.LastError = ""
			c.HealthCheckedAt = time.Now().Unix()
		})
		return SyncResult{OK: true, Models: []*store.Model{}, Discovered: 0}
	}

	// 增量同步：快照已有真实探测结果与模型 ID，按 (channel_id, upstream_id) 复用，
	// 避免重复同步丢失真实输入/输出样例、或产生重复记录 / 孤立 probe。
	prior := map[string]*store.Model{}
	priorAlias := map[string]string{} // alias -> 旧 model id
	for _, m := range d.St.ListModelsByChannel(ch.ID) {
		prior[m.UpstreamID] = m
		priorAlias[m.Alias] = m.ID
	}
	d.St.DeleteModelsByChannel(ch.ID)

	out := make([]*store.Model, 0, len(autos))
	seenAlias := map[string]bool{}
	for _, a := range autos {
		m := &store.Model{
			ID: store.NewID("mdl"), ChannelID: ch.ID, UpstreamID: a.ID,
			Alias:       a.ID, // 统一别名逐字使用真实上游 id
			DisplayName: a.Name, ProviderType: ch.ProviderType,
			ContextLength: a.ContextLength, MaxOutputTokens: a.MaxOutputTokens,
			SupportsStream: a.SupportsStream, SupportsVision: a.SupportsVision,
			SupportsTools: a.SupportsTools, SupportsReasoning: a.SupportsReasoning,
			InputPricePerMtok: a.PromptPrice * 1e6, OutputPricePerMtok: a.CompletionPrice * 1e6,
			ParamSource: "upstream", RawJSON: a.Raw,
			ProbeStatus: "unavailable", Enabled: true,
		}
		if p, ok := prior[a.ID]; ok {
			m.ID = p.ID
			if p.ProbeStatus == "ok" && p.ProbedAt > 0 {
				m.ProbeStatus = p.ProbeStatus
				m.ProbeInput = p.ProbeInput
				m.ProbeOutput = p.ProbeOutput
				m.ProbePromptTokens = p.ProbePromptTokens
				m.ProbeCompletionTokens = p.ProbeCompletionTokens
				m.ProbeLatencyMs = p.ProbeLatencyMs
				m.ProbedAt = p.ProbedAt
			}
		}
		d.St.UpsertModel(m)
		d.St.UpsertAlias(&store.ModelAlias{
			Alias: a.ID, CanonicalModelID: m.ID,
			Description: "discovered from real upstream " + ch.BaseURI, Enabled: true,
			CreatedAt: time.Now().Unix(),
		})
		seenAlias[a.ID] = true
		out = append(out, m)
	}
	// 清理上游已下线的旧别名：只删确实指向本渠道旧模型 ID 的别名，不误删他人渠道。
	for alias, oldID := range priorAlias {
		if seenAlias[alias] {
			continue
		}
		if cur, ok := d.St.GetAlias(alias); ok && cur.CanonicalModelID == oldID {
			d.St.DeleteAlias(alias)
		}
	}
	d.St.UpdateChannelLocked(ch.ID, func(c *store.Channel) {
		c.ProbeStatus = "ok"
		c.LastError = ""
		c.FailCount = 0
		c.HealthCheckedAt = time.Now().Unix()
	})
	return SyncResult{OK: true, Models: out, Discovered: len(out)}
}

// ProbeResult 单模型探测结果。
type ProbeResult struct {
	ModelID   string
	OK        bool
	LatencyMs int
	Error     string
}

// ProbeModel 执行 F4：发一次极小真实请求采集真实输入/输出/用量/延迟。
// 探测失败仅标 probe_status=failed，不写任何编造的输出内容。
func (d *Discoverer) ProbeModel(ctx context.Context, ch *store.Channel, m *store.Model) ProbeResult {
	cx, err := d.channelCtx(ch)
	if err != nil {
		return d.recordFailedProbe(m, err.Error())
	}
	probeInput := map[string]interface{}{
		"model": m.UpstreamID,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Reply with the single word: OK"},
		},
		"max_tokens": d.probeMaxTokens(), "stream": false,
	}
	canonical, _ := json.Marshal(probeInput)
	ad := adapter.ForProvider(ch.ProviderType)
	url, headers, body, err := ad.ChatRequest(cx, canonical)
	if err != nil {
		return d.recordFailedProbe(m, err.Error())
	}
	start := time.Now()
	status, respBody, err := d.do(ctx, http.MethodPost, url, headers, body)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return d.recordFailedProbe(m, err.Error())
	}
	if status < 200 || status >= 300 {
		return d.recordFailedProbe(m, fmt.Sprintf("上游返回 HTTP %d: %s", status, trunc(string(respBody), 300)))
	}
	// 把上游响应规范成 OpenAI 格式以抽取真实输出与 usage
	canonicalResp, err := ad.ResponseToCanonical(status, respBody)
	if err != nil {
		return d.recordFailedProbe(m, err.Error())
	}
	output, pt, ct := extractOutputAndUsage(canonicalResp)
	inputBytes, _ := json.Marshal(probeInput)
	now := time.Now().Unix()
	d.St.UpdateModel(m.ID, func(mm *store.Model) {
		mm.ProbeStatus = "ok"
		mm.ProbeInput = inputBytes
		mm.ProbeOutput = output
		mm.ProbePromptTokens = pt
		mm.ProbeCompletionTokens = ct
		mm.ProbeLatencyMs = latency
		mm.ProbedAt = now
	})
	d.St.AddProbe(&store.ModelProbe{
		ModelID: m.ID, Input: inputBytes, Output: output, LatencyMs: latency,
		OK: true, CreatedAt: now,
	})
	return ProbeResult{ModelID: m.ID, OK: true, LatencyMs: latency}
}

func (d *Discoverer) probeMaxTokens() int {
	if d.Cfg != nil && d.Cfg.ProbeMaxTokens > 0 {
		return d.Cfg.ProbeMaxTokens
	}
	return 16
}

func (d *Discoverer) recordFailedProbe(m *store.Model, reason string) ProbeResult {
	now := time.Now().Unix()
	d.St.UpdateModel(m.ID, func(mm *store.Model) {
		mm.ProbeStatus = "failed"
		mm.ProbeOutput = "" // 不写入任何编造内容
		mm.ProbedAt = now
	})
	d.St.AddProbe(&store.ModelProbe{
		ModelID: m.ID, Output: "", OK: false, Error: reason, CreatedAt: now,
	})
	return ProbeResult{ModelID: m.ID, OK: false, Error: reason}
}

// ProbeChannel 对渠道下所有模型做真实探测。
func (d *Discoverer) ProbeChannel(ctx context.Context, ch *store.Channel) (probed, failed int) {
	for _, m := range d.St.ListModelsByChannel(ch.ID) {
		r := d.ProbeModel(ctx, ch, m)
		if r.OK {
			probed++
		} else {
			failed++
		}
	}
	return
}

// extractOutputAndUsage 从 OpenAI 规范响应抽取真实输出文本与 token 用量。
func extractOutputAndUsage(canonical []byte) (output string, pt, ct int) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &m); err != nil {
		return "", 0, 0
	}
	var choices []map[string]json.RawMessage
	if v, ok := m["choices"]; ok {
		_ = json.Unmarshal(v, &choices)
	}
	if len(choices) > 0 {
		var msg map[string]json.RawMessage
		_ = json.Unmarshal(choices[0]["message"], &msg)
		_ = json.Unmarshal(msg["content"], &output)
	}
	if v, ok := m["usage"]; ok {
		var u map[string]json.RawMessage
		_ = json.Unmarshal(v, &u)
		_ = json.Unmarshal(u["prompt_tokens"], &pt)
		_ = json.Unmarshal(u["completion_tokens"], &ct)
	}
	return
}

func trunc(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}
