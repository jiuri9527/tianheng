// Package avatar 实现「用户自己上传头像」与个人资料（F1 用户鉴权 / F12 用户前台扩展）。
// 零第三方 Go 依赖，仅使用标准库。
//
// 安全约束（逐条落实）：
//   - multipart/form-data 字段名固定为 avatar；只认服务端嗅探出的真实文件魔数，
//     绝不采信客户端文件名、也不采信 Content-Type 作为唯一依据；
//   - 落盘文件名由服务端认证用户 id + 白名单扩展名构成，目录 0700、文件 0600，
//     所有写入走同目录临时文件 + rename 的原子写；
//   - PNG/JPEG/GIF 用标准库 image 解码校验并生成 128×128 缩略图；
//     WebP 无标准解码器，按魔数校验后原样保存并在元数据中标记 decoded=false；
//   - 昵称/简介做长度限制与 HTML 转义后写入 profiles.json（原子写、0600、mutex），
//     文件不存在时为空资料集，禁止预置任何假资料。
//
// 统一注册入口：Register(mux, st, cfg, auth)，不修改 server.go / api/server.go。
package avatar

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"llm-hub/src/internal/auth"
	"llm-hub/src/internal/config"
	"llm-hub/src/internal/middleware"
	"llm-hub/src/internal/relay"
	"llm-hub/src/internal/store"
)

const (
	// maxAvatarBytes 头像文件上限 2 MiB（2 << 20）。
	maxAvatarBytes = 2 << 20
	// avatarBodyLimit 额外留出 64 KiB 给 multipart 边界与表单头。
	avatarBodyLimit = maxAvatarBytes + 64<<10
	// thumbSize 缩略图边长（正方形）。
	thumbSize = 128
	// thumbExt 缩略图固定为 PNG（标准库可编码）。
	thumbExt = ".thumb.png"
)

// 落盘扩展名白名单：任何拼接路径都必须经过此集合，杜绝用户可控扩展名。
var allowedExts = map[string]bool{
	".png": true, ".jpg": true, ".webp": true, ".gif": true, thumbExt: true,
}

var imageExts = []string{".png", ".jpg", ".webp", ".gif"}

// safeUserID 只允许字母数字与下划线/连字符；store.NewID("usr") 产出形如 usr_<hex>，天然满足。
// 用于抵御 /api/avatar/{user_id} 的路径穿越。
var safeUserID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Service 聚合头像与个人资料所需依赖。
type Service struct {
	st         *store.Store
	cfg        *config.Config
	auth       *auth.Service
	profiles   *ProfileStore
	avatarsDir string
	// mu 串行化头像文件写入，避免同一用户并发上传互相覆盖 / 残留旧扩展名文件。
	mu sync.Mutex
}

// New 构造服务并准备目录（DataDir 0700、avatars 0700）。
func New(st *store.Store, cfg *config.Config, authSvc *auth.Service) (*Service, error) {
	dir := cfg.DataDir
	if dir == "" {
		dir = "./.llmhub-data" // 与 src/cmd/server/main.go 的默认保持一致
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	avatarsDir := filepath.Join(dir, "avatars")
	if err := os.MkdirAll(avatarsDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(avatarsDir, 0o700); err != nil {
		return nil, err
	}
	ps, err := NewProfileStore(dir)
	if err != nil {
		return nil, err
	}
	return &Service{
		st: st, cfg: cfg, auth: authSvc, profiles: ps, avatarsDir: avatarsDir,
	}, nil
}

// Register 把头像与个人资料端点挂到既有 mux 上，供装配层一行调用。
//
// 路由：
//
//	POST /api/avatar/upload        user   上传头像（multipart 字段 avatar）
//	GET  /api/avatar/me            user   本人头像与资料元数据（JSON）
//	GET  /api/avatar/{user_id}     pub    只返回图片字节（默认缩略图，?original=1 原图）
//	PUT  /api/profile              user   更新昵称/简介
//	GET  /api/profile              user   读取本人资料
//	GET  /api/profile/{user_id}    pub    公开资料（不含邮箱/角色/余额）
func Register(mux *http.ServeMux, st *store.Store, cfg *config.Config, auth *auth.Service) {
	svc, err := New(st, cfg, auth)
	if err != nil {
		log.Printf("avatar: 初始化失败，端点未注册: %v", err)
		return
	}
	mux.Handle("POST /api/avatar/upload", svc.user(svc.handleUpload))
	mux.Handle("GET /api/avatar/me", svc.user(svc.handleMe))
	mux.Handle("GET /api/avatar/{user_id}", svc.pub(svc.handlePublic))
	mux.Handle("PUT /api/profile", svc.user(svc.handleProfilePut))
	mux.Handle("GET /api/profile", svc.user(svc.handleProfileGet))
	mux.Handle("GET /api/profile/{user_id}", svc.pub(svc.handlePublicProfile))
}

// pub / user 与论坛约定一致：pub 仅 RequestID + CORS；
// user 追加 BodyLimit + Management(JWT)，认证后的当前用户用 middleware.UserFrom 取得。
func (s *Service) pub(h http.HandlerFunc) http.Handler {
	return middleware.Chain(h, middleware.RequestID, middleware.CORS(s.cfg.CORSOrigins))
}

func (s *Service) user(h http.HandlerFunc) http.Handler {
	return middleware.Chain(h,
		middleware.RequestID,
		middleware.CORS(s.cfg.CORSOrigins),
		middleware.BodyLimit(avatarBodyLimit),
		middleware.Management(s.auth),
	)
}

// ---------- 公共辅助 ----------

func (s *Service) currentUser(r *http.Request) *store.User {
	return middleware.UserFrom(r.Context())
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	relay.WriteJSONError(w, status, "invalid_request_error", code, message,
		middleware.RequestIDFrom(r.Context()))
}

func (s *Service) ok(w http.ResponseWriter, r *http.Request, v map[string]interface{}) {
	if v == nil {
		v = map[string]interface{}{}
	}
	v["request_id"] = middleware.RequestIDFrom(r.Context())
	writeJSON(w, http.StatusOK, v)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// randomID 用 crypto/rand 生成服务端随机 id（用于头像版本号 / 缓存串，绝不采信用户文件名）。
func randomID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// avatarPath 由「已校验的用户 id + 白名单扩展名」拼出绝对路径，并二次确认未逃逸目录。
func (s *Service) avatarPath(userID, ext string) (string, bool) {
	if !safeUserID.MatchString(userID) || !allowedExts[ext] {
		return "", false
	}
	p := filepath.Join(s.avatarsDir, userID+ext)
	clean := filepath.Clean(p)
	if filepath.Dir(clean) != filepath.Clean(s.avatarsDir) {
		return "", false
	}
	return clean, true
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
