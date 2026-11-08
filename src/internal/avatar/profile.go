package avatar

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxNicknameRunes = 32
	maxBioRunes      = 280
)

// Profile 是单个用户的资料；昵称/简介保存的是 HTML 转义后的文本，
// 前端按纯文本渲染（并做实体解码回填编辑框），不执行、不注入 HTML。
type Profile struct {
	UserID            string `json:"user_id"`
	Nickname          string `json:"nickname"`
	Bio               string `json:"bio"`
	AvatarID          string `json:"avatar_id,omitempty"`
	AvatarExt         string `json:"avatar_ext,omitempty"`
	AvatarContentType string `json:"avatar_content_type,omitempty"`
	AvatarBytes       int    `json:"avatar_bytes,omitempty"`
	AvatarDecoded     bool   `json:"avatar_decoded,omitempty"`
	HasThumbnail      bool   `json:"has_thumbnail,omitempty"`
	UpdatedAt         int64  `json:"updated_at,omitempty"`
}

type profileFile struct {
	Profiles map[string]*Profile `json:"profiles"`
}

// ProfileStore 以 mutex 保护内存资料表，落盘 profiles.json（原子写、0600）。
// 文件不存在时为空表：禁止预置任何假资料。
type ProfileStore struct {
	mu       sync.RWMutex
	dir      string
	path     string
	profiles map[string]*Profile
}

func NewProfileStore(dir string) (*ProfileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ps := &ProfileStore{
		dir: dir, path: filepath.Join(dir, "profiles.json"),
		profiles: map[string]*Profile{},
	}
	b, err := os.ReadFile(ps.path)
	if err != nil {
		if os.IsNotExist(err) {
			return ps, nil // 没有任何资料，不创建假数据
		}
		return nil, err
	}
	var pf profileFile
	if err := json.Unmarshal(b, &pf); err != nil {
		return nil, fmt.Errorf("profiles.json 损坏，拒绝加载: %w", err)
	}
	if pf.Profiles != nil {
		for id, p := range pf.Profiles {
			if p == nil || !safeUserID.MatchString(id) {
				continue // 跳过非法记录，不采信文件内容里的用户 id
			}
			p.UserID = id
			ps.profiles[id] = p
		}
	}
	return ps, nil
}

// Get 返回资料副本（nil 表示该用户从未设置过资料）。
func (ps *ProfileStore) Get(userID string) *Profile {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	p := ps.profiles[userID]
	if p == nil {
		return nil
	}
	c := *p
	return &c
}

// Update 在锁内基于已有资料做修改，先写盘成功再提交内存，避免并发覆盖。
func (ps *ProfileStore) Update(userID string, fn func(p *Profile)) (*Profile, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	cur := &Profile{UserID: userID}
	if old := ps.profiles[userID]; old != nil {
		c := *old
		cur = &c
	}
	cur.UserID = userID
	fn(cur)
	cur.UpdatedAt = time.Now().Unix()

	next := make(map[string]*Profile, len(ps.profiles)+1)
	for k, v := range ps.profiles {
		next[k] = v
	}
	c := *cur
	next[userID] = &c

	b, err := json.MarshalIndent(profileFile{Profiles: next}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := atomicWrite(ps.path, append(b, '\n'), 0o600); err != nil {
		return nil, err
	}
	ps.profiles = next
	return &c, nil
}

// sanitizeText 校验 UTF-8、清除控制字符、按 rune 限长，并做 HTML 转义。
func sanitizeText(raw, label string, max int) (string, error) {
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("%s 含非法 UTF-8", label)
	}
	s := strings.TrimSpace(raw)
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r // 简介允许换行与制表
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if n := utf8.RuneCountInString(s); n > max {
		return "", fmt.Errorf("%s 超过 %d 字符限制（当前 %d）", label, max, n)
	}
	return html.EscapeString(s), nil
}

// publicProfileView 公开视图：绝不包含邮箱、角色、余额等敏感字段。
func publicProfileView(p *Profile, userID string) map[string]interface{} {
	nick, bio := "", ""
	if p != nil {
		nick, bio = p.Nickname, p.Bio
	}
	return map[string]interface{}{
		"user_id": userID, "nickname": nick, "bio": bio,
		"has_avatar": p != nil && p.AvatarExt != "",
		"avatar_url": "/api/avatar/" + userID,
	}
}
