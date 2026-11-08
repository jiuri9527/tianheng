package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"llm-hub/src/internal/middleware"
	"llm-hub/src/internal/store"
)

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	u, tok, err := s.Auth.Register(in.Email, in.Password, s.Cfg.RegistrationOpen, s.Cfg.AdminEmail)
	if err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "register_failed", err.Error())
		return
	}
	_ = s.St.Save()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user": userView(u), "tokens": tok,
	})
}

// 登录失败限流（F13）：每 (client_ip, email) 窗口内最多 5 次失败尝试。
var loginFails = struct {
	sync.Mutex
	m map[string]*loginFailRecord
}{m: map[string]*loginFailRecord{}}

type loginFailRecord struct {
	count int
	until int64
}

const (
	loginFailWindowSec = 60
	loginFailMax       = 5
	loginFailMaxKeys   = 100000
)

func loginRateLimited(key string) bool {
	loginFails.Lock()
	defer loginFails.Unlock()
	now := time.Now().Unix()
	r := loginFails.m[key]
	if r == nil || now >= r.until {
		if len(loginFails.m) >= loginFailMaxKeys {
			for k, v := range loginFails.m {
				if now >= v.until {
					delete(loginFails.m, k)
				}
			}
		}
		if len(loginFails.m) < loginFailMaxKeys {
			loginFails.m[key] = &loginFailRecord{count: 1, until: now + loginFailWindowSec}
		}
		return false
	}
	r.count++
	return r.count > loginFailMax
}

func resetLoginFails(key string) {
	loginFails.Lock()
	delete(loginFails.m, key)
	loginFails.Unlock()
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	key := middleware.ClientIPFrom(r.Context()) + "|" + strings.ToLower(strings.TrimSpace(in.Email))
	if loginRateLimited(key) {
		fail(w, r, http.StatusTooManyRequests, "rate_limit_error", "login_rate_limited", "too many failed login attempts")
		return
	}
	u, tok, err := s.Auth.Login(in.Email, in.Password)
	if err != nil {
		fail(w, r, http.StatusUnauthorized, "invalid_request_error", "login_failed", err.Error())
		return
	}
	resetLoginFails(key)
	writeJSON(w, http.StatusOK, map[string]interface{}{"user": userView(u), "tokens": tok})
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tok, err := s.Auth.Refresh(in.RefreshToken)
	if err != nil {
		fail(w, r, http.StatusUnauthorized, "invalid_request_error", "refresh_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tok)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	// 无状态 JWT：退出即客户端丢弃令牌；此处返回统一成功体
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	u := middleware.UserFrom(r.Context())
	if err := s.Auth.ChangePassword(u.ID, in.OldPassword, in.NewPassword); err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "password_change_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user": userView(u),
		"counts": map[string]interface{}{
			"channels": len(s.St.ListChannelsByOwner(u.ID)),
			"tokens": len(s.St.ListTokensByUser(u.ID)),
		},
	})
}

func (s *Server) myChannels(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	out := []map[string]interface{}{}
	for _, ch := range s.St.ListChannelsByOwner(u.ID) {
		view := channelView(ch)
		models := []map[string]interface{}{}
		for _, m := range s.St.ListModelsByChannel(ch.ID) {
			models = append(models, modelView(m))
		}
		view["models"] = models
		view["models_discovered"] = len(models)
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": out})
}

func (s *Server) myTokens(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	out := []map[string]interface{}{}
	for _, t := range s.St.ListTokensByUser(u.ID) {
		out = append(out, tokenView(t))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": out})
}

func (s *Server) myUsage(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	rows := s.St.ListUsage(store.UsageFilter{
		From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to"), UserID: u.ID,
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": rows})
}

func (s *Server) myRewards(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	out := []*store.QuotaLedger{}
	for _, l := range s.St.ListLedgerByUser(u.ID) {
		if l.Reason == "contribution_reward" {
			out = append(out, l)
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": out})
}

func (s *Server) myExamples(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	base := s.Cfg.ServerBaseURL + "/v1"
	tokHint := "sk-YOUR_TOKEN"
	model := "liquid/lfm-3-1.7b" // 真实存在于 data/models-real.json
	curl := fmt.Sprintf(`curl %s/chat/completions -H "Authorization: Bearer %s" -H "Content-Type: application/json" -d '{"model":"%s","messages":[{"role":"user","content":"hi"}]}'`, base, tokHint, model)
	python := fmt.Sprintf("from openai import OpenAI\nclient = OpenAI(base_url=%q, api_key=%q)\nresp = client.chat.completions.create(model=%q, messages=[{\"role\":\"user\",\"content\":\"hi\"}])\nprint(resp.choices[0].message.content)", base, tokHint, model)
	anthropic := fmt.Sprintf("import anthropic\nc = anthropic.Anthropic(base_url=%q, api_key=%q)\nm = c.messages.create(model=%q, max_tokens=64, messages=[{\"role\":\"user\",\"content\":\"hi\"}])\nprint(m.content[0].text)", base, tokHint, model)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user": u.ID,
		"base_url": base,
		"model": model,
		"model_source": "data/models-real.json",
		"curl": curl,
		"python_openai_sdk": python,
		"anthropic_sdk": anthropic,
	})
}
