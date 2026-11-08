// Package crypto 提供 AES-256-GCM 加解密、key_hint、日志脱敏与口令迭代哈希。
// 全部基于 Go 标准库，无任何第三方依赖。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const gcmNonceSize = 12
const gcmTagSize = 16

// Cipher 持有 32 字节 AES-256 主密钥。
type Cipher struct {
	key []byte
}

// NewCipher 依据 base64 主密钥构造 Cipher。MASTER_KEY 缺失时拒绝启动，
// 绝不回退到源码内可推导的开发密钥（否则所有未配置部署共享同一把 AES 主密钥）。
func NewCipher(masterKeyB64 string) (*Cipher, error) {
	if strings.TrimSpace(masterKeyB64) == "" {
		return nil, errors.New("MASTER_KEY 未配置：拒绝使用开发派生密钥，请注入 32 字节 base64 主密钥")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(masterKeyB64))
	if err != nil {
		return nil, fmt.Errorf("MASTER_KEY 不是合法 base64: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("MASTER_KEY 解码后必须 32 字节, 实际 %d", len(raw))
	}
	return &Cipher{key: raw}, nil
}

// Encrypt 用 AES-256-GCM 加密明文，返回密文/IV/TAG 三段（与 channels 表三列对应）。
// GCM.Seal 输出为 ciphertext||tag，这里把末尾 16 字节拆为 tag 单独返回。
func (c *Cipher) Encrypt(plaintext []byte) (cipherText, iv, tag []byte, err error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, nil, err
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, nil, err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, nil)
	if len(sealed) < gcmTagSize {
		return nil, nil, nil, errors.New("GCM 输出异常短")
	}
	ct := sealed[:len(sealed)-gcmTagSize]
	tg := sealed[len(sealed)-gcmTagSize:]
	return ct, nonce, tg, nil
}

// Decrypt 依据密文/IV/TAG 还原明文。
func (c *Cipher) Decrypt(cipherText, iv, tag []byte) ([]byte, error) {
	if len(iv) != gcmNonceSize {
		return nil, fmt.Errorf("IV 长度错误: %d", len(iv))
	}
	if len(tag) != gcmTagSize {
		return nil, fmt.Errorf("TAG 长度错误: %d", len(tag))
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sealed := make([]byte, 0, len(cipherText)+len(tag))
	sealed = append(sealed, cipherText...)
	sealed = append(sealed, tag...)
	plain, err := gcm.Open(nil, iv, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("AES-GCM 解密失败: %w", err)
	}
	return plain, nil
}

// KeyHint 生成 "****" + 真实密钥末 4 位的提示，绝不回显完整密钥。
func KeyHint(secret string) string {
	s := strings.TrimSpace(secret)
	if len(s) <= 4 {
		return "****" // 短密钥不得回显任何字符，避免完整明文出现在 key_hint / 日志
	}
	return "****" + s[len(s)-4:]
}

// MaskSecret 把任意字符串按 key_hint 规则脱敏，用于日志与响应。
func MaskSecret(secret string) string { return KeyHint(secret) }

// Redact 在一段文本中把出现的敏感串替换为脱敏形式（用于请求体/日志脱敏）。
func Redact(text string, secrets []string) string {
	for _, s := range secrets {
		if s == "" {
			continue
		}
		text = strings.ReplaceAll(text, s, KeyHint(s))
	}
	return text
}

// TokenHash 返回平台令牌的 sha256 十六进制摘要（数据库只存哈希）。
func TokenHash(sk string) string {
	h := sha256.Sum256([]byte(sk))
	return hex.EncodeToString(h[:])
}

// ConstantTimeEqual 常量时间比较，用于令牌/签名校验。
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

const defaultIterations = 120000

// HashPassword 使用「argon 风格」迭代 sha256 KDF：
// 随机 16 字节 salt + iterations 次 sha256(salt||password||counter)，
// 结果编码为 iterations$saltHex$hashHex。仅用标准库实现一个带盐迭代派生。
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}
	hash := derive(password, salt, defaultIterations)
	return strconv.Itoa(defaultIterations) + "$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(hash), nil
}

func derive(password string, salt []byte, iterations int) []byte {
	acc := make([]byte, 32)
	copy(acc, salt)
	pwd := []byte(password)
	for i := 0; i < iterations; i++ {
		h := sha256.New()
		h.Write(acc)
		h.Write(pwd)
		// counter 参与，避免固定点
		c := byte(i & 0xff)
		h.Write([]byte{c, byte((i >> 8) & 0xff), byte((i >> 16) & 0xff), byte((i >> 24) & 0xff)})
		acc = h.Sum(acc[:0])
	}
	return acc
}

// VerifyPassword 校验迭代哈希口令。
func VerifyPassword(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 3 {
		return false
	}
	iterations, err := strconv.Atoi(parts[0])
	if err != nil || iterations <= 0 || iterations > 10000000 {
		return false
	}
	salt, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	got := derive(password, salt, iterations)
	return hmac.Equal(got, want)
}

// RandomHex 返回 n 字节随机数据的十六进制字符串（用于生成 sk- 令牌与 id）。
func RandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
