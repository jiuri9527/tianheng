package api

import (
	"net/http"

	"llm-hub/src/internal/channel"
	"llm-hub/src/internal/middleware"
	"llm-hub/src/internal/store"
)

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) {
	var in channel.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	u := middleware.UserFrom(r.Context())
	res, err := s.Channels.Create(r.Context(), u.ID, in)
	if err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "channel_create_failed", err.Error())
		return
	}
	_ = s.St.Save()
	view := channelView(res.Channel)
	view["models_discovered"] = res.ModelsDiscovered
	view["connection_ok"] = res.ConnectionOK
	view["connection_error"] = res.ConnectionError
	view["key_hint"] = res.Channel.KeyHint
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	// F2 多租户：管理员看全量，普通用户看「本人渠道 + 公开渠道」。
	u := middleware.UserFrom(r.Context())
	owner := ""
	if !isAdminRole(u.Role) {
		owner = u.ID
	}
	status := r.URL.Query().Get("status")
	out := []map[string]interface{}{}
	seen := map[string]bool{}
	appendCh := func(ch *store.Channel) {
		if seen[ch.ID] {
			return
		}
		if status != "" && ch.Status != status {
			return
		}
		seen[ch.ID] = true
		view := channelView(ch)
		view["models_discovered"] = len(s.St.ListModelsByChannel(ch.ID))
		out = append(out, view)
	}
	for _, ch := range s.Channels.List(owner) {
		appendCh(ch)
	}
	if owner != "" {
		for _, ch := range s.Channels.List("") {
			if ch.Visibility == "public" {
				appendCh(ch)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": out})
}

func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.authorizeChannel(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	view := channelView(ch)
	models := []map[string]interface{}{}
	for _, m := range s.St.ListModelsByChannel(ch.ID) {
		models = append(models, modelView(m))
	}
	view["models"] = models
	view["models_discovered"] = len(models)
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) updateChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.authorizeChannel(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	var in channel.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	updated, err := s.Channels.Update(ch.ID, in)
	if err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "channel_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, channelView(updated))
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.authorizeChannel(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	s.Channels.Delete(ch.ID)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "id": ch.ID})
}

func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.authorizeChannel(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	okConn, latency, errMsg := s.Channels.Test(r.Context(), ch.ID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": okConn, "latency_ms": latency, "error": errMsg,
	})
}

func (s *Server) syncChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.authorizeChannel(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	res, err := s.Channels.Sync(r.Context(), ch.ID)
	if err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "sync_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"added": res.Discovered, "updated": 0, "removed": 0,
		"ok": res.OK, "error": res.Error, "models_discovered": res.Discovered,
	})
}

func (s *Server) probeChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.authorizeChannel(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	probed, failed, err := s.Channels.Probe(r.Context(), ch.ID)
	if err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "probe_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"probed": probed, "failed": failed})
}

func (s *Server) setChannelStatus(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.authorizeChannel(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	updated, err := s.Channels.SetStatus(ch.ID, in.Status)
	if err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "status_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, channelView(updated))
}
