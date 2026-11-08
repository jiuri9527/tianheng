package forum

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"llm-hub/src/internal/auth"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/store"
)

type fixture struct {
	mux      *http.ServeMux
	dir      string
	cfg      *config.Config
	adminTok string
	userTok  string
	authSvc  *auth.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		DataDir: dir, CORSOrigins: "*", BodyLimitMB: 1,
		JWTSecret: "forum-test-secret-0123456789", JWTTTL: time.Hour,
	}
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	authSvc := auth.NewService(st, cfg)
	adminUser, adminTokens, err := authSvc.Register("alice@example.com", "password123", true, "")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	userUser, userTokens, err := authSvc.Register("bob@example.com", "password123", true, "")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	t.Logf("fixture roles: alice=%s bob=%s", adminUser.Role, userUser.Role)
	mux := http.NewServeMux()
	Register(mux, st, cfg, authSvc)
	return &fixture{mux: mux, dir: dir, cfg: cfg,
		adminTok: adminTokens.AccessToken, userTok: userTokens.AccessToken, authSvc: authSvc}
}

func (f *fixture) do(method, path, token, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}

func decodeInto(t *testing.T, w *httptest.ResponseRecorder, dst interface{}) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode %d %q: %v", w.Code, w.Body.String(), err)
	}
}

// TestForumRealEmptyState 验证：无任何真实提交时 boards/posts 是真实空数组，
// 绝不出现种子 / 演示 / 假数据。
func TestForumRealEmptyState(t *testing.T) {
	f := newFixture(t)
	w := f.do(http.MethodGet, "/api/forum/boards", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("boards status=%d body=%s", w.Code, w.Body.String())
	}
	var bl BoardList
	decodeInto(t, w, &bl)
	if bl.Total != 0 || len(bl.Items) != 0 || bl.Items == nil {
		t.Fatalf("boards 非空态: %+v", bl)
	}
	w = f.do(http.MethodGet, "/api/forum/posts", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("posts status=%d body=%s", w.Code, w.Body.String())
	}
	var pl PostList
	decodeInto(t, w, &pl)
	if pl.Total != 0 || len(pl.Items) != 0 || pl.Items == nil {
		t.Fatalf("posts 非空态: %+v", pl)
	}
	// 真实空态时不预创建 forum.json（首次真实提交才落盘）。
	if _, err := os.Stat(filepath.Join(f.dir, "forum.json")); !os.IsNotExist(err) {
		t.Fatalf("空态不应预创建 forum.json, err=%v", err)
	}
	t.Logf("PASS 真实空态: boards=%d posts=%d forum.json 不存在", bl.Total, pl.Total)
}

// TestForumLifecycle 走完整真实提交链路：发帖 -> 版块由提交产生 -> 回复 ->
// 幂等点赞 -> 置顶(admin) -> 持久化 0600 + 重启可读 -> 删除级联。
func TestForumLifecycle(t *testing.T) {
	f := newFixture(t)

	// 1) 发帖（XSS 输入同时验证净化）
	createBody := `{"board":"general","board_name":"综合讨论","board_description":"真实提交的版块","title":"第一帖 <b>Hi</b>","content":"hello <script>alert(1)</script> world"}`
	w := f.do(http.MethodPost, "/api/forum/posts", f.adminTok, createBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("createPost status=%d body=%s", w.Code, w.Body.String())
	}
	var created PostView
	decodeInto(t, w, &created)
	if created.ID == "" || created.AuthorName != "alice" {
		t.Fatalf("createPost 视图异常: %+v", created)
	}
	if strings.Contains(w.Body.String(), "<script") || strings.Contains(w.Body.String(), "<b>") {
		t.Fatalf("响应回显了原始 HTML: %s", w.Body.String())
	}
	t.Logf("PASS 发帖: id=%s author=%s title=%q content=%q", created.ID, created.AuthorName, created.Title, created.Content)

	// 2) 版块由真实提交产生
	w = f.do(http.MethodGet, "/api/forum/boards", "", "")
	var bl BoardList
	decodeInto(t, w, &bl)
	if bl.Total != 1 || bl.Items[0].Slug != "general" || bl.Items[0].PostCount != 1 {
		t.Fatalf("版块未由真实提交产生: %+v", bl)
	}

	// 3) 无 token 发帖被拒
	w = f.do(http.MethodPost, "/api/forum/posts", "", createBody)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录发帖应 401, got %d", w.Code)
	}

	// 4) 回复
	pid := created.ID
	w = f.do(http.MethodPost, "/api/forum/posts/"+pid+"/replies", f.userTok,
		`{"content":"真实回复 <i>x</i>"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("reply status=%d body=%s", w.Code, w.Body.String())
	}

	// 5) 点赞幂等：两次 POST 计数仍为 1
	for i := 0; i < 2; i++ {
		w = f.do(http.MethodPost, "/api/forum/posts/"+pid+"/like", f.userTok, "")
		if w.Code != http.StatusOK {
			t.Fatalf("like#%d status=%d body=%s", i, w.Code, w.Body.String())
		}
		var lr LikeResult
		decodeInto(t, w, &lr)
		if lr.LikeCount != 1 || !lr.Liked {
			t.Fatalf("like 非幂等: %+v", lr)
		}
	}

	// 6) 置顶：非作者 403，作者 200
	w = f.do(http.MethodPut, "/api/forum/posts/"+pid+"/pin", f.userTok, `{"pinned":true}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("非作者置顶应 403, got %d body=%s", w.Code, w.Body.String())
	}
	chk, _, chkErr := f.authSvc.Authenticate(f.adminTok)
	if chkErr != nil {
		t.Fatalf("admin token 校验失败: %v", chkErr)
	}
	t.Logf("作者 token -> id=%s role=%s", chk.ID, chk.Role)
	w = f.do(http.MethodPut, "/api/forum/posts/"+pid+"/pin", f.adminTok, `{"pinned":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("作者置顶 status=%d body=%s", w.Code, w.Body.String())
	}

	// 7) 详情：真实计数 + 真实回复
	w = f.do(http.MethodGet, "/api/forum/posts/"+pid, f.userTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", w.Code, w.Body.String())
	}
	var detail PostDetail
	decodeInto(t, w, &detail)
	if detail.Post.ReplyCount != 1 || detail.Post.LikeCount != 1 || len(detail.Replies) != 1 || !detail.Post.Pinned {
		t.Fatalf("详情计数异常: %+v replies=%d", detail.Post, len(detail.Replies))
	}
	if !detail.Post.Liked {
		t.Fatalf("pub 端点带 JWT 未识别 liked")
	}
	t.Logf("PASS 计数: reply_count=%d like_count=%d replies=%d pinned=%v liked=%v",
		detail.Post.ReplyCount, detail.Post.LikeCount, len(detail.Replies), detail.Post.Pinned, detail.Post.Liked)

	// 8) 持久化：0600、原子落盘、重启可读且数据一致
	path := filepath.Join(f.dir, "forum.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("forum.json 未落盘: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("forum.json 权限应为 0600, got %o", info.Mode().Perm())
	}
	fs2, err := openFileStore(f.cfg)
	if err != nil {
		t.Fatalf("重启读取 forum.json 失败: %v", err)
	}
	d2, err := fs2.getPost(pid, "")
	if err != nil || d2.Post.ReplyCount != 1 || d2.Post.LikeCount != 1 {
		t.Fatalf("重启后数据不一致: err=%v detail=%+v", err, d2)
	}
	t.Logf("PASS 持久化: mode=0600 bytes=%d 重启后 reply_count=%d like_count=%d",
		info.Size(), d2.Post.ReplyCount, d2.Post.LikeCount)

	// 9) 删除级联
	w = f.do(http.MethodDelete, "/api/forum/posts/"+pid, f.adminTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", w.Code, w.Body.String())
	}
	w = f.do(http.MethodGet, "/api/forum/posts/"+pid, "", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("删除后详情应 404, got %d", w.Code)
	}
	fs3, _ := openFileStore(f.cfg)
	fs3.read(func(db *database) {
		if len(db.Posts) != 0 || len(db.Replies) != 0 || len(db.Likes) != 0 {
			t.Fatalf("删除未级联: posts=%d replies=%d likes=%d", len(db.Posts), len(db.Replies), len(db.Likes))
		}
	})
	t.Logf("PASS 删除级联: posts=0 replies=0 likes=0")
}

// TestForumSearchAndPaging 验证检索 / 版块过滤 / 分页。
func TestForumSearchAndPaging(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 3; i++ {
		body := `{"board":"general","title":"检索关键词 Alpha ` + string(rune('A'+i)) + `","content":"内容 ` + string(rune('a'+i)) + `"}`
		w := f.do(http.MethodPost, "/api/forum/posts", f.adminTok, body)
		if w.Code != http.StatusCreated {
			t.Fatalf("create#%d status=%d body=%s", i, w.Code, w.Body.String())
		}
	}
	w := f.do(http.MethodGet, "/api/forum/posts?q=Alpha&page=1&page_size=2", "", "")
	var pl PostList
	decodeInto(t, w, &pl)
	if pl.Total != 3 || len(pl.Items) != 2 || pl.Page != 1 || pl.PageSize != 2 {
		t.Fatalf("检索/分页异常: %+v", pl)
	}
	w = f.do(http.MethodGet, "/api/forum/posts?board=not-exist", "", "")
	decodeInto(t, w, &pl)
	if pl.Total != 0 {
		t.Fatalf("版块过滤异常: %+v", pl)
	}
	t.Logf("PASS 检索/分页: total=3 page1=2 page_size=2")
}

// TestForumStartValidation 验证启动校验：损坏的 forum.json 必须返回 error，
// 绝不静默吞掉后用空数据冒充成功。
func TestForumStartValidation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "forum.json"), []byte(`{"boards": [broken`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := openFileStore(&config.Config{DataDir: dir}); err == nil {
		t.Fatal("损坏的 forum.json 应导致启动校验失败")
	} else {
		t.Logf("PASS 启动校验: %v", err)
	}
}

// TestSanitizeText 直接验证 XSS 净化与长度截断。
func TestSanitizeText(t *testing.T) {
	in := "<script>alert(1)</script> Hi <b>there</b> & <img src=x onerror=alert(2)>"
	got := sanitizeText(in, 200)
	for _, bad := range []string{"<script", "<b>", "<img", ">", "<"} {
		if strings.Contains(got, bad) {
			t.Fatalf("sanitize 残留 %q: %q", bad, got)
		}
	}
	long := sanitizeText(strings.Repeat("字", 300), maxTitleRunes)
	if len([]rune(long)) != maxTitleRunes {
		t.Fatalf("未按 rune 截断: %d", len([]rune(long)))
	}
	t.Logf("PASS sanitizeText: %q -> %q (截断=%d rune)", in, got, len([]rune(long)))
}

// ---------- 编辑与删回复（P1 补全，按真实 fixture API 编写）----------

// TestForumUpdatePostAndReplyLifecycle 覆盖：
// 作者编辑帖子（created_at 不变 / updated_at 递增）→ 非作者 403 →
// 帖子作者删除他人回复 → reply_count 真实递减到 0。
func TestForumUpdatePostAndReplyLifecycle(t *testing.T) {
	f := newFixture(t)

	// 作者（adminTok）发帖
	w := f.do(http.MethodPost, "/api/forum/posts", f.adminTok,
		`{"title":"编辑测试帖","board":"board-edit","content":"原始正文内容"}`)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("发帖失败 %d %s", w.Code, w.Body.String())
	}
	var pv PostView
	decodeInto(t, w, &pv)

	// 非作者（userTok）编辑 → 403
	w = f.do(http.MethodPut, "/api/forum/posts/"+pv.ID, f.userTok,
		`{"title":"劫持","content":"劫持内容"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("非作者编辑帖子应 403，实际 %d %s", w.Code, w.Body.String())
	}

	// 作者编辑 → 200，created_at 不变、updated_at 递增
	time.Sleep(1100 * time.Millisecond)
	w = f.do(http.MethodPut, "/api/forum/posts/"+pv.ID, f.adminTok,
		`{"title":"编辑后的标题","content":"编辑后的正文"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("作者编辑应 200，实际 %d %s", w.Code, w.Body.String())
	}
	var ev PostView
	decodeInto(t, w, &ev)
	if ev.Title != "编辑后的标题" {
		t.Fatalf("编辑后标题不符: %q", ev.Title)
	}
	if ev.CreatedAt != pv.CreatedAt {
		t.Fatalf("编辑不得改变 created_at: %d → %d", pv.CreatedAt, ev.CreatedAt)
	}
	if ev.UpdatedAt <= pv.UpdatedAt {
		t.Fatalf("编辑后 updated_at 应递增: %d → %d", pv.UpdatedAt, ev.UpdatedAt)
	}

	// 他人回复
	w = f.do(http.MethodPost, "/api/forum/posts/"+pv.ID+"/replies", f.userTok, `{"content":"他人回复"}`)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("回复失败 %d %s", w.Code, w.Body.String())
	}
	var rv ReplyView
	decodeInto(t, w, &rv)

	// 第三方不可编辑该回复：用一个没有 token 的匿名请求验证 401
	w = f.do(http.MethodPut, "/api/forum/posts/"+pv.ID+"/replies/"+rv.ID, "", `{"content":"匿名劫持"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("匿名编辑回复应 401，实际 %d", w.Code)
	}

	// 回复作者编辑自己的回复 → 200
	w = f.do(http.MethodPut, "/api/forum/posts/"+pv.ID+"/replies/"+rv.ID, f.userTok, `{"content":"作者自编辑"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("回复作者编辑应 200，实际 %d %s", w.Code, w.Body.String())
	}

	// 帖子作者删除他人回复 → 200（帖子作者具备版主语义）
	w = f.do(http.MethodDelete, "/api/forum/posts/"+pv.ID+"/replies/"+rv.ID, f.adminTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("帖子作者删他人回复应 200，实际 %d %s", w.Code, w.Body.String())
	}

	// 详情 reply_count 归 0
	w = f.do(http.MethodGet, "/api/forum/posts/"+pv.ID, "", "")
	var pd PostDetail
	decodeInto(t, w, &pd)
	if pd.Post.ReplyCount != 0 {
		t.Fatalf("删回复后 reply_count 应为 0，实际 %d", pd.Post.ReplyCount)
	}

	// 落盘 0600 且重启可读（真实持久化）
	st, err := os.Stat(filepath.Join(f.dir, "forum.json"))
	if err != nil {
		t.Fatalf("forum.json 未落盘: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("forum.json 权限应为 0600，实际 %o", st.Mode().Perm())
	}
	t.Logf("PASS: 作者编辑 created_at 保持 / updated_at 递增 / 非作者 403 / 匿名 401 / 删回复 reply_count=0 / 落盘 0600")
}

// TestForumDeleteReplyNotFound 删除不存在的回复 → 404。
func TestForumDeleteReplyNotFound(t *testing.T) {
	f := newFixture(t)
	w := f.do(http.MethodPost, "/api/forum/posts", f.adminTok,
		`{"title":"帖","board":"b","content":"正文"}`)
	var pv PostView
	decodeInto(t, w, &pv)

	w = f.do(http.MethodDelete, "/api/forum/posts/"+pv.ID+"/replies/rpl_notexist", f.adminTok, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("删除不存在回复应 404，实际 %d %s", w.Code, w.Body.String())
	}
}
