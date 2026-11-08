package tests

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-hub/src/hub"
)

// testMasterKey 返回固定 32 字节 base64 主密钥，专供离线测试使用。
// 团队7 patch 002 后 NewCipher 拒绝空 MASTER_KEY，因此测试必须显式注入，
// 不依赖环境变量、也不回退到源码内可推导的开发密钥。
func testMasterKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
}

// ---------- 可注入的假上游 HTTP 执行器 ----------

type fakeDoer struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f *fakeDoer) Do(r *http.Request) (*http.Response, error) { return f.fn(r) }

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func sseResp(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
}

// mockUpstream 返回一个按 URL 路径分派的假上游：/models 返回真实数据集中的模型 id，
// /chat/completions 返回 OpenAI 规范响应（或 SSE），/messages 返回 Anthropic 规范响应。
func mockUpstream(chatBody string, chatIsSSE bool) *fakeDoer {
	return &fakeDoer{fn: func(r *http.Request) (*http.Response, error) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/models"):
			return jsonResp(200, `{"object":"list","data":[{"id":"liquid/lfm-3-1.7b","object":"model","created":1794009477,"owned_by":"liquid"}]}`), nil
		case strings.HasSuffix(p, "/chat/completions"), strings.HasSuffix(p, "/messages"),
			strings.HasSuffix(p, "/responses"), strings.HasSuffix(p, "/completions"),
			strings.HasSuffix(p, "/embeddings"):
			if chatIsSSE {
				return sseResp(chatBody), nil
			}
			return jsonResp(200, chatBody), nil
		default:
			return jsonResp(200, chatBody), nil
		}
	}}
}

// ---------- Server 构造 ----------

func newServer(t *testing.T, doer *fakeDoer) (*hub.Server, *hub.Store) {
	t.Helper()
	cfg := hub.LoadConfig()
	cfg.DataDir = t.TempDir()
	cfg.RegistrationOpen = true
	cfg.MasterKeyB64 = testMasterKey()
	cfg.MaxFailover = 3
	st, err := hub.NewStore(cfg.DataDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	var d hub.Doer
	if doer != nil {
		d = doer
	}
	// 测试用放行 SSRF 校验器（允许本地 httptest 风格 base_uri），生产由 channel.New 注入 ssrf.Validate
	validator := func(string) error { return nil }
	srv, err := hub.NewServerWith(cfg, st, d, validator)
	if err != nil {
		t.Fatalf("NewServerWith: %v", err)
	}
	t.Cleanup(func() { srv.Stop() })
	return srv, st
}

// doLoopback 与 do 相同，但把 RemoteAddr 设为 127.0.0.1（回环运维通道），
// 供防火墙 seal/unlock 等 V-36 修复后的本机运维端点测试使用。
func doLoopback(srv *hub.Server, method, path, body, auth string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.RemoteAddr = "127.0.0.1:41234"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func do(srv *hub.Server, method, path, body, auth string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("响应不是 JSON: %v; body=%s", err, rec.Body.String())
	}
	return m
}

// bootstrap 注册首个超级用户（superuser）、创建平台令牌，返回 (JWT, sk-)。
func bootstrap(t *testing.T, srv *hub.Server) (string, string) {
	t.Helper()
	rec := do(srv, "POST", "/api/auth/register", `{"email":"tester@example.com","password":"password123"}`, "")
	if rec.Code != 200 {
		t.Fatalf("register 失败: %d %s", rec.Code, rec.Body.String())
	}
	m := decode(t, rec)
	tokens, ok := m["tokens"].(map[string]interface{})
	if !ok {
		t.Fatalf("register 未返回 tokens: %v", m)
	}
	jwt, _ := tokens["access_token"].(string)
	if jwt == "" {
		t.Fatalf("register 未返回 access_token")
	}
	rec = do(srv, "POST", "/api/tokens", `{"name":"test","rpm":60,"tpm":200000}`, "Bearer "+jwt)
	if rec.Code != 200 {
		t.Fatalf("create token 失败: %d %s", rec.Code, rec.Body.String())
	}
	tm := decode(t, rec)
	sk, _ := tm["token"].(string)
	if !strings.HasPrefix(sk, "sk-") {
		t.Fatalf("未返回 sk- 明文令牌: %v", tm)
	}
	return jwt, sk
}

// createChannel 通过 API 创建一条 public 渠道，返回渠道 id。
func createChannel(t *testing.T, srv *hub.Server, jwt string) string {
	t.Helper()
	body := `{"name":"mock","provider_type":"openai","base_uri":"https://upstream.mock/v1","api_key":"sk-upstream-secret-1234","visibility":"public"}`
	rec := do(srv, "POST", "/api/channels", body, "Bearer "+jwt)
	if rec.Code != 200 {
		t.Fatalf("create channel 失败: %d %s", rec.Code, rec.Body.String())
	}
	m := decode(t, rec)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("create channel 未返回 id: %v", m)
	}
	return id
}

func mustContain(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("期望包含 %q，实际: %s", sub, s)
	}
}

func rawJSON(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(buf.Bytes())
}
