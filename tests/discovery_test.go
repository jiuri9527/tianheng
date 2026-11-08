package tests

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"llm-hub/src/hub"
)

func testChannel(t *testing.T, st *hub.Store, c *hub.Cipher, baseURI string) *hub.Channel {
	t.Helper()
	ct, iv, tag, err := c.Encrypt([]byte("sk-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	ch := &hub.Channel{
		ID: hub.NewID("chn"), Name: "t", ProviderType: "openai", BaseURI: baseURI,
		AuthStyle: "bearer", Visibility: "public", Status: "active", Weight: 1,
		APIKeyCipher: ct, APIKeyIV: iv, APIKeyTag: tag, KeyHint: hub.KeyHint("sk-test-key"),
	}
	st.CreateChannel(ch)
	return ch
}

func testDiscoverer(t *testing.T, doer *fakeDoer) (*hub.Store, *hub.Cipher, *hub.Config, *hub.Discoverer) {
	t.Helper()
	cfg := hub.LoadConfig()
	st, err := hub.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := hub.NewCipher(testMasterKey())
	if err != nil {
		t.Fatal(err)
	}
	d := hub.NewDiscoverer(st, c, cfg)
	d.SetDoer(doer)
	return st, c, cfg, d
}

func failingDoer(err error) *fakeDoer {
	return &fakeDoer{fn: func(*http.Request) (*http.Response, error) {
		return nil, err
	}}
}

// 校验：未拉取到真实上游数据时模型列表必须为空、probe_status=unavailable，绝不编造/占位。
func TestDiscoveryEmptyOnUpstreamFailure(t *testing.T) {
	st, c, _, d := testDiscoverer(t, failingDoer(errors.New("connection refused")))
	ch := testChannel(t, st, c, "https://upstream.mock/v1")

	res := d.SyncChannel(context.Background(), ch)
	if res.OK {
		t.Fatal("上游不可达时不应标记 OK")
	}
	if len(res.Models) != 0 {
		t.Fatalf("上游不可达时模型列表必须为空，实际 %d 条: %+v", len(res.Models), res.Models)
	}
	if res.Discovered != 0 {
		t.Fatalf("Discovered 应为 0，实际 %d", res.Discovered)
	}
	if ch.ProbeStatus != "unavailable" {
		t.Fatalf("probe_status 应为 unavailable，实际 %q", ch.ProbeStatus)
	}
	if got := len(st.ListModelsByChannel(ch.ID)); got != 0 {
		t.Fatalf("store 中该渠道模型应为 0，实际 %d", got)
	}
	if got := len(st.ListModels()); got != 0 {
		t.Fatalf("全局不应产生任何占位/编造模型，实际 %d 条", got)
	}
}

func TestDiscoveryEmptyOnNon2xx(t *testing.T) {
	st, c, _, d := testDiscoverer(t, &fakeDoer{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(503, `{"error":"upstream down"}`), nil
	}})
	ch := testChannel(t, st, c, "https://upstream.mock/v1")

	res := d.SyncChannel(context.Background(), ch)
	if res.OK || len(res.Models) != 0 || ch.ProbeStatus != "unavailable" {
		t.Fatalf("HTTP 503 应导致空模型列表 + unavailable: %+v status=%q", res, ch.ProbeStatus)
	}
	if strings.Contains(res.Error, "gpt") {
		t.Fatal("错误信息不应包含编造模型名")
	}
}

func TestDiscoveryRealModelsFromUpstreamOnly(t *testing.T) {
	st, c, _, d := testDiscoverer(t, &fakeDoer{fn: func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			return jsonResp(200, `{}`), nil
		}
		return jsonResp(200, `{"object":"list","data":[
		  {"id":"liquid/lfm-3-1.7b","object":"model","created":1794009477,"owned_by":"liquid",
		   "context_length":262144,"architecture":{"modality":"text->text","input_modalities":["text"],"output_modalities":["text"]},
		   "pricing":{"prompt":"0.00000008","completion":"0.0000002"},
		   "top_provider":{"max_completion_tokens":8192}}
		]}`), nil
	}})
	ch := testChannel(t, st, c, "https://upstream.mock/v1")

	res := d.SyncChannel(context.Background(), ch)
	if !res.OK || res.Discovered != 1 || len(res.Models) != 1 {
		t.Fatalf("真实模型发现失败: %+v", res)
	}
	m := res.Models[0]
	if m.UpstreamID != "liquid/lfm-3-1.7b" || m.Alias != "liquid/lfm-3-1.7b" {
		t.Fatalf("模型 id 必须逐字来自上游: %+v", m)
	}
	if m.ParamSource != "upstream" {
		t.Fatalf("参数来源应为 upstream: %q", m.ParamSource)
	}
	if len(m.RawJSON) == 0 {
		t.Fatal("应保留逐字上游原始对象")
	}
	if m.ContextLength != 262144 || m.MaxOutputTokens != 8192 {
		t.Fatalf("参数应从真实上游对象补全: ctx=%d max=%d", m.ContextLength, m.MaxOutputTokens)
	}
	if m.ProbeOutput != "" {
		t.Fatal("未探测时不应有输出样例")
	}
	// 该 id 必须真实存在于 data/models-real.json
	idset, err := hub.RealModelIDSet()
	if err != nil || !idset[m.UpstreamID] {
		t.Fatalf("发现的模型 id 不在真实数据集中: %q err=%v", m.UpstreamID, err)
	}
	// 别名与 store 落库
	if _, ok := st.GetAlias("liquid/lfm-3-1.7b"); !ok {
		t.Fatal("未生成全局 model_alias")
	}
	if len(st.ListModelsByChannel(ch.ID)) != 1 {
		t.Fatal("未 upsert 到 models")
	}
}

func TestDiscoveryProbeRealInputOutput(t *testing.T) {
	st, c, _, d := testDiscoverer(t, &fakeDoer{fn: func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			return jsonResp(200, `{"data":[{"id":"liquid/lfm-3-1.7b","owned_by":"liquid"}]}`), nil
		}
		return jsonResp(200, `{"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"liquid/lfm-3-1.7b",
		  "choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],
		  "usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`), nil
	}})
	ch := testChannel(t, st, c, "https://upstream.mock/v1")
	res := d.SyncChannel(context.Background(), ch)
	if !res.OK || len(res.Models) != 1 {
		t.Fatalf("同步失败: %+v", res)
	}
	pr := d.ProbeModel(context.Background(), ch, res.Models[0])
	if !pr.OK {
		t.Fatalf("探测应成功: %+v", pr)
	}
	m, _ := st.GetModel(res.Models[0].ID)
	if m.ProbeStatus != "ok" {
		t.Fatalf("probe_status 应为 ok: %q", m.ProbeStatus)
	}
	if m.ProbeOutput != "OK" {
		t.Fatalf("输出样例应来自真实响应: %q", m.ProbeOutput)
	}
	if !strings.Contains(string(m.ProbeInput), "Reply with the single word: OK") {
		t.Fatalf("输入样例未记录: %s", m.ProbeInput)
	}
	if m.ProbePromptTokens != 10 || m.ProbeCompletionTokens != 2 {
		t.Fatalf("用量应来自真实上游 usage: %d/%d", m.ProbePromptTokens, m.ProbeCompletionTokens)
	}
	probes := st.ListProbesByModel(m.ID)
	if len(probes) != 1 || !probes[0].OK || probes[0].Output != "OK" {
		t.Fatalf("model_probes 未落真实探测: %+v", probes)
	}
}

func TestDiscoveryProbeFailureNeverFabricates(t *testing.T) {
	call := 0
	st, c, _, d := testDiscoverer(t, &fakeDoer{fn: func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			return jsonResp(200, `{"data":[{"id":"liquid/lfm-3-1.7b"}]}`), nil
		}
		call++
		return nil, errors.New("probe call failed")
	}})
	ch := testChannel(t, st, c, "https://upstream.mock/v1")
	res := d.SyncChannel(context.Background(), ch)
	pr := d.ProbeModel(context.Background(), ch, res.Models[0])
	if pr.OK {
		t.Fatal("上游失败时探测不应成功")
	}
	m, _ := st.GetModel(res.Models[0].ID)
	if m.ProbeStatus != "failed" {
		t.Fatalf("probe_status 应为 failed: %q", m.ProbeStatus)
	}
	if m.ProbeOutput != "" {
		t.Fatalf("探测失败时绝不编造输出内容: %q", m.ProbeOutput)
	}
	if call != 1 {
		t.Fatalf("应真实发起一次探测请求，实际 %d", call)
	}
}
