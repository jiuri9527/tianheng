// 文件: src/internal/firewall/firewall_test.go
package firewall

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestFW(t *testing.T) *Firewall {
	t.Helper()
	cfg := DefaultConfig(t.TempDir())
	cfg.AuthFailureThreshold = 3
	cfg.BlockScore = 6
	cfg.LockdownIPThreshold = 2
	cfg.LockdownTTL = 80 * time.Millisecond
	cfg.BlockTTL = time.Second
	return New(cfg)
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func req(ip, path, ua string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = ip + ":41234"
	r.Header.Set("User-Agent", ua)
	r.Header.Set("X-Request-Id", "rid-test-0001")
	return r
}

func TestRedactNeverKeepsCredentialPlaintext(t *testing.T) {
	in := "POST /api/channels api_key=sk-abcdefghijklmnopqrstuvwxyz Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.x.y x-api-key: secretvalue123456"
	out := Redact(in)
	for _, secret := range []string{"sk-abcdefghijklmnopqrstuvwxyz", "wjIstTjgSgKMQxU9ZtS7", "secretvalue123456"} {
		if strings.Contains(out, secret) {
			t.Fatalf("脱敏失败，仍含明文 %q: %s", secret, out)
		}
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("未标记脱敏: %s", out)
	}
}

func TestNormalTrafficStaysOpen(t *testing.T) {
	fw := newTestFW(t)
	h := fw.Middleware(okHandler())
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req("203.0.113.9", "/v1/models", "Mozilla/5.0"))
		if rec.Code != http.StatusOK {
			t.Fatalf("正常请求被误拦: %d", rec.Code)
		}
	}
	if fw.Level() != LevelObserve || fw.Sealed() {
		t.Fatalf("正常流量触发了保护: level=%s sealed=%v", fw.Level(), fw.Sealed())
	}
}

func TestAttackTriggersAutomaticLockdownAndSealsData(t *testing.T) {
	fw := newTestFW(t)
	h := fw.Middleware(okHandler())

	// 两个不同来源持续注入攻击：path_traversal(critical) + scanner_ua(medium)
	// → 首个 IP 自动封禁 → 第二个 IP 封禁数达阈值 → 全局 lockdown + 数据封存
	for _, ip := range []string{"198.51.100.7", "198.51.100.8"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req(ip, "/v1/models?path=../etc/passwd", "sqlmap/1.5"))
	}

	if fw.Level() != LevelLockdown {
		t.Fatalf("攻击未触发全局自动保护: level=%s", fw.Level())
	}
	if !fw.Sealed() {
		t.Fatal("自动保护未封存敏感数据")
	}
	if fw.SessionEpoch() < 1 {
		t.Fatal("会话 epoch 未递增")
	}

	// lockdown 下所有业务端点被外层硬阻断，后端敏感数据完全不可达
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req("203.0.113.20", "/api/channels", "Mozilla/5.0"))
	if rec.Code != http.StatusLocked {
		t.Fatalf("lockdown 未阻断业务请求: %d", rec.Code)
	}
	// /healthz 仍可用（存活探针）
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req("203.0.113.20", "/healthz", "Mozilla/5.0"))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz 在 lockdown 下应可用: %d", rec.Code)
	}

	// 审计事件含 auto_lockdown，且哈希链完整、无凭据明文
	events := fw.Events(100)
	found := false
	for _, e := range events {
		if e.Rule == "auto_lockdown" {
			found = true
		}
		if e.Hash == "" {
			t.Fatalf("事件缺少 hash: %+v", e)
		}
	}
	if !found {
		t.Fatal("未记录 auto_lockdown 事件")
	}
	v := fw.VerifyData()
	if v["verified"] != true || v["credential_leaks"].(int) != 0 || v["hash_chain_intact"] != true {
		t.Fatalf("硬数据保护自检失败: %+v", v)
	}

	// 快照真实落盘且 0600、不含任何密钥明文
	dir := fw.cfg.StateDir
	entries, err := os.ReadDir(filepath.Join(dir, "firewall"))
	if err != nil {
		t.Fatal(err)
	}
	snapshots := 0
	for _, de := range entries {
		if !strings.HasPrefix(de.Name(), "snapshot-") {
			continue
		}
		snapshots++
		info, _ := de.Info()
		if info.Mode().Perm() != 0600 {
			t.Fatalf("快照权限应为 0600，实际 %o", info.Mode().Perm())
		}
		b, _ := os.ReadFile(filepath.Join(dir, "firewall", de.Name()))
		if strings.Contains(string(b), "sk-") || strings.Contains(string(b), "Bearer") {
			t.Fatalf("快照含凭据明文: %s", string(b))
		}
	}
	if snapshots == 0 {
		t.Fatal("自动保护未写审计快照")
	}
	t.Logf("auto lockdown verified: events=%d snapshots=%d epoch=%d", len(events), snapshots, fw.SessionEpoch())
}

func TestUnlockRestoresService(t *testing.T) {
	fw := newTestFW(t)
	h := fw.Middleware(okHandler())
	fw.Seal("drill")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req("203.0.113.9", "/v1/models", "Mozilla/5.0"))
	if rec.Code != http.StatusLocked {
		t.Fatalf("seal 后应阻断: %d", rec.Code)
	}
	fw.Unlock("admin-test")
	if fw.Level() != LevelObserve || fw.Sealed() {
		t.Fatalf("unlock 未恢复: level=%s sealed=%v", fw.Level(), fw.Sealed())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req("203.0.113.9", "/v1/models", "Mozilla/5.0"))
	if rec.Code != http.StatusOK {
		t.Fatalf("unlock 后请求仍被拦: %d", rec.Code)
	}
}

func TestSingleSourceCannotTriggerGlobalLockdown(t *testing.T) {
	// V-39：单台主机重复高危探测只能封它自己，绝不能把全站打成 423。
	cfg := DefaultConfig(t.TempDir())
	cfg.BlockScore = 6
	cfg.LockdownIPThreshold = 2 // 需要 ≥2 个外部 IP 被封才升级
	fw := New(cfg)
	h := fw.Middleware(okHandler())

	// 同一来源连续 6 次路径穿越（critical，每次 +6 分）
	for i := 0; i < 6; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req("198.51.100.66", "/v1/models?f=../../etc/passwd", "Mozilla/5.0"))
	}
	if fw.Level() == LevelLockdown || fw.Sealed() {
		t.Fatalf("单一来源不应触发全局 lockdown: level=%s sealed=%v", fw.Level(), fw.Sealed())
	}
	// 该来源应被自身封禁（429）
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req("198.51.100.66", "/v1/models", "Mozilla/5.0"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("攻击源应被自身封禁 429，实际 %d", rec.Code)
	}
	// 其他正常用户必须仍然可用
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req("203.0.113.50", "/v1/models", "Mozilla/5.0"))
	if rec.Code != http.StatusOK {
		t.Fatalf("攻击期间正常用户应 200，实际 %d", rec.Code)
	}
	t.Logf("PASS: 单源 6 次 critical 探测 → 自身 429，全站仍 200，level=%s sealed=%v", fw.Level(), fw.Sealed())
}

func TestLoopbackNeverEscalatesToLockdown(t *testing.T) {
	// V-39：本机回环（运维/健康检查/本地监控）不参与全局封存升级。
	cfg := DefaultConfig(t.TempDir())
	cfg.BlockScore = 6
	cfg.LockdownIPThreshold = 1
	fw := New(cfg)
	h := fw.Middleware(okHandler())

	for i := 0; i < 6; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req("127.0.0.1", "/v1/models?f=../../etc/passwd", "Mozilla/5.0"))
	}
	if fw.Sealed() {
		t.Fatal("回环来源不得触发全局数据封存")
	}
}

func TestLoopbackNeverGetsIPBanned(t *testing.T) {
	// V-40：单机/容器部署下客户端即回环，回环被封等于运维自锁。
	cfg := DefaultConfig(t.TempDir())
	cfg.BlockScore = 6
	fw := New(cfg)
	h := fw.Middleware(okHandler())

	for i := 0; i < 8; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req("127.0.0.1", "/v1/models?f=../../etc/passwd", "sqlmap/1.5"))
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次回环请求不应被封禁，实际 %d", i+1, rec.Code)
		}
	}
	// 事件仍必须完整记录（检测可见性不丢）
	found := false
	for _, e := range fw.Events(100) {
		if e.Rule == "path_traversal" && e.Action == "observe_loopback" {
			found = true
		}
	}
	if !found {
		t.Fatal("回环攻击事件必须仍被记录（action=observe_loopback）")
	}
	if fw.Sealed() {
		t.Fatal("回环不得触发全局封存")
	}
	t.Logf("PASS: 回环 8 次攻击 → 全部 200（不封禁），事件仍记录，sealed=false")
}

func TestSealedAuthRecoveryLoopbackOnly(t *testing.T) {
	// V-38：封存期间认证端点对远程全封（防攻击者注册后解锁），
	// 对回环开放恢复通道（运维可在无预存 JWT 时登录再解锁）。
	fw := newTestFW(t)
	fw.Seal("drill")
	auth := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"x"}`))
	})
	h := fw.Middleware(auth)

	remote := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	remote.RemoteAddr = "198.51.100.9:41234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, remote)
	if rec.Code != http.StatusLocked {
		t.Fatalf("远程 login 在封存期间应 423，实际 %d", rec.Code)
	}

	local := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	local.RemoteAddr = "127.0.0.1:41234"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, local)
	if rec.Code != http.StatusOK {
		t.Fatalf("回环 login 恢复通道应 200，实际 %d", rec.Code)
	}
}

func TestCooldownDowngradesButKeepsSealed(t *testing.T) {
	fw := newTestFW(t)
	fw.Seal("drill")
	time.Sleep(100 * time.Millisecond)
	fw.mu.Lock()
	fw.maybeRecoverLocked(time.Now())
	fw.mu.Unlock()
	if fw.Level() != LevelObserve {
		t.Fatalf("冷却后应降级: %s", fw.Level())
	}
	if !fw.Sealed() {
		t.Fatal("冷却不得自动解除数据封存，必须显式 unseal")
	}
}
