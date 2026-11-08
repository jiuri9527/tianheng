package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"bytes"

	"llm-hub/kernel"
	"llm-hub/src/internal/adapter"
	"llm-hub/src/internal/ctxbreakdown"
	"llm-hub/src/internal/grouping"
	"llm-hub/src/internal/metering"
	"llm-hub/src/internal/middleware"
	"llm-hub/src/internal/models"
	"llm-hub/src/internal/redact"
	"llm-hub/src/internal/relay"
	"llm-hub/src/internal/routingjournal"
	"llm-hub/src/internal/store"
)

// ---------- /healthz /metrics ----------

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	list, err := models.Load()
	c := s.St.Counts()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":             "ok",
		"time":               nowUnix(),
		"real_models":        len(list),
		"real_models_source": "data/models-real.json",
		"channels":           c.Channels,
		"discovered_models":  c.Models,
		"users":              c.Users,
	})
	_ = err
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	c := s.St.Counts()
	var b strings.Builder
	b.WriteString("# HELP llmhub_requests_total Total requests by model/channel/status.\n")
	b.WriteString("# TYPE llmhub_requests_total counter\n")
	collectMetrics(&b)
	fmt.Fprintf(&b, "# HELP llmhub_channel_health Channel health (1 active,0 otherwise).\n# TYPE llmhub_channel_health gauge\n")
	for _, ch := range s.St.ListChannels() {
		v := 0
		if ch.Status == "active" && ch.FailCount == 0 {
			v = 1
		}
		fmt.Fprintf(&b, "llmhub_channel_health{channel=%q,provider=%q} %d\n", ch.ID, ch.ProviderType, v)
	}
	fmt.Fprintf(&b, "llmhub_models_real_total %d\n", len(mustRealModels()))
	fmt.Fprintf(&b, "llmhub_channels_total %d\n", c.Channels)
	fmt.Fprintf(&b, "llmhub_users_total %d\n", c.Users)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, b.String())
}

func mustRealModels() []models.RealModel {
	l, _ := models.Load()
	return l
}

// ---------- /v1/models：真实模型池 ----------

func (s *Server) v1Models(w http.ResponseWriter, r *http.Request) {
	list, err := models.OpenAIList()
	if err != nil {
		fail(w, r, http.StatusInternalServerError, "upstream_error", "models_load_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   list,
		"source": "data/models-real.json",
	})
}

// v1Model GET /v1/models/{id} —— OpenAI 兼容单模型查询。
// 只返回 data/models-real.json 中逐字存在的真实条目；未知 id 返回 404 model_not_found，
// 绝不合成或编造模型对象。
func (s *Server) v1Model(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "missing_model_id", "model id required")
		return
	}
	m, ok, err := models.FindByID(id)
	if err != nil {
		fail(w, r, http.StatusInternalServerError, "upstream_error", "models_load_error", err.Error())
		return
	}
	if !ok {
		fail(w, r, http.StatusNotFound, "invalid_request_error", "model_not_found",
			"model \""+id+"\" not found in data/models-real.json")
		return
	}
	writeJSON(w, http.StatusOK, models.OpenAIObject(m))
}

// ---------- 数据面通用反代理 + failover ----------

type relayParams struct {
	canonical    []byte
	wantFormat   string
	isStream     bool
	pathOverride string
	useRaw       bool
	entrypoint   string
}

func (s *Server) v1Chat(w http.ResponseWriter, r *http.Request) {
	body := middleware.BodyFrom(r.Context())
	s.doRelay(w, r, relayParams{
		canonical: body, wantFormat: "openai",
		isStream: bodyStream(body), entrypoint: "/v1/chat/completions",
	})
}

func (s *Server) v1Messages(w http.ResponseWriter, r *http.Request) {
	body := middleware.BodyFrom(r.Context())
	canonical, wantFormat, err := adapter.CanonicalizeIncoming("/v1/messages", body)
	if err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "bad_messages_request", err.Error())
		return
	}
	s.doRelay(w, r, relayParams{
		canonical: canonical, wantFormat: wantFormat,
		isStream: bodyStream(body), entrypoint: "/v1/messages",
	})
}

// v1Raw 面向 embeddings/images/audio/responses/completions：不做协议转换，按能力位路径转发。
func (s *Server) v1Raw(path string, stream bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := middleware.BodyFrom(r.Context())
		s.doRelay(w, r, relayParams{
			canonical: body, wantFormat: "openai", isStream: stream,
			pathOverride: path, useRaw: true, entrypoint: r.URL.Path,
		})
	}
}

func bodyStream(body []byte) bool {
	var v struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return false
	}
	return v.Stream
}

func modelFromBody(body []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &v)
	return v.Model
}

// doRelay 实现 F6/F8：别名解析 -> 候选渠道 -> MAX_FAILOVER 切换 -> 反代理 -> 计量。
// 无上游凭据/全部失败时返回统一 502 upstream_error，绝不伪造模型响应内容。
func (s *Server) doRelay(w http.ResponseWriter, r *http.Request, p relayParams) {
	start := time.Now()
	rid := middleware.RequestIDFrom(r.Context())
	tok := middleware.TokenFrom(r.Context())
	user := middleware.UserFrom(r.Context())
	model := modelFromBody(p.canonical)
	if model == "" {
		model = modelFromBody(middleware.BodyFrom(r.Context()))
	}
	clientIP := middleware.ClientIPFrom(r.Context())

	// ── Magpie 原理①：会话标识 ─────────────────────────────────────────
	// 多轮对话没有显式会话 id，用请求头里的 X-Tianheng-Conv 优先，
	// 否则按 (用户, 逻辑模型) 稳定派生 —— 同一用户同一模型的连续请求
	// 会被识别为同一会话，从而触发亲和与缓存复用。
	conv := r.Header.Get("X-Tianheng-Conv")
	if conv == "" {
		conv = user.ID + "|" + model
	}

	// ── Magpie 原理②：分组解析 + 条件规则 ───────────────────────────────
	var groupSnap routingjournal.GroupSnapshot
	effectiveModel := model
	if s.Groups != nil {
		s.refreshGroups() // 渠道/模型是运行时变化的，先补齐自动分组（幂等）
		feat := grouping.Features{
			Effort:      effortFromBody(p.canonical),
			HasImages:   bodyHasImages(p.canonical),
			HasTools:    bodyHasTools(p.canonical),
			Stream:      p.isStream,
			InputTokens: ctxbreakdown.Analyze(p.canonical, model, 0, nil).Tokens,
		}
		if sel, found, err := s.Groups.ResolveOrAuto(model, feat); err == nil && found {
			groupSnap = routingjournal.GroupSnapshot{
				ID: sel.GroupID, Ref: sel.GroupRef, Name: sel.GroupName,
				Affinity: sel.Affinity, Members: sel.Members,
				Rules: sel.Rules, Why: sel.Why,
			}
			// 分组成员优先参与路由；组不存在时退回裸模型路由。
			if len(sel.Members) > 0 && sel.Why != "no_members" {
				effectiveModel = groupFirstModel(sel.Members)
			}
		} else {
			// 未命中任何分组：如实记录（便于运维看出「这个模型还没被成组」）。
			groupSnap = routingjournal.GroupSnapshot{Ref: model, Why: "ungrouped"}
		}
	}

	// ── Magpie 原理③：会话亲和（粘住上游复用 prompt 缓存）────────────
	var pinnedChannel *store.Channel
	affDecisionJSON := json.RawMessage(nil)
	if s.Affinity != nil {
		probe := func(id string) (string, string, bool) {
			ch, ok := s.St.GetChannel(id)
			if !ok || ch.Status != "active" {
				return "", "", false
			}
			if time.Now().Unix() < ch.CooldownUntil {
				return ch.Name, "", false
			}
			return ch.Name, "", true
		}
		mode := groupSnap.Affinity
		d := s.Affinity.Decide(conv, model, mode, probe, time.Now())
		if d.Kept {
			if ch, ok := s.St.GetChannel(d.Last); ok {
				pinnedChannel = ch
			}
		}
		affDecisionJSON, _ = json.Marshal(d)
	}

	// ── Magpie 原理④：上下文构成（估算口径，counted=false 如实标注）──
	promptBreakdown := ctxbreakdown.Analyze(p.canonical, model, s.windowFor(model), nil)
	promptJSON, _ := json.Marshal(promptBreakdown)

	// ── Magpie 原理⑤：出站脱敏（三级：known / model / rule）────────────
	var masker *redact.Masker
	maskReportJSON := json.RawMessage(nil)
	outbound := p.canonical // 默认原文；脱敏开启后被替换
	if s.RedactOn {
		masker = redact.NewMasker(redact.DefaultPolicy())
		masker.SetKey(rid)
		// tier=known：本请求里出现的已知敏感值（渠道密钥、上游账号）
		for _, m := range s.St.ListModels() {
			if ch, ok := s.St.GetChannel(m.ChannelID); ok {
				_ = ch // 密钥是密文存储；明文 only in relay attempt，见下
			}
		}
		masked, rep := masker.Mask(string(p.canonical))
		outbound = []byte(masked)
		maskReportJSON, _ = json.Marshal(rep)
	}

	// ── 路由决策日志（order + tries 全量留痕）──────────────────────────
	rjRec := routingjournal.Record{
		Time: start.Format(time.RFC3339Nano), Key: rid,
		Agent: agentFromUA(r.Header.Get("User-Agent")), Entry: p.entrypoint,
		Model: model, Effort: effortFromBody(p.canonical),
		Group: groupSnap, Order: []routingjournal.OrderEntry{},
		Tries: []routingjournal.Try{}, Conv: conv,
		Affinity: affDecisionJSON, Masking: maskReportJSON, Prompt: promptJSON,
		EstTok: promptBreakdown.Tokens, Stream: p.isStream,
	}

	maxFailover := s.Cfg.MaxFailover
	if maxFailover <= 0 {
		maxFailover = 3
	}
	var lastErr string
	var lastStatus int
	attempts := 0
	for attempts < maxFailover {
		var ch *store.Channel
		// 亲和钉住的渠道优先（仅当它此刻仍可用且能提供该模型）
		if pinnedChannel != nil && attempts == 0 {
			ch = pinnedChannel
			rjRec.Order = append(rjRec.Order, routingjournal.OrderEntry{
				Rank: 1, ChannelID: ch.ID, ChannelNm: ch.Name, Provider: ch.ProviderType,
				Note: "affinity-pinned",
			})
		} else {
			picked, err := s.Router.Pick(effectiveModel, user.ID)
			if err != nil {
				break // 无候选渠道（无上游凭据）
			}
			ch = picked
			rjRec.Order = append(rjRec.Order, routingjournal.OrderEntry{
				Rank: len(rjRec.Order) + 1, ChannelID: ch.ID, ChannelNm: ch.Name,
				Provider: ch.ProviderType, Weight: ch.Weight, Priority: ch.Priority,
			})
		}
		attempts++
		tryStart := time.Now()
		var a *relay.Attempt
		var err error
		if p.useRaw {
			a, err = s.Relay.BuildRawAttempt(ch, middleware.BodyFrom(r.Context()), p.pathOverride)
		} else if masker != nil {
			// 出站脱敏后的 canonical 进入真实上游请求。
			a, err = s.Relay.BuildAttempt(ch, outbound)
		} else {
			a, err = s.Relay.BuildAttempt(ch, p.canonical)
		}
		if err != nil {
			s.Router.Report(ch, kernel.Result{OK: false, Err: err})
			lastErr = err.Error()
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), s.Cfg.DefaultTimeout)
		resp, err := s.Relay.Send(ctx, a)
		if err != nil {
			cancel()
			s.Router.Report(ch, kernel.Result{OK: false, Err: err})
			lastErr = err.Error()
			rjRec.Tries = append(rjRec.Tries, routingjournal.Try{
				ChannelID: ch.ID, Start: tryStart.Format(time.RFC3339Nano),
				Done: false, Status: 0, Ms: int(time.Since(tryStart).Milliseconds()),
				Err: err.Error(),
			})
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			cancel()
			lastStatus = resp.StatusCode
			lastErr = fmt.Sprintf("upstream HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
			s.Router.Report(ch, kernel.Result{OK: false, Status: resp.StatusCode})
			rjRec.Tries = append(rjRec.Tries, routingjournal.Try{
				ChannelID: ch.ID, Start: tryStart.Format(time.RFC3339Nano),
				Done: false, Status: resp.StatusCode, Ms: int(time.Since(tryStart).Milliseconds()),
				Err: lastErr,
			})
			continue
		}
		// 2xx：转发给客户端（流式零缓冲 / 非流式协议转换）
		outcome := s.Relay.Forward(ctx, w, resp, a, p.wantFormat, p.isStream, start)
		cancel()
		ok := outcome.Err == nil

		// 成功路径：绑定亲和（粘住这个上游）+ 回写缓存观测 + 记录成功 try。
		if ok && s.Affinity != nil {
			s.Affinity.Bind(conv, model, ch.ID, ch.Name, a.Channel.Name, time.Now())
			// 缓存用量只记上游真实回报的值；上游没报就保持 0，不做任何推测。
			cw, cr := cacheUsageFromOutcome(*outcome)
			s.Affinity.ObserveCache(conv, model, cw, cr)
			if cw > 0 || cr > 0 {
				// 把真实缓存数字写进决策日志，便于核算亲和带来的实际收益。
				if b, err := json.Marshal(map[string]int64{"cache_write": cw, "cache_read": cr}); err == nil {
					rjRec.Prompt = appendJSONField(rjRec.Prompt, "cache", b)
				}
			}
		}
		rjRec.Tries = append(rjRec.Tries, routingjournal.Try{
			ChannelID: ch.ID, Model: model, Start: tryStart.Format(time.RFC3339Nano),
			Done: true, Status: http.StatusOK,
			Ms: int(time.Since(tryStart).Milliseconds()), TTFTms: outcome.TTFTMs,
		})
		rjRec.Status = http.StatusOK
		rjRec.Channel = ch.ID
		rjRec.Provider = ch.ProviderType
		rjRec.Tokens = outcome.PromptTokens + outcome.CompletionTokens
		rjRec.Out = outcome.CompletionTokens
		rjRec.TTFT = outcome.TTFTMs
		rjRec.Ms = int(time.Since(start).Milliseconds())
		rjRec.Bytes = len(p.canonical)
		if s.RJ != nil {
			_, _ = s.RJ.Append(rjRec)
		}

		s.Router.Report(ch, kernel.Result{OK: ok, Status: http.StatusOK, Latency: time.Since(start)})
		s.recordUsage(r, start, outcome, model, ch, tok, user, clientIP, rid, ok)
		return
	}

	// 全部尝试失败也留痕（线上复盘需要知道失败链）。
	rjRec.Status = lastStatus
	rjRec.Ms = int(time.Since(start).Milliseconds())
	rjRec.Bytes = len(p.canonical)
	if s.RJ != nil {
		_, _ = s.RJ.Append(rjRec)
	}

	// 全部失败：统一 upstream_error，绝无伪造内容
	msg := fmt.Sprintf("no usable upstream channel for model %q", model)
	if attempts > 0 {
		msg = fmt.Sprintf("all %d failover attempts failed for model %q", attempts, model)
	}
	if lastErr != "" {
		msg += ": " + lastErr
	}
	if lastStatus != 0 && lastErr == "" {
		msg += fmt.Sprintf(": HTTP %d", lastStatus)
	}
	fail(w, r, http.StatusBadGateway, "upstream_error", "no_upstream_channel", msg)
	s.Meter.Record(&metering.Record{
		RequestID: rid, UserID: user.ID, TokenID: tok.ID, Alias: model,
		Stream: p.isStream, RequestBody: p.canonical, ResponseBody: "",
		StatusCode: http.StatusBadGateway, ErrorCode: "upstream_error",
		ClientIP: clientIP, Token: tok, LatencyMs: int(time.Since(start).Milliseconds()),
	})
}

func (s *Server) recordUsage(r *http.Request, start time.Time, outcome *relay.Outcome,
	model string, ch *store.Channel, tok *store.Token, user *store.User,
	clientIP, rid string, ok bool) {

	statusCode := http.StatusOK
	errCode := ""
	if !ok {
		statusCode = http.StatusBadGateway
		errCode = "upstream_error"
	}
	rec := &metering.Record{
		RequestID: rid, UserID: user.ID, TokenID: tok.ID, Alias: model,
		ChannelID: ch.ID, ProviderType: ch.ProviderType, Stream: outcome.Stream,
		RequestBody: middleware.BodyFrom(r.Context()), ResponseBody: outcome.OutputText,
		PromptTokens: outcome.PromptTokens, CompletionTokens: outcome.CompletionTokens,
		TTFTMs: outcome.TTFTMs, LatencyMs: outcome.LatencyMs,
		StatusCode: statusCode, ErrorCode: errCode, ClientIP: clientIP,
		// F9/F10：令牌参与配额扣减与用量归属，额度耗尽后后续请求应 402。
		Token: tok,
	}
	// 找到命中模型与贡献奖励归属
	for _, m := range s.St.ListModelsByChannel(ch.ID) {
		if m.Alias == model || modelFromBody(middleware.BodyFrom(r.Context())) == m.Alias {
			rec.ModelID = m.ID
			break
		}
	}
	if ch.Visibility == "public" && ch.OwnerID != user.ID {
		rec.ContributorUserID = ch.OwnerID
		rec.RewardRate = s.Cfg.ContributorRewardRate
	}
	s.Meter.Record(rec)
}

// ── Magpie 原理接线的辅助函数（纯函数、零第三方依赖）───────────────────

// windowFor 返回逻辑模型对应的真实上下文窗口（来自真实数据集；未知返回 0）。
func (s *Server) windowFor(model string) int {
	if id, ok, _ := models.FindByID(strings.TrimPrefix(model, "auto/model/")); ok {
		return id.ContextLength
	}
	return 0
}

// groupFirstModel 从分组成员表达式取第一个成员的模型部分（"渠道/模型" → 模型）。
func groupFirstModel(members []string) string {
	for _, m := range members {
		if i := strings.IndexByte(m, '/'); i >= 0 && i < len(m)-1 {
			return m[i+1:]
		}
	}
	return ""
}

// effortFromBody 取规范化请求体里的推理档位（无则为空）。
func effortFromBody(raw []byte) string {
	var b struct {
		ReasoningEffort string `json:"reasoning_effort"`
		Effort          string `json:"effort"`
	}
	if json.Unmarshal(raw, &b) != nil {
		return ""
	}
	if b.ReasoningEffort != "" {
		return b.ReasoningEffort
	}
	return b.Effort
}

// bodyHasImages 判断请求体是否含图片分块（多模态）。
func bodyHasImages(raw []byte) bool {
	return bytes.Contains(raw, []byte(`"type":"image"`)) ||
		bytes.Contains(raw, []byte(`"type": "image"`)) ||
		bytes.Contains(raw, []byte(`"image_url"`))
}

// bodyHasTools 判断请求体是否声明了工具。
func bodyHasTools(raw []byte) bool {
	return bytes.Contains(raw, []byte(`"tools"`))
}

// agentFromUA 把 User-Agent 归一成短调用方标识（决策日志的 agent 字段）。
func agentFromUA(ua string) string {
	lower := strings.ToLower(ua)
	for _, k := range []string{"claude", "codex", "cline", "cursor", "dsh", "python", "node", "curl", "go-http"} {
		if strings.Contains(lower, k) {
			return k
		}
	}
	if ua == "" {
		return "unknown"
	}
	if i := strings.IndexByte(ua, '/'); i > 0 {
		return strings.ToLower(ua[:i])
	}
	if len(ua) > 12 {
		ua = ua[:12]
	}
	return strings.ToLower(ua)
}

// cacheUsageFromOutcome 从真实上游响应里提取缓存用量（写入/命中）。
// 只记真实值：上游没报缓存就返回 0/0，不做任何推测。
// 兼容 OpenAI（prompt_tokens_details.cached_tokens）与 Anthropic
// （cache_creation_input_tokens / cache_read_input_tokens）两种形状。
func cacheUsageFromOutcome(o relay.Outcome) (write, read int64) {
	var u struct {
		Usage struct {
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	// 优先解析完整响应原文（含 usage）；回退到正文以兼容只回显正文的路径。
	raw := o.RawCanonical
	if len(raw) == 0 {
		raw = []byte(o.OutputText)
	}
	if json.Unmarshal(raw, &u) != nil {
		return 0, 0
	}
	write = int64(u.Usage.CacheCreationInputTokens)
	read = int64(u.Usage.CacheReadInputTokens)
	if read == 0 {
		read = int64(u.Usage.PromptTokensDetails.CachedTokens)
	}
	return write, read
}

// appendJSONField 在已有 JSON 对象里追加一个字段（用于把缓存数字并入 prompt 统计）。
func appendJSONField(base json.RawMessage, key string, val json.RawMessage) json.RawMessage {
	m := map[string]json.RawMessage{}
	if len(base) > 0 {
		_ = json.Unmarshal(base, &m)
	}
	m[key] = val
	out, err := json.Marshal(m)
	if err != nil {
		return base
	}
	return out
}
