package tests

import (
	"errors"
	"net/http"
	"testing"
)

// 天衡：所有用户都是超级用户，没有任何管理员；任何登录用户可访问全部管理端点、
// 管理任意渠道，且渠道列表默认对所有人返回全部渠道。JWT 身份仍保留。
func TestAllUsersAreSuperusers(t *testing.T) {
	srv, st := newServer(t, &fakeDoer{fn: func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("no upstream")
	}})

	reg := func(email, password string) string {
		rec := do(srv, "POST", "/api/auth/register",
			`{"email":"`+email+`","password":"`+password+`"}`, "")
		if rec.Code != 200 {
			t.Fatalf("register %s: %d %s", email, rec.Code, rec.Body.String())
		}
		m := decode(t, rec)
		u := asStringMap(t, m["user"])
		if u["role"] != "superuser" {
			t.Fatalf("%s role 应为 superuser，实际 %v", email, u["role"])
		}
		tk := asStringMap(t, m["tokens"])
		jwt, _ := tk["access_token"].(string)
		if jwt == "" {
			t.Fatalf("%s 未返回 JWT", email)
		}
		return jwt
	}
	jwtA := reg("alpha@example.com", "password123")
	jwtB := reg("beta@example.com", "mzqwlrkc238")

	// 第二个用户（非首注册、非 admin）可访问全部 /api/admin/* 端点。
	adminPaths := []string{
		"/api/admin/dashboard", "/api/admin/users", "/api/admin/settings",
		"/api/admin/channels", "/api/admin/models", "/api/admin/tokens",
	}
	for _, p := range adminPaths {
		if rec := do(srv, "GET", p, "", "Bearer "+jwtB); rec.Code != 200 {
			t.Fatalf("%s 应对任何登录用户开放，实际 %d %s", p, rec.Code, rec.Body.String())
		}
	}
	// 模型/别名管理端点无角色门禁。
	if rec := do(srv, "POST", "/api/aliases", `{"alias":"tenant-a/alias","canonical_model_id":""}`, "Bearer "+jwtB); rec.Code != 200 {
		t.Fatalf("createAlias 应对任何登录用户开放，实际 %d %s", rec.Code, rec.Body.String())
	}
	// 防火墙运维端点无角色门禁，但 V-36 修复后仅限本机回环（攻防开关非业务能力）：
	// 远程任何登录用户 → 403 ops_loopback_only；回环任何登录用户 → 200（无角色判断）。
	if rec := do(srv, "POST", "/api/firewall/seal", `{}`, "Bearer "+jwtB); rec.Code != 403 {
		t.Fatalf("seal 远程应 403 ops_loopback_only，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := doLoopback(srv, "POST", "/api/firewall/seal", `{}`, "Bearer "+jwtB); rec.Code != 200 {
		t.Fatalf("seal 回环应对任何登录用户开放，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := doLoopback(srv, "POST", "/api/firewall/unlock", `{}`, "Bearer "+jwtB); rec.Code != 200 {
		t.Fatalf("unlock 回环应对任何登录用户开放，实际 %d %s", rec.Code, rec.Body.String())
	}
	// 渠道列表默认返回全部渠道（含他人渠道）。
	chID := createChannel(t, srv, jwtA)
	rec := do(srv, "GET", "/api/channels", "", "Bearer "+jwtB)
	if rec.Code != 200 {
		t.Fatalf("channels: %d %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, v := range asSlice(t, decode(t, rec)["data"]) {
		if asStringMap(t, v)["id"] == chID {
			found = true
		}
	}
	if !found {
		t.Fatal("渠道列表默认未返回他人渠道")
	}
	// 任何登录用户可读取/启停他人渠道。
	if rec := do(srv, "GET", "/api/channels/"+chID, "", "Bearer "+jwtB); rec.Code != 200 {
		t.Fatalf("他人渠道详情应 200，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(srv, "POST", "/api/channels/"+chID+"/status", `{"status":"active"}`, "Bearer "+jwtB); rec.Code != 200 {
		t.Fatalf("他人渠道启停应 200，实际 %d %s", rec.Code, rec.Body.String())
	}
	// 他人令牌也可被任意登录用户吊销。
	rec = do(srv, "POST", "/api/tokens", `{"name":"a-token"}`, "Bearer "+jwtA)
	if rec.Code != 200 {
		t.Fatalf("create token: %d %s", rec.Code, rec.Body.String())
	}
	tid, _ := asStringMap(t, decode(t, rec))["id"].(string)
	if tid == "" {
		t.Fatal("未返回令牌 id")
	}
	if rec := do(srv, "DELETE", "/api/tokens/"+tid, "", "Bearer "+jwtB); rec.Code != 200 {
		t.Fatalf("吊销他人令牌应 200，实际 %d %s", rec.Code, rec.Body.String())
	}
	// JWT 身份仍保留：无 JWT 访问管理端点仍 401。
	if rec := do(srv, "GET", "/api/me", "", ""); rec.Code != 401 {
		t.Fatalf("无 JWT 应 401，实际 %d", rec.Code)
	}
	// 存储中所有用户角色均为 superuser，不存在 admin。
	for _, u := range st.ListUsers() {
		if u.Role != "superuser" {
			t.Fatalf("存在非 superuser 角色: %s=%s", u.Email, u.Role)
		}
	}
}
