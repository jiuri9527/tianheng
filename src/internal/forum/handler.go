package forum

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"llm-hub/src/internal/auth"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/middleware"
	"llm-hub/src/internal/relay"
	"llm-hub/src/internal/store"
)

// handler 聚合论坛依赖。st 用于二次核验当前用户仍是 store 中的真实活跃用户；
// 论坛内容本身完全来自独立 forum.json，不读写 store 的任何表。
type handler struct {
	st   *store.Store
	cfg  *config.Config
	auth *auth.Service
	fs   *fileStore
}

// Register 在 Lead 传入的 *http.ServeMux 上注册 8 个论坛端点。
//
// 禁止改 src/internal/api/server.go：本函数由 Lead 在 buildMux() 内接线。
// 包内自行用 middleware.Chain 包装：
//
//	pub  = RequestID + CORS
//	user = RequestID + CORS + BodyLimit(cfg.BodyLimitBytes()) + Management(auth)
//
// 启动校验 forum.json 可读；失败时论坛端点统一降级为 503，绝不写入任何数据、
// 也绝不用空数据冒充成功（Register 无返回值，故以日志 + 503 显式暴露）。
func Register(mux *http.ServeMux, st *store.Store, cfg *config.Config, authSvc *auth.Service) {
	fs, err := openFileStore(cfg)
	if err != nil {
		log.Printf("forum: 启动校验失败: %v（论坛端点降级为 503，不加载任何数据）", err)
		fs = nil
	}
	h := &handler{st: st, cfg: cfg, auth: authSvc, fs: fs}

	pub := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn,
			middleware.RequestID,
			middleware.CORS(cfg.CORSOrigins),
		)
	}
	user := func(fn http.HandlerFunc) http.Handler {
		return middleware.Chain(fn,
			middleware.RequestID,
			middleware.CORS(cfg.CORSOrigins),
			middleware.BodyLimit(cfg.BodyLimitBytes()),
			middleware.Management(authSvc),
		)
	}

	mux.Handle("GET /api/forum/boards", pub(h.boards))
	mux.Handle("GET /api/forum/posts", pub(h.posts))
	mux.Handle("POST /api/forum/posts", user(h.createPost))
	mux.Handle("GET /api/forum/posts/{id}", pub(h.getPost))
	mux.Handle("POST /api/forum/posts/{id}/replies", user(h.createReply))
	mux.Handle("POST /api/forum/posts/{id}/like", user(h.like))
	mux.Handle("PUT /api/forum/posts/{id}/pin", user(h.pin))
	mux.Handle("DELETE /api/forum/posts/{id}", user(h.deletePost))
	mux.Handle("PUT /api/forum/posts/{id}", user(h.updatePost))
	mux.Handle("PUT /api/forum/posts/{id}/replies/{rid}", user(h.updateReply))
	mux.Handle("DELETE /api/forum/posts/{id}/replies/{rid}", user(h.deleteReply))
}

// ---------- 响应辅助 ----------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// json.Encoder 默认 SetEscapeHTML(true)：< > & 被转义为 \uXXXX，
	// 与 sanitizeText 一起构成输出侧 XSS 防护，响应体永不回显原始 HTML。
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, r *http.Request, status int, errType, code, message string) {
	relay.WriteJSONError(w, status, errType, code, message, middleware.RequestIDFrom(r.Context()))
}

func (h *handler) unavailable(w http.ResponseWriter, r *http.Request) bool {
	if h.fs == nil {
		fail(w, r, http.StatusServiceUnavailable, "api_error",
			"forum_storage_unavailable", "forum.json 启动校验失败，论坛暂不可用")
		return true
	}
	return false
}

func (h *handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errPostNotFound), errors.Is(err, errReplyNotFound):
		fail(w, r, http.StatusNotFound, "invalid_request_error", "not_found", err.Error())
	case errors.Is(err, errNotAuthor), errors.Is(err, errForbidden):
		fail(w, r, http.StatusForbidden, "permission_error", "forbidden", err.Error())
	case errors.Is(err, errBoardRequired), errors.Is(err, errBoardInvalid),
		errors.Is(err, errTitleRequired), errors.Is(err, errContentRequired):
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_parameter", err.Error())
	case errors.Is(err, errStorageUnavailable):
		fail(w, r, http.StatusServiceUnavailable, "api_error", "forum_storage_unavailable", err.Error())
	default:
		fail(w, r, http.StatusInternalServerError, "api_error", "forum_write_failed", err.Error())
	}
}

// currentUser 二次核验：Management 已注入用户，这里确认其仍是 store 中真实
// 且活跃的账号（禁用/删除后立即失效），返回真实 *store.User。
func (h *handler) currentUser(r *http.Request) *store.User {
	u := middleware.UserFrom(r.Context())
	if u == nil {
		return nil
	}
	live, ok := h.st.GetUserByID(u.ID)
	if !ok || live.Status != "active" {
		return nil
	}
	return live
}

// optionalUserID 用于 pub 端点：若调用方自愿带了合法 JWT，则识别其真实用户 ID
// （用于 liked 字段）；不强制鉴权，失败静默按匿名处理。
func (h *handler) optionalUserID(r *http.Request) string {
	hdr := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(hdr), "bearer ") {
		return ""
	}
	tok := strings.TrimSpace(hdr[7:])
	if tok == "" || h.auth == nil {
		return ""
	}
	u, _, err := h.auth.Authenticate(tok)
	if err != nil {
		return ""
	}
	return u.ID
}

func (h *handler) requireUser(w http.ResponseWriter, r *http.Request) *store.User {
	u := h.currentUser(r)
	if u == nil {
		fail(w, r, http.StatusUnauthorized, "invalid_request_error",
			"invalid_token", "有效登录令牌 required")
	}
	return u
}

func (h *handler) decode(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	data, _, ok := middleware.ReadBody(w, r)
	if !ok {
		return false
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "empty_body", "request body required")
		return false
	}
	if err := json.Unmarshal(data, dst); err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_json", err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !entityIDRe.MatchString(id) {
		fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_parameter", "invalid id")
		return "", false
	}
	return id, true
}

func parseIntParam(r *http.Request, key string, def int) int {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// ---------- 端点 ----------

// GET /api/forum/boards —— 真实版块列表（初始为空数组）。
func (h *handler) boards(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, h.fs.listBoards())
}

// GET /api/forum/posts?board=&q=&page=&page_size= —— 检索 / 版块过滤 / 分页。
func (h *handler) posts(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w, r) {
		return
	}
	page := parseIntParam(r, "page", 1)
	pageSize := parseIntParam(r, "page_size", defaultPageSize)
	list := h.fs.listPosts(r.URL.Query().Get("board"), r.URL.Query().Get("q"),
		page, pageSize, h.optionalUserID(r))
	writeJSON(w, http.StatusOK, list)
}

// POST /api/forum/posts —— 真实发帖（需登录）。
func (h *handler) createPost(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w, r) {
		return
	}
	u := h.requireUser(w, r)
	if u == nil {
		return
	}
	var req createPostRequest
	if !h.decode(w, r, &req) {
		return
	}
	view, err := h.fs.createPost(u, req)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

// GET /api/forum/posts/{id} —— 帖子详情 + 真实回复列表。
func (h *handler) getPost(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	detail, err := h.fs.getPost(id, h.optionalUserID(r))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// POST /api/forum/posts/{id}/replies —— 真实回复（需登录）。
func (h *handler) createReply(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w, r) {
		return
	}
	u := h.requireUser(w, r)
	if u == nil {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req createReplyRequest
	if !h.decode(w, r, &req) {
		return
	}
	view, err := h.fs.createReply(u, id, req.Content)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

// POST /api/forum/posts/{id}/like —— 幂等点赞（需登录）。
func (h *handler) like(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w, r) {
		return
	}
	u := h.requireUser(w, r)
	if u == nil {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	res, err := h.fs.likePost(u, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// PUT /api/forum/posts/{id}/pin —— 置顶 / 取消置顶（admin）。
// body 可为 {"pinned": false}；缺省或空 body 表示置顶 true。
func (h *handler) pin(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w, r) {
		return
	}
	u := h.requireUser(w, r)
	if u == nil {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	data, _, ok := middleware.ReadBody(w, r)
	if !ok {
		return
	}
	pinned := true
	if len(strings.TrimSpace(string(data))) > 0 {
		var req pinRequest
		if err := json.Unmarshal(data, &req); err != nil {
			fail(w, r, http.StatusBadRequest, "invalid_request_error", "invalid_json", err.Error())
			return
		}
		if req.Pinned != nil {
			pinned = *req.Pinned
		}
	}
	view, err := h.fs.pinPost(u, id, pinned)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// DELETE /api/forum/posts/{id} —— 作者本人或 admin 删除（级联真实回复/点赞）。
func (h *handler) deletePost(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w, r) {
		return
	}
	u := h.requireUser(w, r)
	if u == nil {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.fs.deletePost(u, id); err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ActionResult{OK: true, ID: id})
}

// updatePost PUT /api/forum/posts/{id} —— 编辑帖子（仅作者本人）。
func (h *handler) updatePost(w http.ResponseWriter, r *http.Request) {
	if h.fs == nil {
		h.unavailable(w, r)
		return
	}
	u := h.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	view, err := h.fs.updatePost(u, r.PathValue("id"), in.Title, in.Content)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// updateReply PUT /api/forum/posts/{id}/replies/{rid} —— 编辑回复（仅回复作者本人）。
func (h *handler) updateReply(w http.ResponseWriter, r *http.Request) {
	if h.fs == nil {
		h.unavailable(w, r)
		return
	}
	u := h.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	view, err := h.fs.updateReply(u, r.PathValue("rid"), in.Content)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// deleteReply DELETE /api/forum/posts/{id}/replies/{rid} —— 删除回复（回复作者或帖子作者）。
func (h *handler) deleteReply(w http.ResponseWriter, r *http.Request) {
	if h.fs == nil {
		h.unavailable(w, r)
		return
	}
	u := h.requireUser(w, r)
	if u == nil {
		return
	}
	if err := h.fs.deleteReply(u, r.PathValue("rid")); err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "id": r.PathValue("rid")})
}
