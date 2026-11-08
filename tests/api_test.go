package tests

import (
	"encoding/json"
	"net/http"
	"errors"
	"strings"
	"testing"

	"llm-hub/src/hub"
)

func errorMessage(t *testing.T, body []byte) string {
	t.Helper()
	var m map[string]interface{}
	_ = json.Unmarshal(body, &m)
	if e, ok := m["error"].(map[string]interface{}); ok {
		msg, _ := e["message"].(string)
		code, _ := e["code"].(string)
		t.Logf("error body: message=%q code=%q", msg, code)
		return msg + " | " + code
	}
	return string(body)
}

func TestAPIAuthAndManagement(t *testing.T) {
	srv, st := newServer(t, nil)

	// 天衡：注册用户统一 superuser，没有任何管理员角色。
	rec := do(srv, "POST", "/api/auth/register", `{"email":"tester@example.com","password":"password123"}`, "")
	if rec.Code != 200 {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	reg := decode(t, rec)
	user := asStringMap(t, reg["user"])
	if user["role"] != "superuser" {
		t.Fatalf("注册用户应为 superuser: %v", user)
	}
	tokens := asStringMap(t, reg["tokens"])
	jwt, _ := tokens["access_token"].(string)
	if jwt == "" {
		t.Fatal("未返回 access_token")
	}
	if _, ok := st.GetUserByEmail("tester@example.com"); !ok {
		t.Fatal("用户未落库")
	}

	// 登录
	rec = do(srv, "POST", "/api/auth/login", `{"email":"tester@example.com","password":"password123"}`, "")
	if rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	// /api/me 无 JWT -> 401
	if rec := do(srv, "GET", "/api/me", "", ""); rec.Code != 401 {
		t.Fatalf("无 JWT 应 401，实际 %d", rec.Code)
	}
	// /api/me 有 JWT -> 200
	if rec := do(srv, "GET", "/api/me", "", "Bearer "+jwt); rec.Code != 200 {
		t.Fatalf("有 JWT 应 200，实际 %d %s", rec.Code, rec.Body.String())
	}
	// 错误 JWT -> 401
	if rec := do(srv, "GET", "/api/me", "", "Bearer not-a-jwt"); rec.Code != 401 {
		t.Fatalf("错误 JWT 应 401，实际 %d", rec.Code)
	}
	// 改密
	if rec := do(srv, "POST", "/api/auth/password", `{"old_password":"password123","new_password":"brandnew99"}`, "Bearer "+jwt); rec.Code != 200 {
		t.Fatalf("改密失败: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(srv, "POST", "/api/auth/login", `{"email":"tester@example.com","password":"brandnew99"}`, ""); rec.Code != 200 {
		t.Fatalf("新口令登录失败: %d", rec.Code)
	}
}

func TestAPITokenOnceAndV1Models(t *testing.T) {
	srv, st := newServer(t, nil)
	jwt, sk := bootstrap(t, srv)

	// sk- 明文仅创建时返回一次
	rec := do(srv, "GET", "/api/tokens", "", "Bearer "+jwt)
	mustNotContain(t, rec.Body.String(), sk)
	list := asSlice(t, decode(t, rec)["data"])
	if len(list) != 1 {
		t.Fatalf("应有 1 个令牌，实际 %d", len(list))
	}
	tv := asStringMap(t, list[0])
	if _, ok := tv["token"]; ok {
		t.Fatal("列表接口不应回显明文令牌")
	}
	if _, ok := tv["token_hash"]; ok {
		t.Fatal("列表接口不应回显令牌哈希")
	}
	// 数据库只存哈希，不存明文
	for _, tkn := range st.ListTokens() {
		if strings.Contains(tkn.TokenHash, sk) || tkn.TokenHash == sk {
			t.Fatal("库里存了明文 sk-")
		}
		if tkn.TokenHash != hub.TokenHash(sk) {
			t.Fatal("令牌哈希不匹配")
		}
	}

	// /v1/models 需有效 sk-
	if rec := do(srv, "GET", "/v1/models", "", "Bearer "+sk); rec.Code != 200 {
		t.Fatalf("/v1/models 应 200，实际 %d", rec.Code)
	}
	// 无效 sk- -> 401
	if rec := do(srv, "GET", "/v1/models", "", "Bearer sk-invalid"); rec.Code != 401 {
		t.Fatalf("无效令牌应 401，实际 %d", rec.Code)
	}
	// 无令牌 -> 401
	if rec := do(srv, "GET", "/v1/models", "", ""); rec.Code != 401 {
		t.Fatalf("缺失令牌应 401，实际 %d", rec.Code)
	}
}

func TestAPINoUpstreamReturnsUpstreamError(t *testing.T) {
	srv, _ := newServer(t, nil)
	_, sk := bootstrap(t, srv)
	// 未配置任何渠道 / 无上游凭据
	body := `{"model":"liquid/lfm-3-1.7b","messages":[{"role":"user","content":"hi"}],"stream":false}`
	rec := do(srv, "POST", "/v1/chat/completions", body, "Bearer "+sk)
	if rec.Code != 502 {
		t.Fatalf("无上游应 502，实际 %d %s", rec.Code, rec.Body.String())
	}
	m := decode(t, rec)
	errObj := asStringMap(t, m["error"])
	if errObj["type"] != "upstream_error" {
		t.Fatalf("错误类型应为 upstream_error: %v", errObj)
	}
	if !strings.Contains(errObj["message"].(string), "no usable upstream channel") {
		t.Fatalf("错误信息应明确指出无可用上游: %v", errObj["message"])
	}
	// 绝不伪造模型响应内容
	mustNotContain(t, rec.Body.String(), `"choices"`)
	mustNotContain(t, rec.Body.String(), `"content"`)
	mustNotContain(t, rec.Body.String(), "assistant")
}

// 天衡：无任何令牌使用限制——额度耗尽 / RPM / TPM / 并发 / 过期 / IP / 模型白名单
// 都不得产生 402 / 429 / 403 拒绝，全部请求恒放行。
// F9：令牌限制字段必须被持久化并真实参与拒绝判定（SPEC.md C-2 禁止降级）。
// TestAPITokenLimitsAreNotEnforced 天衡：**不设任何使用限制**。
//
// 需求原文：不准对任何人设置 token 使用限制、并发限制、设备限制。
// 因此无论创建令牌时提交什么限制值，都必须被强制归零（0 = 不限），
// 且后续调用一律放行 —— 不再出现 429 / 402 / 模型白名单拦截。
func TestAPITokenLimitsAreNotEnforced(t *testing.T) {
	srv, _ := newServer(t, nil)
	jwt, _ := bootstrap(t, srv)

	// 1) 提交极端限制值：必须被强制归零（而非"持久化生效"）。
	rec := do(srv, "POST", "/api/tokens",
		`{"name":"nolimit","rpm":2,"tpm":4096,"max_concurrency":3,"quota_usd":0.5,`+
			`"ip_allowlist":["203.0.113.0/24"],"allowed_models":["liquid/lfm-3-1.7b"]}`, "Bearer "+jwt)
	if rec.Code != 200 {
		t.Fatalf("create token: %d %s", rec.Code, rec.Body.String())
	}
	tv := decode(t, rec)
	sk, _ := tv["token"].(string)
	if !strings.HasPrefix(sk, "sk-") {
		t.Fatalf("未返回 sk- 明文令牌: %v", tv)
	}
	for field, want := range map[string]float64{
		"rpm": 0, "tpm": 0, "max_concurrency": 0, "quota_usd": 0, "expires_at": 0,
	} {
		if got, _ := tv[field].(float64); got != want {
			t.Fatalf("%s 应被强制为 %v（不限），实际 %v", field, want, got)
		}
	}
	if am, ok := tv["allowed_models"].([]interface{}); ok && len(am) != 0 {
		t.Fatalf("allowed_models 应被清空（无白名单），实际 %v", tv["allowed_models"])
	}
	if ips, ok := tv["ip_allowlist"].([]interface{}); ok && len(ips) != 0 {
		t.Fatalf("ip_allowlist 应被清空（无 IP 限制），实际 %v", tv["ip_allowlist"])
	}

	// 2) 连续 60 次调用一律 200：不存在 429 限流。
	for i := 0; i < 60; i++ {
		r := do(srv, "GET", "/v1/models", "", "Bearer "+sk)
		if r.Code != 200 {
			t.Fatalf("第 %d 次调用应 200（天衡无任何使用限制），实际 %d %s",
				i+1, r.Code, r.Body.String())
		}
	}

	// 3) 即便把令牌额度手工写满并标记过期，也不得拦截（不再有 402 / 过期闸门）。
	rec = do(srv, "POST", "/api/tokens", `{"name":"edge","quota_usd":0.5}`, "Bearer "+jwt)
	edgeSK := decode(t, rec)["token"].(string)
	if !hub.ForceTokenLegacyLimitsForTest(srv.St, edgeSK) {
		t.Fatal("构造历史遗留限制状态失败")
	}
	// 同时确认存储里的真实值是"限制态"（证明下一步放行确实来自"不判定"）
	rpm, tpm, conc, exp, quota, ips, mdl, ok := hub.TokenLimitsForTest(srv.St, edgeSK)
	if !ok || exp != 1 || ips == 0 || mdl == 0 || quota == 0 {
		t.Fatalf("遗留限制状态未生效: ok=%v exp=%d ips=%d mdl=%d quota=%v", ok, exp, ips, mdl, quota)
	}
	_ = rpm
	_ = tpm
	_ = conc
	r := do(srv, "GET", "/v1/models", "", "Bearer "+edgeSK)
	if r.Code != 200 {
		t.Fatalf("过期/超额/IP 不合规的令牌仍应放行（天衡不设限制），实际 %d %s",
			r.Code, r.Body.String())
	}
	for _, forbidden := range []string{"rpm_exceeded", "insufficient_quota", "token has expired", "not allowed"} {
		mustNotContain(t, r.Body.String(), forbidden)
	}
}

// TestAPIModelWhitelistIsNotEnforced 天衡无模型白名单：任何令牌可调用全部真实模型。
func TestAPIModelWhitelistIsNotEnforced(t *testing.T) {
	srv, _ := newServer(t, nil)
	jwt, _ := bootstrap(t, srv)

	// 即使提交了白名单，也不被持久化，更不构成拦截依据。
	rec := do(srv, "POST", "/api/tokens",
		`{"name":"wl","allowed_models":["liquid/lfm-3-1.7b"]}`, "Bearer "+jwt)
	if rec.Code != 200 {
		t.Fatalf("create token: %d %s", rec.Code, rec.Body.String())
	}
	sk := decode(t, rec)["token"].(string)

	// 白名单外的模型同样可访问（不再 403 / model_not_allowed）。
	body := `{"model":"openai/gpt-6.1-terra","messages":[{"role":"user","content":"hi"}]}`
	r := do(srv, "POST", "/v1/chat/completions", body, "Bearer "+sk)
	mustNotContain(t, r.Body.String(), "model_not_allowed")
	mustNotContain(t, r.Body.String(), "not in allowlist")
	// 无上游凭据时是 502 upstream_error（真实失败），绝不是白名单拒绝。
	if r.Code == http.StatusForbidden {
		t.Fatalf("不得因模型白名单拒绝：%d %s", r.Code, r.Body.String())
	}
}


func TestAPIHealthzAndMetrics(t *testing.T) {
	srv, _ := newServer(t, nil)
	rec := do(srv, "GET", "/healthz", "", "")
	if rec.Code != 200 {
		t.Fatalf("/healthz: %d", rec.Code)
	}
	hm := decode(t, rec)
	if hm["status"] != "ok" || hm["real_models"].(float64) != 460 {
		t.Fatalf("/healthz 内容错误: %v", hm)
	}
	if hm["real_models_source"] != "data/models-real.json" {
		t.Fatalf("未标注真实来源: %v", hm)
	}
	rec = do(srv, "GET", "/metrics", "", "")
	if rec.Code != 200 {
		t.Fatalf("/metrics: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "llmhub_models_real_total 460")
}

// 字段契约 + SSRF 回归：snake_case 提交内网 base_uri 必须返回含 SSRF 拒绝的 400，
// 而不是「provider_type 必填」；合法外网 + 假 key 必须 200 且不编造任何模型。
func TestAPIChannelFieldContractAndSSRF(t *testing.T) {
	cfg := hub.LoadConfig()
	cfg.DataDir = t.TempDir()
	cfg.RegistrationOpen = true
	cfg.MasterKeyB64 = testMasterKey()
	st, err := hub.NewStore(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	// validator=nil -> 保留 channel.New 注入的真实 ssrf.Validate
	srv, err := hub.NewServerWith(cfg, st, failingDoer(errors.New("no upstream")), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Stop() })
	jwt, _ := bootstrap(t, srv)

	// [A] snake_case + 内网/元数据地址 -> 400 SSRF 拒绝（不是字段缺失误判）
	rec := do(srv, "POST", "/api/channels",
		`{"name":"ssrf-snake","provider_type":"openai","base_uri":"http://169.254.169.254/latest/meta-data/","api_key":"fake-key","visibility":"public"}`,
		"Bearer "+jwt)
	if rec.Code != 400 {
		t.Fatalf("内网 base_uri 应 400，实际 %d %s", rec.Code, rec.Body.String())
	}
	msg := errorMessage(t, rec.Body.Bytes())
	for _, want := range []string{"拒绝", "169.254.169.254", "base_uri 校验失败"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息应含 %q，实际: %s", want, msg)
		}
	}
	if strings.Contains(msg, "provider_type 必填") {
		t.Fatal("snake_case 字段未被正确解析（误判为必填缺失）")
	}

	// [C] snake_case + 公网字面 IP + 假 key -> 200，不编造模型
	rec = do(srv, "POST", "/api/channels",
		`{"name":"public-snake","provider_type":"openai","base_uri":"https://8.8.8.8/v1","api_key":"fake-key","visibility":"public"}`,
		"Bearer "+jwt)
	if rec.Code != 200 {
		t.Fatalf("合法外网 base_uri 应 200，实际 %d %s", rec.Code, rec.Body.String())
	}
	cm := decode(t, rec)
	if cm["models_discovered"].(float64) != 0 {
		t.Fatalf("拉取失败时 models_discovered 必须为 0: %v", cm["models_discovered"])
	}
	if cm["connection_ok"] != false {
		t.Fatalf("拉取失败时 connection_ok 必须为 false: %v", cm["connection_ok"])
	}
	if cm["probe_status"] != "unavailable" {
		t.Fatalf("拉取失败时 probe_status 应为 unavailable: %v", cm["probe_status"])
	}
	if got := len(st.ListModels()); got != 0 {
		t.Fatalf("拉取失败时不得产生任何占位/编造模型，实际 %d 条", got)
	}
	// 响应绝不回显 api_key 明文或密文
	mustNotContain(t, rec.Body.String(), "fake-key")
	mustNotContain(t, rec.Body.String(), "api_key_cipher")
}
