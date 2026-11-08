package api

import (
	"net/http"
	"strconv"
	"time"

	"llm-hub/src/internal/grouping"
	"llm-hub/src/internal/redact"
	"llm-hub/src/internal/routingjournal"
)

// routingAPI 暴露「Magpie 原理原生实现」的运行时可观测数据。
// 全部只读、全部真实：数据来自 affinity.json / groups.json / routing/*.jsonl，
// 没有任何预置或编造的示例值；无数据时返回真实空态。

// GET /api/routing/affinity?limit=
func (s *Server) affinityView(w http.ResponseWriter, r *http.Request) {
	limit := atoiDefault(r.URL.Query().Get("limit"), 100)
	if limit <= 0 || limit > 2000 {
		limit = 100
	}
	if s.Affinity == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"available": false, "entries": []interface{}{}, "stats": nil,
		})
		return
	}
	entries := s.Affinity.Snapshot(limit)
	st := s.Affinity.Stats()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"available": true,
		"source":    "affinity.json（会话亲和：多轮会话钉住同一上游以复用 prompt 缓存）",
		"stats":     st,
		"entries":   entries,
		"note":      "亲和是渠道维度的路由决策，不是用户限制：不限制任何用户能发多少请求/多少 token/多少并发。",
	})
}

// GET /api/routing/journal?day=&limit=&summary=1
func (s *Server) routingJournalView(w http.ResponseWriter, r *http.Request) {
	if s.RJ == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"available": false, "days": []string{}, "records": []interface{}{},
		})
		return
	}
	day := r.URL.Query().Get("day")
	limit := atoiDefault(r.URL.Query().Get("limit"), 50)
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	recs, err := s.RJ.Query(day, limit)
	if err != nil {
		fail(w, r, http.StatusInternalServerError, "api_error", "journal_read_failed", err.Error())
		return
	}
	days, _ := s.RJ.Days()
	if days == nil {
		days = []string{}
	}
	if day == "" {
		day = time.Now().Format("2006-01-02")
	}
	out := map[string]interface{}{
		"available": true,
		"source":    "routing/YYYY-MM-DD.jsonl（每次路由决策的 order/tries/affinity/masking/prompt 全量留痕）",
		"day":       day,
		"days":      days,
		"count":     len(recs),
		"records":   recs,
	}
	if r.URL.Query().Get("summary") != "" {
		all, _ := s.RJ.Query(day, 0)
		out["summary"] = routingjournal.Summarize(day, all)
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /api/routing/groups
func (s *Server) groupsView(w http.ResponseWriter, r *http.Request) {
	if s.Groups != nil {
		// 渠道/模型是运行时众包变化的，这里按需把新出现的真实模型补成分组
		// （AutoDerive 幂等：已存在的分组不会被覆盖，运维手写规则始终优先）。
		s.refreshGroups()
	}
	if s.Groups == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"available": false, "groups": []interface{}{}})
		return
	}
	list := s.Groups.List()
	type row struct {
		grouping.Group
		Expanded int `json:"expanded_members"`
	}
	out := make([]row, 0, len(list))
	for _, g := range list {
		n := len(g.Members)
		if m, err := s.Groups.Members(g.ID); err == nil {
			n = len(m)
		}
		out = append(out, row{Group: g, Expanded: n})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"available": true,
		"source":    "groups.json（逻辑分组 + 条件规则；不存在时按真实模型 id 自动派生）",
		"count":     len(out),
		"groups":    out,
	})
}

// GET /api/routing/resolve?model=&effort=&images=&tools=&stream=&input_tokens=
// 解析一次假设请求会命中哪个成员 —— 便于运维核对规则，不产生任何真实调用。
func (s *Server) resolveView(w http.ResponseWriter, r *http.Request) {
	if s.Groups != nil {
		s.refreshGroups()
	}
	if s.Groups == nil {
		fail(w, r, http.StatusServiceUnavailable, "api_error", "groups_unavailable", "分组目录不可用")
		return
	}
	q := r.URL.Query()
	model := q.Get("model")
	if model == "" {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_parameter", "model 必填")
		return
	}
	feat := grouping.Features{
		Effort:      q.Get("effort"),
		HasImages:   q.Get("images") == "1" || q.Get("images") == "true",
		HasTools:    q.Get("tools") == "1" || q.Get("tools") == "true",
		Stream:      q.Get("stream") == "1" || q.Get("stream") == "true",
		InputTokens: atoiDefault(q.Get("input_tokens"), 0),
	}
	sel, err := s.Groups.Resolve(model, feat)
	if err != nil {
		fail(w, r, http.StatusNotFound, "invalid_request_error", "not_found", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"available": true, "features": feat, "selection": sel,
		"note": "规则只把指定成员提到最前，其余候选保留（保住失败切换能力）。",
	})
}

// GET /api/routing/redact
func (s *Server) redactPolicyView(w http.ResponseWriter, r *http.Request) {
	pol := redact.DefaultPolicy()
	pol.Enabled = s.RedactOn
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"available": true,
		"source":    "redact 包三级脱敏：known（已知值精确匹配）/ model（本地结构识别器）/ rule（确定性规则）",
		"policy":    pol,
		"kinds":     redact.AllKinds,
		"tiers":     []string{redact.TierKnown, redact.TierModel, redact.TierRule},
		"model_name": pol.ModelName,
		"note": "tier=model 的原语是天衡本地结构识别器（确定性代码，不调用外部模型）；" +
			"需要通用 NER 时可注入自定义 Recognizer。出站遮蔽、入站还原，保证功能不受影响。",
	})
}

// POST /api/routing/redact/preview  body={"text":"..."}
// 用真实策略对一段文本做一次脱敏预演，返回遮蔽结果与真实统计（不落审计、不外发）。
func (s *Server) redactPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	pol := redact.DefaultPolicy()
	pol.Enabled = s.RedactOn
	m := redact.NewMasker(pol)
	masked, rep := m.Mask(in.Text)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"masked":   masked,
		"restored": m.Restore(masked),
		"report":   rep,
		"counted":  true,
	})
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// refreshGroups 把「真实渠道 × 真实上游模型」补成分组（幂等）。
// 返回本次新增的分组数。它是天衡「零配置也能用」的实现：用户只填 base_uri + key，
// 分组由系统按真实模型 id 自动成组，不需要任何人手写。
func (s *Server) refreshGroups() int {
	if s.Groups == nil || s.St == nil {
		return 0
	}
	rows := make([]grouping.Row, 0, 64)
	for _, m := range s.St.ListModels() {
		ch, ok := s.St.GetChannel(m.ChannelID)
		if !ok {
			continue
		}
		rows = append(rows, grouping.Row{
			ChannelID: ch.ID, ProviderType: ch.ProviderType,
			UpstreamModelID: m.UpstreamID, Alias: m.Alias,
		})
	}
	n := s.Groups.AutoDerive(rows)
	if n > 0 {
		_, _ = s.Groups.Save(s.Cfg.DataDir)
	}
	return n
}
