// 文件: src/internal/api/firewall_api.go
package api

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"llm-hub/src/internal/middleware"
)

// opsIsLoopback 判断请求是否来自本机回环。
// V-36 修复：自动保护的解锁/封存是攻防开关，不是业务能力；远程任何
// JWT 持有者都可解锁会让攻击者把 lockdown 武器化成持久 DoS 开关
//（先注入触发 423，再自行解锁，循环往复）。收归本机运维通道与
//「全员超级用户」的业务语义不冲突（超级用户指业务能力全开放）。
func opsIsLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// 攻防防火墙运维端点。状态/事件/自检只暴露脱敏后的运行时观测事实，
// 不含任何 api_key / sk- 令牌 / JWT / 密码哈希 / 模型输入输出明文。

func (s *Server) fwStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.FW.Status())
}

func (s *Server) fwEvents(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 {
		n = 50
	}
	events := s.FW.Events(n)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"count":  len(events),
		"data":   events,
		"note":   "all event fields are redacted; no credential plaintext is ever persisted",
	})
}

func (s *Server) fwVerify(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.FW.VerifyData())
}

// 天衡：防火墙运维端点无角色门禁；但解锁/封存仅限本机回环（V-36），
// 远程请求一律 403，防止攻击者触发 lockdown 后自行解除、循环 DoS。
func (s *Server) fwUnlock(w http.ResponseWriter, r *http.Request) {
	if !opsIsLoopback(r) {
		fail(w, r, http.StatusForbidden, "invalid_request_error", "ops_loopback_only",
			"firewall unlock/seal is a local ops channel: request must originate from loopback")
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		fail(w, r, http.StatusUnauthorized, "invalid_request_error", "unauthorized", "authentication required")
		return
	}
	s.FW.Unlock(u.ID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"status": s.FW.Status(),
		"actor":  u.ID,
	})
}

func (s *Server) fwSeal(w http.ResponseWriter, r *http.Request) {
	if !opsIsLoopback(r) {
		fail(w, r, http.StatusForbidden, "invalid_request_error", "ops_loopback_only",
			"firewall unlock/seal is a local ops channel: request must originate from loopback")
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		fail(w, r, http.StatusUnauthorized, "invalid_request_error", "unauthorized", "authentication required")
		return
	}
	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "operator-initiated drill"
	}
	s.FW.Seal(reason)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"status": s.FW.Status(),
		"actor":  u.ID,
	})
}
