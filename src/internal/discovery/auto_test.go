package discovery

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"llm-hub/src/internal/crypto"
	"llm-hub/src/internal/store"
)

// captureDoer 记录出站请求头，便于核对 Anthropic 鉴权风格。
type captureDoer struct {
	fn      func(*http.Request) (*http.Response, error)
	headers http.Header
}

func (c *captureDoer) Do(r *http.Request) (*http.Response, error) {
	c.headers = r.Header.Clone()
	return c.fn(r)
}

func resp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func testMasterKey(t *testing.T) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
}

func newFixture(t *testing.T, doer Doer) (*Discoverer, *store.Store, *store.Channel) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := crypto.NewCipher(testMasterKey(t))
	if err != nil {
		t.Fatal(err)
	}
	ct, iv, tag, err := c.Encrypt([]byte("sk-real-upstream-key"))
	if err != nil {
		t.Fatal(err)
	}
	ch := &store.Channel{
		ID: store.NewID("chn"), Name: "t", ProviderType: "openai",
		BaseURI: "https://upstream.mock/v1", AuthStyle: "bearer",
		Visibility: "public", Status: "active", Weight: 1,
		APIKeyCipher: ct, APIKeyIV: iv, APIKeyTag: tag, KeyHint: "****",
	}
	st.CreateChannel(ch)
	d := New(st, c, nil)
	d.SetDoer(doer)
	return d, st, ch
}

// 测试数据逐字取自 data/models-real.json 的真实条目（liquid/lfm-3-1.7b、
// google/gemini-omni-1.2-flash），模拟「真实上游 GET /models 响应」，
// 绝不编造任何模型 id / 名称 / 参数 / 价格。
const fullModelJSON = `{"object":"list","data":[{"id":"liquid/lfm-3-1.7b","name":"LiquidAI: LFM3-1.7B","canonical_slug":"liquid/lfm-3-1.7b-20261106","created":1794009477,"context_length":262144,"architecture":{"modality":"text->text","input_modalities":["text"],"output_modalities":["text"],"tokenizer":"Other","instruct_type":null},"pricing":{"prompt":"0.00000008","completion":"0.0000002","input_cache_read":"0.00000002"},"top_provider":{"context_length":262144,"max_completion_tokens":8192,"is_moderated":false},"supported_parameters":["frequency_penalty","include_reasoning","logprobs","max_completion_tokens","max_tokens","min_p","presence_penalty","reasoning","repetition_penalty","response_format","seed","stop","structured_outputs","temperature","tool_choice","tools","top_k","top_logprobs","top_p"]},{"id":"google/gemini-omni-1.2-flash","name":"Google: Gemini Omni 1.2 Flash","canonical_slug":"google/gemini-omni-1.2-flash-20261105","created":1793898961,"context_length":131072,"architecture":{"modality":"text+image+video->text+video","input_modalities":["text","image","video"],"output_modalities":["text","video"],"tokenizer":"Gemini","instruct_type":null},"pricing":{"prompt":"0.0000015","completion":"0.000009","web_search":"0.014"},"top_provider":{"context_length":131072,"max_completion_tokens":65536,"is_moderated":false},"supported_parameters":["include_reasoning","max_tokens","reasoning","reasoning_effort","seed","temperature","top_p"]}]}`

// 真实上游 /models 的完整字段逐字解析（id/name/context_length/pricing/architecture/
// supported_parameters/top_provider），绝不内置能力表或编造字段。
func TestParseUpstreamModelsFullFields(t *testing.T) {
	ms, err := parseUpstreamModels([]byte(fullModelJSON), 200, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("应解析 2 条真实模型，实际 %d", len(ms))
	}
	a := ms[0]
	if a.ID != "liquid/lfm-3-1.7b" || a.Name != "LiquidAI: LFM3-1.7B" {
		t.Fatalf("id/name 必须逐字来自上游: %+v", a)
	}
	if a.CanonicalSlug != "liquid/lfm-3-1.7b-20261106" || a.Created != 1794009477 || a.OwnedBy != "liquid" {
		t.Fatalf("canonical_slug/created/owned_by 解析错误: %+v", a)
	}
	if a.ContextLength != 262144 || a.MaxOutputTokens != 8192 {
		t.Fatalf("context_length/top_provider.max_completion_tokens 解析错误: %d/%d", a.ContextLength, a.MaxOutputTokens)
	}
	if a.Modality != "text->text" || len(a.InputModalities) != 1 || a.SupportsVision {
		t.Fatalf("architecture 解析错误: %+v", a)
	}
	if a.PromptPrice != 0.00000008 || a.CompletionPrice != 0.0000002 {
		t.Fatalf("pricing 解析错误: %v/%v", a.PromptPrice, a.CompletionPrice)
	}
	if !a.SupportsTools || a.SupportsStream || !a.SupportsReasoning || len(a.SupportedParameters) != 19 {
		t.Fatalf("supported_parameters 能力位解析错误: %+v", a)
	}
	if len(a.Raw) == 0 || len(a.Architecture) == 0 || len(a.Pricing) == 0 || len(a.TopProvider) == 0 {
		t.Fatal("应保留逐字原始对象与 architecture/pricing/top_provider 子对象")
	}
	b := ms[1]
	if b.ID != "google/gemini-omni-1.2-flash" || b.Name != "Google: Gemini Omni 1.2 Flash" {
		t.Fatalf("第二条真实模型解析错误: %+v", b)
	}
	if b.ContextLength != 131072 || b.MaxOutputTokens != 65536 || !b.SupportsVision {
		t.Fatalf("第二条真实模型参数解析错误: %+v", b)
	}
}

// Anthropic 风格显示名取 display_name；上游没有任何名称字段时逐字回退到真实 id。
func TestParseUpstreamModelsAnthropicDisplayName(t *testing.T) {
	body := `{"data":[{"id":"google/gemini-omni-1.2-flash","display_name":"Google: Gemini Omni 1.2 Flash"}]}`
	ms, err := parseUpstreamModels([]byte(body), 200, true)
	if err != nil || len(ms) != 1 || ms[0].Name != "Google: Gemini Omni 1.2 Flash" {
		t.Fatalf("Anthropic display_name 未生效: %v %+v", err, ms)
	}
	ms, err = parseUpstreamModels([]byte(body), 200, false)
	if err != nil || len(ms) != 1 || ms[0].Name == "" {
		t.Fatalf("非 Anthropic 也应解析出真实名称: %v %+v", err, ms)
	}
	ms, err = parseUpstreamModels([]byte(`{"data":[{"id":"liquid/lfm-3-1.7b"}]}`), 200, false)
	if err != nil || len(ms) != 1 || ms[0].Name != "liquid/lfm-3-1.7b" {
		t.Fatalf("无名称字段时应逐字回退到真实 id: %v %+v", err, ms)
	}
}

// 非法/空响应不产生任何占位模型。
func TestParseUpstreamModelsRejectsBadPayloads(t *testing.T) {
	if _, err := parseUpstreamModels([]byte(`{}`), 200, false); err == nil {
		t.Fatal("缺少 data 且非数组应报错")
	}
	if _, err := parseUpstreamModels([]byte(`{"data":[]}`), 503, false); err == nil {
		t.Fatal("非 2xx 应报错")
	}
	ms, err := parseUpstreamModels([]byte(`{"data":[]}`), 200, false)
	if err != nil || len(ms) != 0 {
		t.Fatalf("空 data 应返回空列表: %v %+v", err, ms)
	}
	ms, err = parseUpstreamModels([]byte(`{"data":[{"name":"LiquidAI: LFM3-1.7B"}]}`), 200, false)
	if err != nil || len(ms) != 0 {
		t.Fatalf("无 id 的条目必须丢弃: %v %+v", err, ms)
	}
	// 裸数组也支持
	ms, err = parseUpstreamModels([]byte(`[{"id":"liquid/lfm-3-1.7b"}]`), 200, false)
	if err != nil || len(ms) != 1 || ms[0].ID != "liquid/lfm-3-1.7b" {
		t.Fatalf("裸数组解析失败: %v %+v", err, ms)
	}
	// 非法定价不采信
	ms, err = parseUpstreamModels([]byte(`{"data":[{"id":"liquid/lfm-3-1.7b","pricing":{"prompt":"NaN","completion":"-1"}}]}`), 200, false)
	if err != nil || len(ms) != 1 || ms[0].PromptPrice != 0 || ms[0].CompletionPrice != 0 {
		t.Fatalf("NaN/负定价必须拒绝: %v %+v", err, ms)
	}
}

// Anthropic 渠道 GET /models 必须带 x-api-key + anthropic-version: 2023-06-01。
func TestFetchModelsAnthropicHeaders(t *testing.T) {
	doer := &captureDoer{fn: func(r *http.Request) (*http.Response, error) {
		return resp(200, `{"data":[{"id":"google/gemini-omni-1.2-flash","display_name":"Google: Gemini Omni 1.2 Flash"}]}`), nil
	}}
	d, _, ch := newFixture(t, doer)
	ch.ProviderType = "anthropic"
	ch.AuthStyle = "x-api-key"
	ch.APIVersion = ""
	ms, err := d.fetchModels(context.Background(), ch)
	if err != nil || len(ms) != 1 {
		t.Fatalf("Anthropic 发现失败: %v %+v", err, ms)
	}
	if got := doer.headers.Get("x-api-key"); got != "sk-real-upstream-key" {
		t.Fatalf("缺少 x-api-key: %q", got)
	}
	if got := doer.headers.Get("anthropic-version"); got != "2023-06-01" {
		t.Fatalf("缺少 anthropic-version: %q", got)
	}
	if ms[0].Name != "Google: Gemini Omni 1.2 Flash" {
		t.Fatalf("Anthropic display_name 未生效: %q", ms[0].Name)
	}
}

// 增量同步：复用模型 ID、保留真实探测样例、不产生重复记录、不重复追加别名。
func TestSyncChannelIncrementalPreservesRealProbe(t *testing.T) {
	doer := &captureDoer{fn: func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			return resp(200, fullModelJSON), nil
		}
		return resp(200, `{"choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`), nil
	}}
	d, st, ch := newFixture(t, doer)

	r1 := d.SyncChannel(context.Background(), ch)
	if !r1.OK || r1.Discovered != 2 {
		t.Fatalf("首次同步失败: %+v", r1)
	}
	firstID := r1.Models[0].ID
	st.UpdateModel(firstID, func(m *store.Model) {
		m.ProbeStatus = "ok"
		m.ProbeOutput = "OK"
		m.ProbedAt = time.Now().Unix()
	})

	r2 := d.SyncChannel(context.Background(), ch)
	if !r2.OK || r2.Discovered != 2 {
		t.Fatalf("二次同步失败: %+v", r2)
	}
	if got := len(st.ListModelsByChannel(ch.ID)); got != 2 {
		t.Fatalf("增量同步后模型数应为 2，实际 %d", got)
	}
	if r2.Models[0].ID != firstID {
		t.Fatalf("增量同步应复用模型 ID: %q != %q", r2.Models[0].ID, firstID)
	}
	m, _ := st.GetModel(firstID)
	if m.ProbeStatus != "ok" || m.ProbeOutput != "OK" {
		t.Fatalf("真实探测样例必须保留: %+v", m)
	}
	if got := len(st.ListAliases()); got != 2 {
		t.Fatalf("别名不应重复追加，实际 %d", got)
	}
}

// 拉取失败时模型列表保持为空，渠道 probe_status=unavailable，绝不编造。
func TestSyncChannelFailureKeepsModelsEmpty(t *testing.T) {
	d, st, ch := newFixture(t, &captureDoer{fn: func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	}})
	res := d.SyncChannel(context.Background(), ch)
	if res.OK || len(res.Models) != 0 || res.Discovered != 0 {
		t.Fatalf("失败时应为空结果: %+v", res)
	}
	if got := len(st.ListModelsByChannel(ch.ID)); got != 0 {
		t.Fatalf("失败时不得产生占位模型: %d", got)
	}
	cur, _ := st.GetChannel(ch.ID)
	if cur.ProbeStatus != "unavailable" {
		t.Fatalf("probe_status 应为 unavailable: %q", cur.ProbeStatus)
	}
}

// 上游返回空 data：列表为空且标记 unavailable。
func TestSyncChannelEmptyDataStaysUnavailable(t *testing.T) {
	d, st, ch := newFixture(t, &captureDoer{fn: func(*http.Request) (*http.Response, error) {
		return resp(200, `{"object":"list","data":[]}`), nil
	}})
	res := d.SyncChannel(context.Background(), ch)
	if !res.OK || res.Discovered != 0 || len(res.Models) != 0 {
		t.Fatalf("空 data 应为空列表: %+v", res)
	}
	if got := len(st.ListModelsByChannel(ch.ID)); got != 0 {
		t.Fatalf("空 data 不得产生模型: %d", got)
	}
	cur, _ := st.GetChannel(ch.ID)
	if cur.ProbeStatus != "unavailable" {
		t.Fatalf("空 data 应标记 unavailable: %q", cur.ProbeStatus)
	}
}

// 自动真实探测：对每条新模型发一次真实极小请求，采集真实输入/输出。
func TestAutoProbeCollectsRealSamplesForEachModel(t *testing.T) {
	doer := &captureDoer{fn: func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			return resp(200, fullModelJSON), nil
		}
		return resp(200, `{"choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`), nil
	}}
	d, st, ch := newFixture(t, doer)
	res := d.SyncChannel(context.Background(), ch)
	if !res.OK || res.Discovered != 2 {
		t.Fatalf("同步失败: %+v", res)
	}
	job := d.AutoProbe(ch)
	probed, failed := job.Wait()
	if probed != 2 || failed != 0 {
		t.Fatalf("应真实探测 2 条模型: probed=%d failed=%d", probed, failed)
	}
	for _, m := range res.Models {
		cur, _ := st.GetModel(m.ID)
		if cur.ProbeStatus != "ok" || cur.ProbeOutput != "OK" {
			t.Fatalf("真实输出样例未落库: %+v", cur)
		}
		if len(cur.ProbeInput) == 0 || cur.ProbePromptTokens != 10 {
			t.Fatalf("真实输入/用量未落库: %+v", cur)
		}
	}
}
