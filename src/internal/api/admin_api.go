package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"llm-hub/src/internal/middleware"
	"llm-hub/src/internal/store"
)

// F10/F11：用量与日志默认按当前用户收窄；`?all=1` 仅 admin/superuser 生效。
func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	userID := u.ID
	if r.URL.Query().Get("all") == "1" && isAdminRole(u.Role) {
		userID = ""
	}
	rows := s.St.ListUsage(store.UsageFilter{
		From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to"),
		UserID: userID, GroupBy: r.URL.Query().Get("group_by"),
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": rows})
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := 0
	if v := q.Get("status"); v != "" {
		status, _ = strconv.Atoi(v)
	}
	page := 1
	if v := q.Get("page"); v != "" {
		page, _ = strconv.Atoi(v)
	}
	f := store.LogFilter{
		RequestID: q.Get("request_id"), Alias: q.Get("alias"),
		Status: status, Keyword: q.Get("q"), Page: page, PageSize: 50,
	}
	// F10：非管理员只能检索自己的日志；管理员默认看全量，可用 ?user_id= 定向。
	if !isAdminRole(middleware.UserFrom(r.Context()).Role) {
		f.UserID = middleware.UserFrom(r.Context()).ID
	} else if v := q.Get("user_id"); v != "" {
		f.UserID = v
	}
	rows, total := s.St.ListRequestLogs(f)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list", "data": rows, "total": total, "page": page,
	})
}

func (s *Server) adminDashboard(w http.ResponseWriter, r *http.Request) {
	c := s.St.Counts()
	allLogs, total := s.St.ListRequestLogs(store.LogFilter{PageSize: 1000000})
	okCount := 0
	statusDist := map[string]int{}
	modelCount := map[string]int{}
	for _, l := range allLogs {
		if l.StatusCode >= 200 && l.StatusCode < 300 {
			okCount++
		}
		label := "other"
		if l.StatusCode >= 200 && l.StatusCode < 300 {
			label = "2xx"
		} else if l.StatusCode >= 400 && l.StatusCode < 500 {
			label = "4xx"
		} else if l.StatusCode >= 500 {
			label = "5xx"
		}
		statusDist[label]++
		modelCount[l.Alias]++
	}
	successRate := 0.0
	if total > 0 {
		successRate = float64(okCount) / float64(total)
	}
	usage := s.St.ListUsage(store.UsageFilter{})
	top := []map[string]interface{}{}
	for _, u := range usage {
		top = append(top, map[string]interface{}{"alias": u.Alias, "requests": u.Requests, "cost_usd": u.CostUSD})
	}
	healthMatrix := []map[string]interface{}{}
	for _, ch := range s.St.ListChannels() {
		healthMatrix = append(healthMatrix, map[string]interface{}{
			"id": ch.ID, "name": ch.Name, "provider_type": ch.ProviderType,
			"status": ch.Status, "probe_status": ch.ProbeStatus,
			"fail_count": ch.FailCount, "last_error": ch.LastError,
			"healthy": ch.Status == "active" && ch.FailCount == 0,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"counts":                c,
		"requests_total":        total,
		"success_rate":          successRate,
		"status_distribution":   statusDist,
		"model_request_counts":  modelCount,
		"top_models":            top,
		"channel_health_matrix": healthMatrix,
		"real_models_source":    "data/models-real.json",
		"generated_at":          time.Now().Unix(),
	})
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	out := []map[string]interface{}{}
	for _, u := range s.St.ListUsers() {
		out = append(out, userView(u))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": out})
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	u, ok := s.St.GetUserByID(id)
	if !ok {
		fail(w, r, http.StatusNotFound, "invalid_request_error", "not_found", "user not found")
		return
	}
	// F11：管理员可改角色（superuser/admin/user）。
	var in struct {
		Role       string   `json:"role"`
		Status     string   `json:"status"`
		BalanceUSD *float64 `json:"balance_usd"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Role != "" {
		switch in.Role {
		case "superuser", "admin", "user":
		default:
			fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_role",
				"role must be one of superuser|admin|user")
			return
		}
		if u.Role == "superuser" && in.Role != "superuser" && s.St.CountRole("superuser") <= 1 {
			fail(w, r, http.StatusBadRequest, "invalid_request_error", "last_superuser",
				"cannot demote the last remaining superuser")
			return
		}
		u.Role = in.Role
	}
	if in.Status == "active" || in.Status == "disabled" {
		u.Status = in.Status
	}
	if in.BalanceUSD != nil {
		u.BalanceUSD = *in.BalanceUSD
	}
	u.UpdatedAt = time.Now().Unix()
	writeJSON(w, http.StatusOK, userView(u))
}

func (s *Server) adminGetSettings(w http.ResponseWriter, r *http.Request) {
	out := map[string]interface{}{}
	for _, st := range s.St.ListSettings() {
		var v interface{}
		_ = json.Unmarshal(st.Value, &v)
		out[st.Key] = v
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "map", "data": out})
}

func (s *Server) adminPutSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]json.RawMessage
	if !decodeJSON(w, r, &in) {
		return
	}
	for k, v := range in {
		s.St.SetSetting(k, v)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "updated": len(in)})
}

func (s *Server) adminChannels(w http.ResponseWriter, r *http.Request) {
	out := []map[string]interface{}{}
	for _, ch := range s.St.ListChannels() {
		v := channelView(ch)
		v["models_discovered"] = len(s.St.ListModelsByChannel(ch.ID))
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": out})
}

func (s *Server) adminModels(w http.ResponseWriter, r *http.Request) {
	s.listModels(w, r)
}

func (s *Server) adminTokens(w http.ResponseWriter, r *http.Request) {
	out := []map[string]interface{}{}
	for _, t := range s.St.ListTokens() {
		out = append(out, tokenView(t))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": out})
}
