package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"llm-hub/src/internal/models"
	"llm-hub/src/internal/store"
)

// modelView 运行时发现的真实模型视图（含真实输入输出样例）。
func modelView(m *store.Model) map[string]interface{} {
	return map[string]interface{}{
		"id": m.ID, "channel_id": m.ChannelID, "upstream_id": m.UpstreamID,
		"alias": m.Alias, "display_name": m.DisplayName, "provider_type": m.ProviderType,
		"context_length": m.ContextLength, "max_output_tokens": m.MaxOutputTokens,
		"supports_stream": m.SupportsStream, "supports_vision": m.SupportsVision,
		"supports_tools": m.SupportsTools, "supports_reasoning": m.SupportsReasoning,
		"input_price_per_mtok": m.InputPricePerMtok, "output_price_per_mtok": m.OutputPricePerMtok,
		"param_source": m.ParamSource, "raw_json": m.RawJSON,
		"probe_status": m.ProbeStatus, "probe_input": m.ProbeInput,
		"probe_output": m.ProbeOutput, "probe_prompt_tokens": m.ProbePromptTokens,
		"probe_completion_tokens": m.ProbeCompletionTokens, "probe_latency_ms": m.ProbeLatencyMs,
		"probed_at": m.ProbedAt, "enabled": m.Enabled,
	}
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	realList, err := models.Load()
	if err != nil {
		fail(w, r, http.StatusInternalServerError, "upstream_error", "models_load_error", err.Error())
		return
	}
	provider := r.URL.Query().Get("provider")
	capability := r.URL.Query().Get("capability")
	q := strings.ToLower(r.URL.Query().Get("q"))
	catalog := []map[string]interface{}{}
	for _, m := range realList {
		if provider != "" && !strings.Contains(strings.ToLower(m.OwnedBy), strings.ToLower(provider)) &&
			!strings.Contains(strings.ToLower(m.ID), strings.ToLower(provider)) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(m.ID), q) && !strings.Contains(strings.ToLower(m.Name), q) {
			continue
		}
		if capability != "" && !realHasCapability(m, capability) {
			continue
		}
		catalog = append(catalog, map[string]interface{}{
			"id": m.ID, "name": m.Name, "owned_by": m.OwnedBy,
			"context_length":            m.ContextLength,
			"prompt_price_per_mtok":     m.PromptPricePerTok * 1e6,
			"completion_price_per_mtok": m.CompletionPricePerTok * 1e6,
			"modality":                  m.ArchitectureModality,
			"source":                    "data/models-real.json",
		})
	}
	runtime := []map[string]interface{}{}
	for _, m := range s.St.ListModels() {
		runtime = append(runtime, modelView(m))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object":         "list",
		"catalog_source": "data/models-real.json",
		"catalog_count":  len(realList),
		"data":           catalog,
		"runtime_models": runtime,
	})
}

func realHasCapability(m models.RealModel, cap string) bool {
	switch cap {
	case "vision", "image":
		for _, x := range m.InputModalities {
			if strings.Contains(x, "image") || strings.Contains(x, "video") {
				return true
			}
		}
	case "tools":
		for _, p := range m.SupportedParameters {
			if p == "tools" || p == "tool_choice" {
				return true
			}
		}
	case "reasoning":
		for _, p := range m.SupportedParameters {
			if strings.Contains(p, "reasoning") {
				return true
			}
		}
	}
	return false
}

func (s *Server) getModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if m, ok := s.St.GetModel(id); ok {
		probes := []map[string]interface{}{}
		for _, p := range s.St.ListProbesByModel(m.ID) {
			probes = append(probes, map[string]interface{}{
				"id": p.ID, "input": p.Input, "output": p.Output,
				"latency_ms": p.LatencyMs, "ok": p.OK, "error": p.Error,
			})
		}
		view := modelView(m)
		view["probes"] = probes
		writeJSON(w, http.StatusOK, view)
		return
	}
	realList, _ := models.Load()
	for _, m := range realList {
		if m.ID == id {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"id": m.ID, "name": m.Name, "owned_by": m.OwnedBy,
				"context_length": m.ContextLength, "modality": m.ArchitectureModality,
				"source": "data/models-real.json", "catalog_only": true,
			})
			return
		}
	}
	fail(w, r, http.StatusNotFound, "invalid_request_error", "not_found", "model not found")
}

func (s *Server) updateModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Enabled       *bool           `json:"enabled"`
		Alias         string          `json:"alias"`
		PriceOverride json.RawMessage `json:"price_override"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	ok := s.St.UpdateModel(id, func(m *store.Model) {
		if in.Enabled != nil {
			m.Enabled = *in.Enabled
		}
		if in.Alias != "" {
			m.Alias = in.Alias
		}
	})
	if ok && len(in.PriceOverride) > 0 {
		// UpsertAlias 自身会取 store 锁，必须在 UpdateModel 闭包外调用，
		// 否则同一把 sync.RWMutex 被重入导致整个进程死锁（CWE-833）。
		s.St.UpsertAlias(&store.ModelAlias{
			Alias: in.Alias, PriceOverride: in.PriceOverride, Enabled: true,
			CreatedAt: time.Now().Unix(),
		})
	}
	if !ok {
		fail(w, r, http.StatusNotFound, "invalid_request_error", "not_found", "runtime model not found")
		return
	}
	m, _ := s.St.GetModel(id)
	writeJSON(w, http.StatusOK, modelView(m))
}

func (s *Server) probeModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, ok := s.St.GetModel(id)
	if !ok {
		fail(w, r, http.StatusNotFound, "invalid_request_error", "not_found", "runtime model not found")
		return
	}
	ch, ok := s.St.GetChannel(m.ChannelID)
	if !ok {
		fail(w, r, http.StatusNotFound, "invalid_request_error", "not_found", "channel not found")
		return
	}
	res := s.Discoverer.ProbeModel(r.Context(), ch, m)
	m, _ = s.St.GetModel(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": res.OK, "latency_ms": res.LatencyMs, "error": res.Error,
		"model": modelView(m),
	})
}

// ---------- 别名管理 ----------

func (s *Server) listAliases(w http.ResponseWriter, r *http.Request) {
	out := []map[string]interface{}{}
	for _, a := range s.St.ListAliases() {
		out = append(out, map[string]interface{}{
			"alias": a.Alias, "canonical_model_id": a.CanonicalModelID,
			"description": a.Description, "enabled": a.Enabled,
			"price_override": a.PriceOverride, "created_at": a.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": out})
}

func (s *Server) createAlias(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Alias            string          `json:"alias"`
		CanonicalModelID string          `json:"canonical_model_id"`
		Description      string          `json:"description"`
		Enabled          *bool           `json:"enabled"`
		PriceOverride    json.RawMessage `json:"price_override"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Alias == "" {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "alias_required", "alias required")
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	s.St.UpsertAlias(&store.ModelAlias{
		Alias: in.Alias, CanonicalModelID: in.CanonicalModelID,
		Description: in.Description, Enabled: enabled,
		PriceOverride: in.PriceOverride, CreatedAt: time.Now().Unix(),
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "alias": in.Alias})
}

func (s *Server) updateAlias(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("alias")
	a, ok := s.St.GetAlias(name)
	if !ok {
		fail(w, r, http.StatusNotFound, "invalid_request_error", "not_found", "alias not found")
		return
	}
	var in struct {
		Description      string          `json:"description"`
		CanonicalModelID string          `json:"canonical_model_id"`
		Enabled          *bool           `json:"enabled"`
		PriceOverride    json.RawMessage `json:"price_override"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Description != "" {
		a.Description = in.Description
	}
	if in.CanonicalModelID != "" {
		a.CanonicalModelID = in.CanonicalModelID
	}
	if in.Enabled != nil {
		a.Enabled = *in.Enabled
	}
	if len(in.PriceOverride) > 0 {
		a.PriceOverride = in.PriceOverride
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "alias": name})
}

func (s *Server) deleteAlias(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("alias")
	s.St.DeleteAlias(name)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "alias": name})
}
