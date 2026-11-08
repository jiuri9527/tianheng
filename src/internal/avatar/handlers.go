package avatar

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"llm-hub/src/internal/middleware"
)

// avatarView 只暴露非敏感的头像元数据。
func avatarView(p *Profile, userID string) map[string]interface{} {
	v := map[string]interface{}{
		"user_id":    userID,
		"has_avatar": false,
		"url":        "/api/avatar/" + userID,
	}
	if p == nil || p.AvatarExt == "" {
		return v
	}
	v["has_avatar"] = true
	v["avatar_id"] = p.AvatarID
	v["content_type"] = p.AvatarContentType
	v["bytes"] = p.AvatarBytes
	v["decoded"] = p.AvatarDecoded
	v["has_thumbnail"] = p.HasThumbnail
	v["ext"] = p.AvatarExt
	v["updated_at"] = p.UpdatedAt
	v["url"] = "/api/avatar/" + userID + "?v=" + p.AvatarID
	if p.HasThumbnail {
		v["thumb_url"] = "/api/avatar/" + userID + "?v=" + p.AvatarID
		v["original_url"] = "/api/avatar/" + userID + "?original=1&v=" + p.AvatarID
	}
	return v
}

// handleUpload POST /api/avatar/upload（user）
func (s *Service) handleUpload(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		s.fail(w, r, http.StatusUnauthorized, "unauthorized", "需要登录")
		return
	}
	if _, ok := s.st.GetUserByID(u.ID); !ok {
		s.fail(w, r, http.StatusUnauthorized, "user_not_found", "用户不存在")
		return
	}
	if !safeUserID.MatchString(u.ID) {
		s.fail(w, r, http.StatusBadRequest, "invalid_user_id", "用户 id 不符合安全字符集")
		return
	}

	data, declaredCT, err := readAvatarPart(r)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid_upload", err.Error())
		return
	}
	if len(data) > maxAvatarBytes {
		s.fail(w, r, http.StatusRequestEntityTooLarge, "avatar_too_large",
			"头像超过 2MiB 上限")
		return
	}
	k, err := validateAvatar(data, declaredCT)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid_avatar", err.Error())
		return
	}

	thumb, decoded, err := s.processImage(k, data)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "decode_failed", err.Error())
		return
	}
	avatarID, err := randomID(16)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "random_failed", err.Error())
		return
	}
	if err := s.saveAvatarFiles(u.ID, k, data, thumb); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "write_failed", err.Error())
		return
	}
	p, err := s.profiles.Update(u.ID, func(p *Profile) {
		p.AvatarID = avatarID
		p.AvatarExt = k.ext
		p.AvatarContentType = k.contentType
		p.AvatarBytes = len(data)
		p.AvatarDecoded = decoded
		p.HasThumbnail = len(thumb) > 0
	})
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "profile_write_failed", err.Error())
		return
	}

	s.ok(w, r, map[string]interface{}{
		"ok":     true,
		"avatar": avatarView(p, u.ID),
		"profile": map[string]interface{}{
			"user_id": p.UserID, "nickname": p.Nickname, "bio": p.Bio,
		},
		"note": "文件名由服务端生成；用户上传的原始文件名已被丢弃。WebP 无标准解码器时仅按魔数校验原样保存。",
	})
}

// handleMe GET /api/avatar/me（user）：本人头像与资料元数据。
func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		s.fail(w, r, http.StatusUnauthorized, "unauthorized", "需要登录")
		return
	}
	p := s.profiles.Get(u.ID)
	w.Header().Set("Cache-Control", "private, no-store")
	s.ok(w, r, map[string]interface{}{
		"user_id": u.ID,
		"avatar":  avatarView(p, u.ID),
		"profile": publicProfileView(p, u.ID),
	})
}

// handlePublic GET /api/avatar/{user_id}（public）：只返回图片字节。
// 默认返回 128×128 缩略图；?original=1 返回原图。
func (s *Service) handlePublic(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	if !safeUserID.MatchString(userID) {
		s.fail(w, r, http.StatusBadRequest, "invalid_user_id", "用户 id 不符合安全字符集")
		return
	}
	if _, ok := s.st.GetUserByID(userID); !ok {
		s.fail(w, r, http.StatusNotFound, "user_not_found", "用户不存在")
		return
	}

	p := s.profiles.Get(userID)
	ext := ""
	if p != nil && allowedExts[p.AvatarExt] && p.AvatarExt != thumbExt {
		ext = p.AvatarExt
	}
	if ext == "" {
		// profiles.json 丢失但文件仍在时，按白名单顺序探测真实落盘文件。
		for _, e := range imageExts {
			if path, ok := s.avatarPath(userID, e); ok && fileExists(path) {
				ext = e
				break
			}
		}
	}
	if ext == "" {
		s.fail(w, r, http.StatusNotFound, "avatar_not_found", "该用户尚未上传头像（真实空态，不返回占位图）")
		return
	}

	original := r.URL.Query().Get("original") == "1"
	useExt := ext
	contentType := "image/png"
	if !original {
		if tp, ok := s.avatarPath(userID, thumbExt); ok && fileExists(tp) {
			useExt = thumbExt
		}
	}
	if useExt != thumbExt {
		switch ext {
		case ".jpg":
			contentType = "image/jpeg"
		case ".webp":
			contentType = "image/webp"
		case ".gif":
			contentType = "image/gif"
		default:
			contentType = "image/png"
		}
	}
	path, ok := s.avatarPath(userID, useExt)
	if !ok || !fileExists(path) {
		s.fail(w, r, http.StatusNotFound, "avatar_not_found", "头像文件不存在")
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "read_failed", err.Error())
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", itoa(len(b)))
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if st, err := os.Stat(path); err == nil {
		w.Header().Set("Last-Modified", st.ModTime().UTC().Format(http.TimeFormat))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// handleProfilePut PUT /api/profile（user）：昵称/简介长度限制 + HTML 转义。
func (s *Service) handleProfilePut(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		s.fail(w, r, http.StatusUnauthorized, "unauthorized", "需要登录")
		return
	}
	data, _, ok := middleware.ReadBody(w, r)
	if !ok {
		return
	}
	var req struct {
		Nickname *string `json:"nickname"`
		Bio      *string `json:"bio"`
	}
	if len(data) == 0 {
		s.fail(w, r, http.StatusBadRequest, "empty_body", "请求体不能为空")
		return
	}
	if err := json.Unmarshal(data, &req); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Nickname == nil && req.Bio == nil {
		s.fail(w, r, http.StatusBadRequest, "no_fields", "至少提供 nickname 或 bio")
		return
	}
	var nick, bio string
	var err error
	if req.Nickname != nil {
		nick, err = sanitizeText(*req.Nickname, "昵称", maxNicknameRunes)
		if err != nil {
			s.fail(w, r, http.StatusBadRequest, "invalid_nickname", err.Error())
			return
		}
	}
	if req.Bio != nil {
		bio, err = sanitizeText(*req.Bio, "简介", maxBioRunes)
		if err != nil {
			s.fail(w, r, http.StatusBadRequest, "invalid_bio", err.Error())
			return
		}
	}
	p, err := s.profiles.Update(u.ID, func(p *Profile) {
		if req.Nickname != nil {
			p.Nickname = nick
		}
		if req.Bio != nil {
			p.Bio = bio
		}
	})
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "profile_write_failed", err.Error())
		return
	}
	s.ok(w, r, map[string]interface{}{
		"ok":      true,
		"profile": publicProfileView(p, u.ID),
		"limits": map[string]interface{}{
			"nickname_runes": maxNicknameRunes, "bio_runes": maxBioRunes,
			"escaped": true,
		},
	})
}

// handleProfileGet GET /api/profile（user）：读取本人资料（无数据返回真实空态）。
func (s *Service) handleProfileGet(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		s.fail(w, r, http.StatusUnauthorized, "unauthorized", "需要登录")
		return
	}
	p := s.profiles.Get(u.ID)
	w.Header().Set("Cache-Control", "private, no-store")
	s.ok(w, r, map[string]interface{}{
		"user_id":   u.ID,
		"profile":   publicProfileView(p, u.ID),
		"has_data":  p != nil,
		"limits":    map[string]int{"nickname_runes": maxNicknameRunes, "bio_runes": maxBioRunes},
		"checked_at": time.Now().Unix(),
	})
}

// handlePublicProfile GET /api/profile/{user_id}（public）：公开资料，不含邮箱/角色/余额。
func (s *Service) handlePublicProfile(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	if !safeUserID.MatchString(userID) {
		s.fail(w, r, http.StatusBadRequest, "invalid_user_id", "用户 id 不符合安全字符集")
		return
	}
	if _, ok := s.st.GetUserByID(userID); !ok {
		s.fail(w, r, http.StatusNotFound, "user_not_found", "用户不存在")
		return
	}
	p := s.profiles.Get(userID)
	w.Header().Set("Cache-Control", "public, max-age=60")
	s.ok(w, r, map[string]interface{}{
		"profile":  publicProfileView(p, userID),
		"has_data": p != nil,
	})
}

// itoa 用标准库避免为格式化引入额外依赖（strconv 亦可，此处保持最小）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
