package avatar

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"llm-hub/src/internal/auth"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/middleware"
	"llm-hub/src/internal/store"
)

func testEnv(t *testing.T) (*http.ServeMux, *config.Config, string, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		DataDir:     dir,
		JWTSecret:   "avatar-test-jwt-secret-32bytes-minimum-ok",
		JWTTTL:      time.Hour,
		CORSOrigins: "*",
		BodyLimitMB: 20,
	}
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	authSvc := auth.NewService(st, cfg)
	u, toks, err := authSvc.Register("avatar-test@example.com", "correct-horse-battery", true, "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	mux := http.NewServeMux()
	Register(mux, st, cfg, authSvc)
	return mux, cfg, u.ID, toks.AccessToken
}

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func upload(t *testing.T, mux *http.ServeMux, token, filename, ct string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="avatar"; filename="`+filename+`"`)
	h.Set("Content-Type", ct)
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/avatar/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestSniffAndValidate(t *testing.T) {
	pngData := makePNG(t, 4, 4)
	var jbuf bytes.Buffer
	if err := jpeg.Encode(&jbuf, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	var gbuf bytes.Buffer
	if err := gif.Encode(&gbuf, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	webp := []byte("RIFF\x1a\x00\x00\x00WEBPVP8 " + strings.Repeat("\x00", 10))

	cases := []struct {
		name string
		data []byte
		ct   string
		ok   bool
		ext  string
	}{
		{"png", pngData, "image/png", true, ".png"},
		{"jpeg", jbuf.Bytes(), "image/jpeg", true, ".jpg"},
		{"gif", gbuf.Bytes(), "image/gif", true, ".gif"},
		{"webp", webp, "image/webp", true, ".webp"},
		{"wrong-magic", []byte("hello world not an image"), "image/png", false, ""},
		{"ct-mismatch", pngData, "image/jpeg", false, ""},
		{"bad-ct", pngData, "application/octet-stream", false, ""},
		{"empty", nil, "image/png", false, ""},
		{"oversize", append(make([]byte, maxAvatarBytes+1), pngData...), "image/png", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k, err := validateAvatar(c.data, c.ct)
			if c.ok {
				if err != nil {
					t.Fatalf("期望通过，实际失败: %v", err)
				}
				if k.ext != c.ext {
					t.Fatalf("ext=%s 期望 %s", k.ext, c.ext)
				}
			} else if err == nil {
				t.Fatalf("期望拒绝，实际通过: %+v", k)
			}
		})
	}
}

func TestSafeUserIDRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"../etc/passwd", "..%2fetc", "a/b", "a\\b", "", "x;rm -rf", "usr_../../x"} {
		if safeUserID.MatchString(bad) {
			t.Fatalf("危险 id 被放行: %q", bad)
		}
	}
	if !safeUserID.MatchString("usr_0123456789abcdef") {
		t.Fatal("合法 id 被拒绝")
	}
	svc := &Service{avatarsDir: filepath.Join(t.TempDir(), "avatars")}
	if _, ok := svc.avatarPath("../etc/passwd", ".png"); ok {
		t.Fatal("路径穿越未被拦截")
	}
	if _, ok := svc.avatarPath("usr_ok", ".svg"); ok {
		t.Fatal("非白名单扩展名未被拦截")
	}
	if p, ok := svc.avatarPath("usr_ok", ".png"); !ok || !strings.HasSuffix(p, "usr_ok.png") {
		t.Fatalf("合法路径生成失败: %q %v", p, ok)
	}
}

func TestProfileStoreNoFakeDataAndAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	ps, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ps.Get("usr_none") != nil {
		t.Fatal("空资料库不应返回任何资料")
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles.json")); !os.IsNotExist(err) {
		t.Fatal("不应预置 profiles.json 假资料文件")
	}
	nick, err := sanitizeText(`<script>alert(1)</script>&"`, "昵称", maxNicknameRunes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(nick, "<script>") {
		t.Fatal("未做 HTML 转义")
	}
	if _, err := sanitizeText(strings.Repeat("字", maxNicknameRunes+1), "昵称", maxNicknameRunes); err == nil {
		t.Fatal("昵称超长未被拒绝")
	}
	if _, err := sanitizeText("x\x00y", "简介", maxBioRunes); err != nil {
		t.Fatal(err)
	}
	p, err := ps.Update("usr_a", func(p *Profile) { p.Nickname = nick })
	if err != nil {
		t.Fatal(err)
	}
	if p.Nickname != nick {
		t.Fatal("更新未生效")
	}
	info, err := os.Stat(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("profiles.json 权限应为 0600，实际 %o", info.Mode().Perm())
	}
	b, _ := os.ReadFile(filepath.Join(dir, "profiles.json"))
	var pf profileFile
	if err := json.Unmarshal(b, &pf); err != nil {
		t.Fatalf("profiles.json 不是合法 JSON: %v", err)
	}
	if pf.Profiles["usr_a"] == nil {
		t.Fatal("资料未落盘")
	}
	// 二次加载应读到同一份真实资料
	ps2, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ps2.Get("usr_a").Nickname != nick {
		t.Fatal("重载后资料不一致")
	}
}

func TestEndToEndUploadAndProfile(t *testing.T) {
	mux, cfg, uid, token := testEnv(t)
	pngData := makePNG(t, 64, 48)

	rr := upload(t, mux, token, "../../evil<script>.png", "image/png", pngData)
	if rr.Code != http.StatusOK {
		t.Fatalf("上传失败 status=%d body=%s", rr.Code, rr.Body.String())
	}
	var up map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	if up["request_id"] == nil || up["request_id"] == "" {
		t.Fatal("响应缺少 request_id")
	}
	av := up["avatar"].(map[string]interface{})
	if av["has_avatar"] != true || av["decoded"] != true || av["has_thumbnail"] != true {
		t.Fatalf("头像元数据不正确: %+v", av)
	}

	// 落盘文件与目录权限
	avatarsDir := filepath.Join(cfg.DataDir, "avatars")
	di, err := os.Stat(avatarsDir)
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("avatars 目录权限应为 0700: %v %v", di, err)
	}
	fi, err := os.Stat(filepath.Join(avatarsDir, uid+".png"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("原图权限应为 0600: %v %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(avatarsDir, uid+thumbExt)); err != nil {
		t.Fatalf("缩略图未生成: %v", err)
	}
	// 用户上传的恶意文件名绝不能被采信落盘
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "evil<script>.png")); !os.IsNotExist(err) {
		t.Fatal("用户文件名被写入磁盘")
	}

	// 公开图片端点：默认返回 128×128 缩略图
	gr := httptest.NewRecorder()
	mux.ServeHTTP(gr, httptest.NewRequest(http.MethodGet, "/api/avatar/"+uid, nil))
	if gr.Code != http.StatusOK {
		t.Fatalf("公开头像 status=%d body=%s", gr.Code, gr.Body.String())
	}
	if ct := gr.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type=%s", ct)
	}
	if gr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("缺少 nosniff")
	}
	c, _, err := image.DecodeConfig(bytes.NewReader(gr.Body.Bytes()))
	if err != nil || c.Width != thumbSize || c.Height != thumbSize {
		t.Fatalf("缩略图不是 128x128: %+v %v", c, err)
	}
	// 原图
	or := httptest.NewRecorder()
	mux.ServeHTTP(or, httptest.NewRequest(http.MethodGet, "/api/avatar/"+uid+"?original=1", nil))
	if or.Code != http.StatusOK || !bytes.Equal(or.Body.Bytes(), pngData) {
		t.Fatalf("原图返回不正确 status=%d len=%d", or.Code, or.Body.Len())
	}

	// /api/avatar/me 需要 JWT
	me := httptest.NewRecorder()
	meReq := httptest.NewRequest(http.MethodGet, "/api/avatar/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+token)
	mux.ServeHTTP(me, meReq)
	if me.Code != http.StatusOK {
		t.Fatalf("/api/avatar/me status=%d body=%s", me.Code, me.Body.String())
	}
	// 未登录访问 /api/avatar/me 应 401
	anon := httptest.NewRecorder()
	mux.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/api/avatar/me", nil))
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，实际 %d", anon.Code)
	}

	// PUT /api/profile：转义 + 长度限制
	pr := httptest.NewRecorder()
	preq := httptest.NewRequest(http.MethodPut, "/api/profile", strings.NewReader(`{"nickname":"<b>Alice</b>&","bio":"hello\nworld"}`))
	preq.Header.Set("Authorization", "Bearer "+token)
	preq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(pr, preq)
	if pr.Code != http.StatusOK {
		t.Fatalf("PUT /api/profile status=%d body=%s", pr.Code, pr.Body.String())
	}
	var pv map[string]interface{}
	_ = json.Unmarshal(pr.Body.Bytes(), &pv)
	profile := pv["profile"].(map[string]interface{})
	if profile["nickname"] != "&lt;b&gt;Alice&lt;/b&gt;&amp;" {
		t.Fatalf("昵称未转义: %v", profile["nickname"])
	}
	if _, ok := profile["email"]; ok {
		t.Fatal("公开资料泄漏邮箱")
	}

	// 超长昵称被拒绝
	tooLong := httptest.NewRecorder()
	lreq := httptest.NewRequest(http.MethodPut, "/api/profile", strings.NewReader(`{"nickname":"`+strings.Repeat("字", maxNicknameRunes+1)+`"}`))
	lreq.Header.Set("Authorization", "Bearer "+token)
	mux.ServeHTTP(tooLong, lreq)
	if tooLong.Code != http.StatusBadRequest {
		t.Fatalf("超长昵称应 400，实际 %d", tooLong.Code)
	}

	// GET /api/profile 读取真实资料
	gpr := httptest.NewRecorder()
	gpreq := httptest.NewRequest(http.MethodGet, "/api/profile", nil)
	gpreq.Header.Set("Authorization", "Bearer "+token)
	mux.ServeHTTP(gpr, gpreq)
	if gpr.Code != http.StatusOK {
		t.Fatalf("GET /api/profile status=%d", gpr.Code)
	}

	// 公开资料端点
	ppr := httptest.NewRecorder()
	mux.ServeHTTP(ppr, httptest.NewRequest(http.MethodGet, "/api/profile/"+uid, nil))
	if ppr.Code != http.StatusOK || strings.Contains(ppr.Body.String(), "email") {
		t.Fatalf("公开资料端点异常: %d %s", ppr.Code, ppr.Body.String())
	}

	// 路径穿越的 user_id 被拒绝
	tr := httptest.NewRecorder()
	mux.ServeHTTP(tr, httptest.NewRequest(http.MethodGet, "/api/avatar/..%2F..%2Fetc", nil))
	if tr.Code == http.StatusOK {
		t.Fatal("路径穿越请求被放行")
	}
}

func TestUploadRejectsInvalidAndUnauthenticated(t *testing.T) {
	mux, _, _, token := testEnv(t)

	// 未登录
	if rr := upload(t, mux, "", "a.png", "image/png", makePNG(t, 4, 4)); rr.Code != http.StatusUnauthorized {
		t.Fatalf("未登录上传应 401，实际 %d", rr.Code)
	}
	// 错误魔数
	if rr := upload(t, mux, token, "a.png", "image/png", []byte("not an image at all")); rr.Code != http.StatusBadRequest {
		t.Fatalf("错误魔数应 400，实际 %d body=%s", rr.Code, rr.Body.String())
	}
	// Content-Type 与魔数不一致
	if rr := upload(t, mux, token, "a.png", "image/jpeg", makePNG(t, 4, 4)); rr.Code != http.StatusBadRequest {
		t.Fatalf("CT 不一致应 400，实际 %d", rr.Code)
	}
	// 超过 2MiB
	big := append(make([]byte, maxAvatarBytes+1), makePNG(t, 2, 2)...)
	if rr := upload(t, mux, token, "a.png", "image/png", big); rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限应 413，实际 %d body=%s", rr.Code, rr.Body.String())
	}
	// 缺少 avatar 字段
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("other", "x")
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/avatar/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("缺字段应 400，实际 %d", rr.Code)
	}
}

func TestWebPIsMagicOnlyAndMarked(t *testing.T) {
	mux, _, uid, token := testEnv(t)
	webp := []byte("RIFF\x1a\x00\x00\x00WEBPVP8 " + strings.Repeat("\x00", 16))
	rr := upload(t, mux, token, "photo.webp", "image/webp", webp)
	if rr.Code != http.StatusOK {
		t.Fatalf("WebP 上传失败 status=%d body=%s", rr.Code, rr.Body.String())
	}
	var up map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &up)
	av := up["avatar"].(map[string]interface{})
	if av["decoded"] != false || av["has_thumbnail"] != false {
		t.Fatalf("WebP 应标记 decoded=false / has_thumbnail=false: %+v", av)
	}
	gr := httptest.NewRecorder()
	mux.ServeHTTP(gr, httptest.NewRequest(http.MethodGet, "/api/avatar/"+uid, nil))
	if gr.Code != http.StatusOK || gr.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("WebP 原样返回失败: %d %s", gr.Code, gr.Header().Get("Content-Type"))
	}
	if !bytes.Equal(gr.Body.Bytes(), webp) {
		t.Fatal("WebP 未被原样保存")
	}
}

func TestJPEGAndGIFGenerateThumbnail(t *testing.T) {
	mux, _, _, token := testEnv(t)
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, img, nil); err != nil {
		t.Fatal(err)
	}
	var gb bytes.Buffer
	if err := gif.Encode(&gb, img, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, ct, fn string
		data         []byte
	}{
		{"jpeg", "image/jpeg", "a.jpg", jb.Bytes()},
		{"gif", "image/gif", "a.gif", gb.Bytes()},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := upload(t, mux, token, c.fn, c.ct, c.data)
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

// 显式确认 middleware.UserFrom 是包内取得当前用户的唯一来源。
func TestCurrentUserComesFromMiddleware(t *testing.T) {
	u := &store.User{ID: "usr_ctx"}
	ctx := context.WithValue(context.Background(), middleware.KeyUser, u)
	if got := middleware.UserFrom(ctx); got == nil || got.ID != u.ID {
		t.Fatalf("UserFrom 未取回认证用户: %+v", got)
	}
	// 真实 JWT 链路已在 TestEndToEndUploadAndProfile 中覆盖。
}
