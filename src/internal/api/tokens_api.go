package api

import (
	"net"
	"net/http"
	"strings"
	"time"

	"llm-hub/src/internal/crypto"
	"llm-hub/src/internal/middleware"
	"llm-hub/src/internal/store"
)

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name           string   `json:"name"`
		AllowedModels  []string `json:"allowed_models"`
		RPM            int      `json:"rpm"`
		TPM            int      `json:"tpm"`
		MaxConcurrency int      `json:"max_concurrency"`
		QuotaUSD       float64  `json:"quota_usd"`
		IPAllowlist    []string `json:"ip_allowlist"`
		ExpiresAt      int64    `json:"expires_at"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	u := middleware.UserFrom(r.Context())
	hexPart, err := crypto.RandomHex(16) // 32 位十六进制
	if err != nil {
		fail(w, r, http.StatusInternalServerError, "invalid_request_error", "token_gen_failed", err.Error())
		return
	}
	sk := "sk-" + hexPart
	// F9：令牌限制字段由调用方设定并在此校验；0 表示该维度不限。
	if in.RPM < 0 || in.TPM < 0 || in.MaxConcurrency < 0 {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_token_limit",
			"rpm / tpm / max_concurrency must not be negative")
		return
	}
	if in.QuotaUSD < 0 {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_token_quota",
			"quota_usd must not be negative")
		return
	}
	if in.ExpiresAt < 0 {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_token_expiry",
			"expires_at must not be negative")
		return
	}
	if in.ExpiresAt > 0 && in.ExpiresAt <= time.Now().Unix() {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_token_expiry",
			"expires_at must be in the future")
		return
	}
	for _, entry := range in.IPAllowlist {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_ip_allowlist",
				"ip_allowlist entries must not be empty")
			return
		}
		if strings.Contains(entry, "/") {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_ip_allowlist",
					"ip_allowlist entry is not a valid CIDR: "+entry)
				return
			}
			continue
		}
		if net.ParseIP(entry) == nil {
			fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_ip_allowlist",
				"ip_allowlist entry is not a valid IP: "+entry)
			return
		}
	}
	// 未显式设定则回落到全局默认额度。
	// 天衡：不设任何使用限制 —— 忽略请求里携带的一切限制值，强制归零（0 = 不限）。
	// 字段保留只为接口形态完整，不参与任何判定。
	in.RPM = 0
	in.TPM = 0
	in.MaxConcurrency = 0
	in.QuotaUSD = 0
	in.ExpiresAt = 0
	in.IPAllowlist = nil
	in.AllowedModels = nil
	if false {
		in.RPM = s.Cfg.RateRPMDefault
	}
	if false {
		in.TPM = s.Cfg.RateTPMDefault
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = "default"
	}
	t := &store.Token{
		ID: store.NewID("tok"), UserID: u.ID, Name: in.Name,
		TokenHash: crypto.TokenHash(sk), KeyHint: crypto.KeyHint(sk),
		AllowedModels: in.AllowedModels, RPM: in.RPM, TPM: in.TPM,
		MaxConcurrency: in.MaxConcurrency, QuotaUSD: in.QuotaUSD,
		IPAllowlist: in.IPAllowlist, ExpiresAt: in.ExpiresAt,
		Status: "active", CreatedAt: time.Now().Unix(),
	}
	s.St.CreateToken(t)
	_ = s.St.Save()
	view := tokenView(t)
	view["token"] = sk // 仅此一次明文返回
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	out := []map[string]interface{}{}
	for _, t := range s.St.ListTokensByUser(u.ID) {
		out = append(out, tokenView(t))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": out})
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, ok := s.St.GetTokenByID(id)
	if !ok {
		fail(w, r, http.StatusNotFound, "invalid_request_error", "not_found", "token not found")
		return
	}
	// F1：普通用户只能吊销本人令牌；admin/superuser 可吊销任意令牌。
	u := middleware.UserFrom(r.Context())
	if t.UserID != u.ID && !isAdminRole(u.Role) {
		fail(w, r, http.StatusForbidden, "invalid_request_error", "forbidden_token",
			"token belongs to another user")
		return
	}
	t.Status = "revoked"
	s.Limiter.Reset(t.ID)
	s.St.DeleteToken(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "id": id})
}

// rotateToken POST /api/tokens/{id}/rotate —— 轮换平台令牌。
// 旧 sk- 立即失效（同一条记录的哈希被覆盖），新明文只在本次响应返回一次。
// 天衡不设任何使用限制，轮换只替换凭证，不引入任何限制字段。
func (s *Server) rotateToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, ok := s.St.GetTokenByID(id)
	if !ok {
		fail(w, r, http.StatusNotFound, "invalid_request_error", "not_found", "token not found")
		return
	}
	hexPart, err := crypto.RandomHex(16)
	if err != nil {
		fail(w, r, http.StatusInternalServerError, "api_error", "token_gen_failed", err.Error())
		return
	}
	sk := "sk-" + hexPart
	rotated, ok := s.St.RotateToken(id, crypto.TokenHash(sk), crypto.KeyHint(sk))
	if !ok {
		fail(w, r, http.StatusInternalServerError, "api_error", "token_rotate_failed", "token disappeared during rotation")
		return
	}
	t = rotated
	_ = s.St.Save()
	view := tokenView(t)
	view["token"] = sk // 仅此一次明文
	view["note"] = "旧 sk- 已立即失效；天衡不设任何使用限制，新令牌可调用全部真实模型"
	writeJSON(w, http.StatusOK, view)
}
