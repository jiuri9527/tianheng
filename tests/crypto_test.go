package tests

import (
	"encoding/base64"
	"strings"
	"testing"

	"llm-hub/src/hub"
)

func TestCryptoRoundTrip(t *testing.T) {
	c, err := hub.NewCipher(testMasterKey()) // 固定测试主密钥
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	plaintext := []byte("sk-upstream-secret-1234")
	ct, iv, tag, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(ct) == 0 || len(iv) != 12 || len(tag) != 16 {
		t.Fatalf("密文分段异常: ct=%d iv=%d tag=%d", len(ct), len(iv), len(tag))
	}
	got, err := c.Decrypt(ct, iv, tag)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("解密不一致: got=%q want=%q", got, plaintext)
	}
}

func TestCryptoWithExplicit32ByteKey(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	c, err := hub.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	ct, iv, tag, _ := c.Encrypt([]byte("hello"))
	got, err := c.Decrypt(ct, iv, tag)
	if err != nil || string(got) != "hello" {
		t.Fatalf("显式密钥往返失败: got=%q err=%v", got, err)
	}
	if _, err := hub.NewCipher("!!!not-base64!!!"); err == nil {
		t.Fatal("非法 base64 应报错")
	}
	if _, err := hub.NewCipher(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Fatal("16 字节密钥应报错（必须 32 字节）")
	}
}

func TestCryptoTamperDetected(t *testing.T) {
	c, err := hub.NewCipher(testMasterKey())
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	ct, iv, tag, _ := c.Encrypt([]byte("sensitive"))
	ct[0] ^= 0xff
	if _, err := c.Decrypt(ct, iv, tag); err == nil {
		t.Fatal("篡改密文应被 GCM 校验拒绝")
	}
}

func TestCryptoKeyHintNeverRevealsFullKey(t *testing.T) {
	secret := "sk-upstream-secret-1234"
	hint := hub.KeyHint(secret)
	want := "****" + secret[len(secret)-4:]
	if hint != want {
		t.Fatalf("key_hint 错误: got %q want %q", hint, want)
	}
	if len(hint) != 8 || !strings.HasPrefix(hint, "****") {
		t.Fatalf("key_hint 格式应为 ****abcd: %q", hint)
	}
	// 中段不得出现在脱敏结果里
	mid := secret[4 : len(secret)-4]
	if mid != "" && strings.Contains(hint, mid) {
		t.Fatal("key_hint 泄露了密钥中段")
	}
	if hub.MaskSecret(secret) != hint {
		t.Fatal("MaskSecret 应与 KeyHint 一致")
	}
	if !strings.Contains(hub.Redact("auth="+secret, []string{secret}), want) {
		t.Fatal("Redact 未脱敏")
	}
}

func TestCryptoTokenHashAndPassword(t *testing.T) {
	a := hub.TokenHash("sk-abc")
	if a != hub.TokenHash("sk-abc") {
		t.Fatal("TokenHash 不确定")
	}
	if a == hub.TokenHash("sk-abd") {
		t.Fatal("不同令牌哈希不应相同")
	}
	if !hub.ConstantTimeEqual(a, hub.TokenHash("sk-abc")) {
		t.Fatal("ConstantTimeEqual 失败")
	}
	stored, err := hub.HashPassword("s3cret-pass")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !hub.VerifyPassword(stored, "s3cret-pass") {
		t.Fatal("正确口令应通过")
	}
	if hub.VerifyPassword(stored, "wrong") {
		t.Fatal("错误口令不应通过")
	}
	if strings.Contains(stored, "s3cret-pass") {
		t.Fatal("口令哈希不应含明文")
	}
}

func TestCryptoRandomHex(t *testing.T) {
	a, _ := hub.RandomHex(16)
	b, _ := hub.RandomHex(16)
	if len(a) != 32 || len(b) != 32 || a == b {
		t.Fatalf("RandomHex 异常: %q %q", a, b)
	}
}
