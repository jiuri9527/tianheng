// Package auth 提供注册/登录/JWT(HMAC-SHA256 自实现)/刷新/改密，零第三方依赖。
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"llm-hub/src/internal/config"
	"llm-hub/src/internal/crypto"
	"llm-hub/src/internal/store"
)

// Claims 是 JWT 载荷。
type Claims struct {
	Sub  string `json:"sub"`
	Role string `json:"role"`
	Type string `json:"type"` // access | refresh
	Exp  int64  `json:"exp"`
	Iat  int64  `json:"iat"`
}

var jwtHeader = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

// SignToken 用 HMAC-SHA256 签发 JWT。
func SignToken(secret string, c Claims) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	body := jwtHeader + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + sig, nil
}

// VerifyToken 校验签名与过期时间，返回载荷。alg 固定 HS256，不接受 none/RS。
func VerifyToken(secret, token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("JWT 格式错误")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("JWT 签名编码错误")
	}
	if !hmac.Equal(want, got) {
		return nil, errors.New("JWT 签名不匹配")
	}
	// 严格解析 header：alg 必须精确等于 HS256（strings.Contains 可被伪造字段绕过）
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("JWT header 编码错误")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil || hdr.Alg != "HS256" {
		return nil, errors.New("JWT alg 非 HS256")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("JWT 载荷编码错误")
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, errors.New("JWT 载荷解析错误")
	}
	if c.Exp <= time.Now().Unix() {
		return nil, errors.New("JWT 已过期")
	}
	return &c, nil
}

// Service 聚合 store + 密钥，提供用户与令牌操作。
// defaultBalanceUSD 是新注册用户的初始余额展示值（USD）：仅用于计量账本展示，
// 与模型数据无关；天衡下余额绝不再作为任何使用门禁（402 insufficient_quota 已移除）。
const defaultBalanceUSD = 50.0

type Service struct {
	store  *store.Store
	secret string
	ttl    time.Duration
}

// NewService 构造鉴权服务。
func NewService(st *store.Store, cfg *config.Config) *Service {
	return &Service{store: st, secret: cfg.JWTSecret, ttl: cfg.JWTTTL}
}

// Tokens 登录/刷新返回的一对令牌。
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

// issue 签发 JWT：角色取自存储中的用户记录，绝不硬编码，
// 使管理面门禁与渠道归属判断能基于真实角色（F1/F11）。
func (s *Service) issue(u *store.User) (*Tokens, error) {
	now := time.Now()
	// 天衡无角色区分：历史数据里可能残留 admin/user，一律按 superuser 签发，
	// 保证老账号在新语义下同样拥有完整权限（F1 的授权依据即"全部权限"）。
	role := "superuser"
	access, err := SignToken(s.secret, Claims{Sub: u.ID, Role: role, Type: "access", Iat: now.Unix(), Exp: now.Add(s.ttl).Unix()})
	if err != nil {
		return nil, err
	}
	refresh, err := SignToken(s.secret, Claims{Sub: u.ID, Role: role, Type: "refresh", Iat: now.Unix(), Exp: now.Add(7 * 24 * time.Hour).Unix()})
	if err != nil {
		return nil, err
	}
	return &Tokens{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer", ExpiresIn: int64(s.ttl.Seconds())}, nil
}

// registerMu 串行化注册，消除邮箱唯一性 check-then-act 竞态。
var registerMu sync.Mutex

// Register 注册用户。角色分配规则（F1）：
//   - 全站首个用户，或自举邮箱等于 ADMIN_EMAIL 的用户 → superuser；
//   - 其余一律 user。
//
// 自报邮箱在任何其他情况下都不产生特权。
func (s *Service) Register(email, password string, registrationOpen bool, adminEmail string) (*store.User, *Tokens, error) {
	registerMu.Lock()
	defer registerMu.Unlock()
	normalizedAdmin := strings.ToLower(strings.TrimSpace(adminEmail))
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, nil, errors.New("邮箱不合法")
	}
	if len(password) < 8 {
		return nil, nil, errors.New("口令至少 8 位")
	}
	if !registrationOpen && s.store.CountUsers() > 0 {
		return nil, nil, errors.New("注册已关闭")
	}
	if _, ok := s.store.GetUserByEmail(email); ok {
		return nil, nil, errors.New("邮箱已注册")
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return nil, nil, err
	}
	// 天衡：所有用户都是超级用户，没有任何管理员角色。
	// 不存在「首个用户才是管理员」的特例 —— 注册即 superuser，无差别。
	// （normalizedAdmin 仅用于日志归属，不参与角色判定。）
	_ = normalizedAdmin
	role := "superuser"
	now := time.Now()
	u := &store.User{
		ID: store.NewID("usr"), Email: email, PasswordHash: hash, Role: role,
		BalanceUSD: defaultBalanceUSD, Status: "active",
		CreatedAt: now.Unix(), UpdatedAt: now.Unix(),
	}
	if err := s.store.CreateUser(u); err != nil {
		return nil, nil, err
	}
	tok, err := s.issue(u)
	if err != nil {
		return nil, nil, err
	}
	return u, tok, nil
}

// errBadCredentials 统一登录失败错误，避免账号/状态枚举。
var errBadCredentials = errors.New("邮箱或口令错误")

// dummyPasswordHash 是合法格式的占位哈希，用于对不存在用户执行等量 KDF 运算，
// 消除通过响应时间枚举账号。
var dummyPasswordHash = "120000$" + strings.Repeat("0", 32) + "$" + strings.Repeat("0", 64)

// Login 校验口令并签发令牌。
func (s *Service) Login(email, password string) (*store.User, *Tokens, error) {
	u, ok := s.store.GetUserByEmail(strings.ToLower(strings.TrimSpace(email)))
	if !ok {
		_ = crypto.VerifyPassword(dummyPasswordHash, password)
		return nil, nil, errBadCredentials
	}
	if u.Status != "active" {
		return nil, nil, errBadCredentials
	}
	if !crypto.VerifyPassword(u.PasswordHash, password) {
		return nil, nil, errBadCredentials
	}
	tok, err := s.issue(u)
	if err != nil {
		return nil, nil, err
	}
	return u, tok, nil
}

// Refresh 用 refresh 令牌换发新的令牌对。
func (s *Service) Refresh(refreshToken string) (*Tokens, error) {
	c, err := VerifyToken(s.secret, refreshToken)
	if err != nil {
		return nil, err
	}
	if c.Type != "refresh" {
		return nil, errors.New("不是 refresh 令牌")
	}
	u, ok := s.store.GetUserByID(c.Sub)
	if !ok {
		return nil, errors.New("用户不存在")
	}
	return s.issue(u)
}

// Authenticate 校验 JWT access 令牌，返回用户。
func (s *Service) Authenticate(token string) (*store.User, *Claims, error) {
	c, err := VerifyToken(s.secret, token)
	if err != nil {
		return nil, nil, err
	}
	if c.Type != "access" {
		return nil, nil, errors.New("不是 access 令牌")
	}
	u, ok := s.store.GetUserByID(c.Sub)
	if !ok {
		return nil, nil, errors.New("用户不存在")
	}
	if u.Status != "active" {
		return nil, nil, errors.New("用户被禁用")
	}
	return u, c, nil
}

// ChangePassword 校验旧口令后更新为新口令哈希。
func (s *Service) ChangePassword(userID, oldPwd, newPwd string) error {
	u, ok := s.store.GetUserByID(userID)
	if !ok {
		return fmt.Errorf("用户不存在")
	}
	if !crypto.VerifyPassword(u.PasswordHash, oldPwd) {
		return errors.New("旧口令错误")
	}
	if len(newPwd) < 8 {
		return errors.New("新口令至少 8 位")
	}
	hash, err := crypto.HashPassword(newPwd)
	if err != nil {
		return err
	}
	// 锁内更新，避免对共享 *User 指针无锁写入（与 store.Save / AdjustQuota 竞态）。
	s.store.SetPasswordHash(userID, hash)
	return nil
}
