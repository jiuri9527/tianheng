package store

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- 辅助 ----------

func tmpDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func mustNew(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New(%s): %v", dir, err)
	}
	return s
}

func mustSave(t *testing.T, s *Store) {
	t.Helper()
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	got := st.Mode().Perm()
	if got != want {
		t.Fatalf("权限不符 %s: got %04o want %04o", path, got, want)
	}
}

// 模拟「WAL 已追加并 fsync，但表文件/清单尚未提交」的崩溃点。
func simulateWALCrash(t *testing.T, dir string, extra *User) {
	t.Helper()
	m, ok, err := readManifest(dir)
	if err != nil || !ok {
		t.Fatalf("readManifest: ok=%v err=%v", ok, err)
	}
	files := map[string][]byte{}
	for _, name := range tableNames {
		b, err := os.ReadFile(statePath(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				b = []byte("[]")
			} else {
				t.Fatal(err)
			}
		}
		if name == "users" {
			var users []*User
			if err := json.Unmarshal(b, &users); err != nil {
				t.Fatal(err)
			}
			users = append(users, extra)
			b, _ = json.MarshalIndent(users, "", "  ")
		}
		files[name] = b
	}
	canonical := makeSnapshotDoc(files)
	rec := walRecord{
		Seq: m.Sequence + 1, State: sha256hex(canonical), Prev: headOf(m),
		Snapshot: base64.StdEncoding.EncodeToString(canonical),
	}
	if err := appendWALRecord(dir, rec); err != nil {
		t.Fatalf("appendWALRecord: %v", err)
	}
	// 模拟表文件写到一半：写入损坏内容（恢复必须完全以 WAL 快照为准）
	if err := os.WriteFile(statePath(dir, "users"), []byte("CORRUPTED-PARTIAL-WRITE"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ---------- 1. 原子写 / 权限 / 目录 ----------

func TestAtomicWritePermissionsAndLayout(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	s.CreateUser(&User{ID: "u1", Email: "a@b.c", PasswordHash: "h", Role: "admin", Status: "active"})
	mustSave(t, s)

	assertPerm(t, dir, 0o700)
	assertPerm(t, backupsPath(dir), 0o700)
	assertPerm(t, manifestPath(dir), 0o600)
	assertPerm(t, walPath(dir), 0o600)
	for _, name := range tableNames {
		assertPerm(t, statePath(dir, name), 0o600)
	}
	backups, err := listBackups(dir)
	if err != nil || len(backups) != 1 {
		t.Fatalf("备份应 1 份: n=%d err=%v", len(backups), err)
	}
	for _, b := range backups {
		assertPerm(t, filepath.Join(backupsPath(dir), b), 0o600)
	}
	// 无遗留临时文件
	leftover, _ := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	if len(leftover) != 0 {
		t.Fatalf("遗留临时文件: %v", leftover)
	}
	// WAL 提交后已做检查点（清空）
	recs, _, err := readWAL(dir)
	if err != nil || len(recs) != 0 {
		t.Fatalf("WAL 应在提交后清空: n=%d err=%v", len(recs), err)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := VerifyDir(dir); err != nil {
		t.Fatalf("VerifyDir: %v", err)
	}
}

func TestHardenPermissionsTightensLooseFiles(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	mustSave(t, s)
	// 人为放宽松权限，HardenPermissions 必须收紧
	_ = os.Chmod(dir, 0o755)
	_ = os.Chmod(backupsPath(dir), 0o755)
	_ = os.Chmod(statePath(dir, "users"), 0o644)
	if err := s.HardenPermissions(); err != nil {
		t.Fatalf("HardenPermissions: %v", err)
	}
	assertPerm(t, dir, 0o700)
	assertPerm(t, backupsPath(dir), 0o700)
	assertPerm(t, statePath(dir, "users"), 0o600)
}

// ---------- 2. 完整性链 ----------

func TestIntegrityChainAdvances(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	prevHead := ""
	for i := 1; i <= 4; i++ {
		s.SetSetting(fmt.Sprintf("k%d", i), json.RawMessage(fmt.Sprintf(`"%d"`, i)))
		mustSave(t, s)
		st := s.IntegrityStatus()
		if st.Sequence != uint64(i) {
			t.Fatalf("sequence=%d want %d", st.Sequence, i)
		}
		if st.ChainLength != i {
			t.Fatalf("chainLen=%d want %d", st.ChainLength, i)
		}
		if st.Head == prevHead {
			t.Fatalf("第 %d 次写入链头未变化", i)
		}
		prevHead = st.Head
		if err := s.Verify(); err != nil {
			t.Fatalf("第 %d 次 Verify: %v", i, err)
		}
	}
	m, ok, err := readManifest(dir)
	if err != nil || !ok {
		t.Fatalf("readManifest: %v ok=%v", err, ok)
	}
	if m.Checksum != manifestChecksum(*m) {
		t.Fatal("清单自身校验和不匹配")
	}
	prev := genesisHash()
	for i, r := range m.Chain {
		if r.Prev != prev || r.Chain != chainHash(prev, r.State, r.Seq) {
			t.Fatalf("第 %d 环链校验失败", i)
		}
		prev = r.Chain
	}
	// 重新打开后链仍完整
	s2 := mustNew(t, dir)
	if s2.IntegrityStatus().Sequence != 4 || s2.IntegrityStatus().ChainLength != 4 {
		t.Fatalf("重开后链不完整: %+v", s2.IntegrityStatus())
	}
	if err := s2.Verify(); err != nil {
		t.Fatalf("重开 Verify: %v", err)
	}
}

// ---------- 3. 篡改检测（明确错误，绝不静默接受） ----------

func TestTamperDetectionTableFile(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	s.CreateUser(&User{ID: "u1", Email: "a@b.c", PasswordHash: "h", Role: "admin", Status: "active"})
	mustSave(t, s)

	// 篡改表文件一个字节
	p := statePath(dir, "users")
	b, _ := os.ReadFile(p)
	b[len(b)/2] ^= 0x01
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := s.Verify(); err == nil {
		t.Fatal("Verify 未检测到表文件篡改")
	} else if !IsIntegrityError(err) {
		t.Fatalf("错误类型应为 *IntegrityError: %T %v", err, err)
	} else if !strings.Contains(err.Error(), "篡改") {
		t.Fatalf("错误信息未指明篡改: %v", err)
	}
	if err := VerifyDir(dir); err == nil || !IsIntegrityError(err) {
		t.Fatalf("VerifyDir 未检测到篡改: %v", err)
	}
	if _, err := New(dir); err == nil || !IsIntegrityError(err) {
		t.Fatalf("New 启动时未拒绝被篡改数据: %v", err)
	} else {
		t.Logf("启动拒绝证据: %v", err)
	}
}

func TestTamperDetectionDeletedFile(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	mustSave(t, s)
	if err := os.Remove(statePath(dir, "tokens")); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir); err == nil || !IsIntegrityError(err) {
		t.Fatalf("删除状态文件应被拒绝: %v", err)
	}
}

func TestTamperDetectionManifestChecksum(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	mustSave(t, s)
	b, _ := os.ReadFile(manifestPath(dir))
	// 修改清单字段但不重算 checksum
	b = []byte(strings.Replace(string(b), `"sequence": 1`, `"sequence": 999`, 1))
	if err := os.WriteFile(manifestPath(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir); err == nil || !IsIntegrityError(err) {
		t.Fatalf("清单自身篡改应被拒绝: %v", err)
	} else if !strings.Contains(err.Error(), "state.integrity") {
		t.Fatalf("错误信息应指明 state.integrity: %v", err)
	}
}

func TestTamperDetectionChainRing(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	for i := 0; i < 3; i++ {
		s.SetSetting(fmt.Sprintf("k%d", i), json.RawMessage(`1`))
		mustSave(t, s)
	}
	// 篡改中间一环的 State，并「正确地」重算清单 checksum，专门验证链环数学校验
	m, _, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Chain[1].State = strings.Repeat("0", 64)
	m.Checksum = manifestChecksum(*m)
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(manifestPath(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir); err == nil || !IsIntegrityError(err) {
		t.Fatalf("链环篡改应被拒绝: %v", err)
	} else if !strings.Contains(err.Error(), "完整性链") {
		t.Fatalf("错误信息应指明完整性链: %v", err)
	}
}

func TestTamperDetectionWALRecord(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	mustSave(t, s)
	m, _, _ := readManifest(dir)
	files := map[string][]byte{}
	for _, name := range tableNames {
		b, _ := os.ReadFile(statePath(dir, name))
		files[name] = b
	}
	canonical := makeSnapshotDoc(files)
	rec := walRecord{Seq: m.Sequence + 1, State: sha256hex(canonical), Prev: headOf(m),
		Snapshot: base64.StdEncoding.EncodeToString(canonical)}
	if err := appendWALRecord(dir, rec); err != nil {
		t.Fatal(err)
	}
	// 篡改 WAL 记录的快照内容
	wp := walPath(dir)
	wb, _ := os.ReadFile(wp)
	wb = []byte(strings.Replace(string(wb), `"state":"`+rec.State+`"`, `"state":"`+strings.Repeat("f", 64)+`"`, 1))
	if err := os.WriteFile(wp, wb, 0o600); err != nil {
		t.Fatal(err)
	}
	// 损坏的 WAL 记录被丢弃（视为崩溃时的半截写入），不得影响已提交状态
	s2 := mustNew(t, dir)
	if err := s2.Verify(); err != nil {
		t.Fatalf("损坏 WAL 不应破坏已提交状态: %v", err)
	}
	if s2.IntegrityStatus().Sequence != m.Sequence {
		t.Fatalf("sequence 被异常推进: %d", s2.IntegrityStatus().Sequence)
	}
}

// ---------- 4. 崩溃恢复（WAL 重放） ----------

func TestWALCrashRecoveryOnNew(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	s.CreateUser(&User{ID: "u1", Email: "a@b.c", PasswordHash: "h", Role: "admin", Status: "active"})
	mustSave(t, s)
	oldSeq := s.IntegrityStatus().Sequence

	extra := &User{ID: "u2", Email: "recovered@b.c", PasswordHash: "h2", Role: "user", Status: "active"}
	simulateWALCrash(t, dir, extra)

	s2, err := New(dir)
	if err != nil {
		t.Fatalf("崩溃后 New 应自动从 WAL 恢复: %v", err)
	}
	u, ok := s2.GetUserByID("u2")
	if !ok || u.Email != "recovered@b.c" {
		t.Fatalf("WAL 恢复的数据缺失: ok=%v u=%+v", ok, u)
	}
	st := s2.IntegrityStatus()
	if !st.Recovered || st.Sequence != oldSeq+1 {
		t.Fatalf("恢复标记/序列错误: %+v (oldSeq=%d)", st, oldSeq)
	}
	if err := s2.Verify(); err != nil {
		t.Fatalf("恢复后 Verify: %v", err)
	}
	t.Logf("崩溃恢复证据: %+v", st)
}

func TestRecoverExplicit(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	mustSave(t, s)
	extra := &User{ID: "u9", Email: "explicit@b.c", PasswordHash: "h", Role: "user", Status: "active"}
	simulateWALCrash(t, dir, extra)
	if err := s.Verify(); !errors.Is(err, ErrWALUncommitted) {
		t.Fatalf("Verify 应报告未提交 WAL: %v", err)
	}
	if err := s.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if _, ok := s.GetUserByID("u9"); !ok {
		t.Fatal("Recover 后内存数据库未重载 WAL 数据")
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Recover 后 Verify: %v", err)
	}
}

func TestWALTrailingGarbageDiscarded(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	s.CreateUser(&User{ID: "u1", Email: "a@b.c", PasswordHash: "h", Role: "admin", Status: "active"})
	mustSave(t, s)
	// 追加半截 WAL 行
	f, err := os.OpenFile(walPath(dir), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"seq":999,"state":"broken"`)
	_ = f.Close()

	s2, err := New(dir)
	if err != nil {
		t.Fatalf("尾部半截 WAL 不应阻止启动: %v", err)
	}
	if _, ok := s2.GetUserByID("u1"); !ok {
		t.Fatal("已提交数据丢失")
	}
	if err := s2.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, trailing, _ := readWAL(dir); trailing {
		t.Fatal("尾部损坏 WAL 应在启动检查点被清空")
	}
}

// ---------- 5. 备份轮转 ----------

func TestBackupRotationKeepsLastN(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	s.SetBackupRetention(3)
	for i := 0; i < 6; i++ {
		s.SetSetting("k", json.RawMessage(fmt.Sprintf(`%d`, i)))
		mustSave(t, s)
	}
	backups, err := listBackups(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 3 {
		t.Fatalf("应保留 3 份备份，实际 %d: %v", len(backups), backups)
	}
	for i, b := range backups {
		want := fmt.Sprintf("snapshot-%020d.json", uint64(4+i))
		if b != want {
			t.Fatalf("备份轮转顺序错误 [%d]=%s want %s", i, b, want)
		}
	}
	if st := s.IntegrityStatus(); st.BackupRetention != 3 || st.Backups != 3 {
		t.Fatalf("status 备份信息错误: %+v", st)
	}
	path, err := s.Backup()
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Backup 文件不存在: %v", err)
	}
	assertPerm(t, path, 0o600)
}

// ---------- 6. 并发写（go test -race 验证） ----------

func TestConcurrentWritesNoRace(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	// 预置一个有余额的用户与令牌，供 ApplyBilling 使用
	s.CreateUser(&User{ID: "owner", Email: "owner@b.c", PasswordHash: "h", Role: "admin",
		BalanceUSD: 1e6, Status: "active"})
	s.CreateToken(&Token{ID: "tok", UserID: "owner", Name: "n", TokenHash: "hash", QuotaUSD: 1e6, Status: "active"})
	mustSave(t, s)

	const workers, perWorker = 8, 25
	var wg sync.WaitGroup
	errCh := make(chan error, workers*perWorker*2)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := fmt.Sprintf("u-%d-%d", w, i)
				s.CreateUser(&User{ID: id, Email: id + "@b.c", PasswordHash: "h",
					Role: "user", Status: "active"})
				s.SetSetting("s-"+id, json.RawMessage(`1`))
				s.AddUsage("2026-11-07", "owner", "alias", 1, 2, 3, 0.001)
				s.AdjustQuota("owner", -0.0001, "consumption", id)
				s.ApplyBilling("tok", "owner", "", 0.0001, 0, id)
				if i%5 == 0 {
					if err := s.Save(); err != nil {
						errCh <- err
					}
				}
			}
		}(w)
	}
	// 并发只读 + 并发 Verify
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = s.CountUsers()
				_ = s.ListUsers()
				_ = s.ListSettings()
				_ = s.Counts()
				_ = s.IntegrityStatus()
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("并发 Save 失败: %v", err)
	}
	mustSave(t, s)

	want := 1 + workers*perWorker // 预置 1 个 + 并发创建
	if got := s.CountUsers(); got != want {
		t.Fatalf("并发写入丢更新: users=%d want=%d", got, want)
	}
	usage := s.ListUsage(UsageFilter{UserID: "owner"})
	if len(usage) != 1 || usage[0].Requests != workers*perWorker {
		t.Fatalf("用量聚合丢更新: %+v", usage)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("并发后完整性校验失败: %v", err)
	}
	// 重新打开：数据不丢、链完整
	s2 := mustNew(t, dir)
	if got := s2.CountUsers(); got != want {
		t.Fatalf("重开后用户数不符: %d want %d", got, want)
	}
	if err := s2.Verify(); err != nil {
		t.Fatalf("重开后完整性校验失败: %v", err)
	}
	t.Logf("并发写证据: users=%d chain=%d seq=%d", want,
		s2.IntegrityStatus().ChainLength, s2.IntegrityStatus().Sequence)
}

// ---------- 7. 字段级加密与口令哈希不变 ----------

func TestFieldEncryptionAndPasswordHashUnchanged(t *testing.T) {
	dir := tmpDir(t)
	s := mustNew(t, dir)
	plaintext := "sk-super-secret-upstream-key" // 仅作为测试夹具，绝不写入状态文件
	cipher := []byte{0x01, 0x02, 0x03, 0xfe, 0xff}
	iv := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	tag := []byte{0x11, 0x22, 0x33, 0x44}
	s.CreateUser(&User{ID: "u1", Email: "a@b.c", PasswordHash: "iterated$sha256$hash", Role: "admin", Status: "active"})
	s.CreateChannel(&Channel{
		ID: "ch1", OwnerID: "u1", Name: "upstream", ProviderType: "openai",
		BaseURI: "https://api.example.com/v1", AuthStyle: "bearer",
		APIKeyCipher: cipher, APIKeyIV: iv, APIKeyTag: tag, KeyHint: "****-key",
		Visibility: "public", Status: "active",
	})
	mustSave(t, s)

	// 原始状态文件：密文以 base64 存在，明文绝不出现
	raw, err := os.ReadFile(statePath(dir, "channels"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), plaintext) {
		t.Fatal("状态文件出现上游 api_key 明文")
	}
	if !strings.Contains(string(raw), base64.StdEncoding.EncodeToString(cipher)) {
		t.Fatal("api_key_cipher 密文未逐字落库")
	}
	if !strings.Contains(string(raw), "key_hint") || !strings.Contains(string(raw), "****-key") {
		t.Fatal("key_hint 未落库")
	}
	usersRaw, _ := os.ReadFile(statePath(dir, "users"))
	if !strings.Contains(string(usersRaw), "iterated$sha256$hash") {
		t.Fatal("口令哈希被改动")
	}

	// 重新打开：三段密文逐字还原，哈希不变
	s2 := mustNew(t, dir)
	ch, ok := s2.GetChannel("ch1")
	if !ok {
		t.Fatal("渠道丢失")
	}
	if !bytesEqual(ch.APIKeyCipher, cipher) || !bytesEqual(ch.APIKeyIV, iv) || !bytesEqual(ch.APIKeyTag, tag) {
		t.Fatalf("密文三段还原不一致: %v %v %v", ch.APIKeyCipher, ch.APIKeyIV, ch.APIKeyTag)
	}
	u, _ := s2.GetUserByID("u1")
	if u.PasswordHash != "iterated$sha256$hash" {
		t.Fatalf("口令哈希被改动: %q", u.PasswordHash)
	}
	_ = plaintext
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------- 8. 遗留数据 TOFU 采纳 / Strict ----------

func TestLegacyAdoptionAndStrictRejection(t *testing.T) {
	// TOFU 采纳：遗留表文件（无 state.integrity）→ 建立基线，之后任何改动被严格检测
	dir := tmpDir(t)
	legacy := `[{"id":"legacy1","email":"l@b.c","password_hash":"h","role":"user","balance_usd":0,"status":"active","created_at":0,"updated_at":0}]`
	if err := os.WriteFile(statePath(dir, "users"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s := mustNew(t, dir)
	if !s.IntegrityStatus().LegacyAdopted {
		t.Fatal("未标记为遗留数据采纳")
	}
	if _, ok := s.GetUserByID("legacy1"); !ok {
		t.Fatal("遗留数据丢失")
	}
	if _, err := os.Stat(manifestPath(dir)); err != nil {
		t.Fatalf("未建立 state.integrity: %v", err)
	}
	// 采纳后篡改必须被拒绝
	mustSave(t, s)
	b, _ := os.ReadFile(statePath(dir, "users"))
	b[len(b)/2] ^= 0x01
	_ = os.WriteFile(statePath(dir, "users"), b, 0o600)
	if _, err := New(dir); err == nil || !IsIntegrityError(err) {
		t.Fatalf("采纳后篡改应被拒绝: %v", err)
	}

	// Strict：同样遗留文件直接报错
	dir2 := tmpDir(t)
	if err := os.WriteFile(statePath(dir2, "users"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWithOptions(dir2, Options{Strict: true, BackupRetention: 2}); !errors.Is(err, ErrIntegrityMissing) {
		t.Fatalf("Strict 应拒绝遗留数据: %v", err)
	}
}

func TestCorruptLegacyJSONRejected(t *testing.T) {
	dir := tmpDir(t)
	if err := os.WriteFile(statePath(dir, "users"), []byte(`{not-json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir); err == nil || !IsIntegrityError(err) {
		t.Fatalf("损坏的遗留 JSON 应被拒绝: %v", err)
	}
}

// ---------- 9. 纯内存 / 新目录 ----------

func TestInMemoryAndFreshDir(t *testing.T) {
	s := mustNew(t, "")
	if err := s.Save(); err != nil {
		t.Fatalf("纯内存 Save: %v", err)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("纯内存 Verify: %v", err)
	}
	s.CreateUser(&User{ID: "m1", Email: "m@b.c", PasswordHash: "h", Role: "admin", Status: "active"})
	if s.CountUsers() != 1 {
		t.Fatal("纯内存写入失败")
	}
	if err := VerifyDir(""); err != nil {
		t.Fatalf("VerifyDir(\"\"): %v", err)
	}
	fresh := tmpDir(t)
	if err := VerifyDir(fresh); err != nil {
		t.Fatalf("空目录 VerifyDir: %v", err)
	}
	if _, err := New(fresh); err != nil {
		t.Fatalf("空目录 New: %v", err)
	}
	// 空目录不应凭空造出状态文件
	if _, err := os.Stat(manifestPath(fresh)); !os.IsNotExist(err) {
		t.Fatalf("空目录不应预先写 state.integrity: %v", err)
	}
}

// ---------- 10. 快照文档确定性 ----------

func TestSnapshotDeterministicAndStateHash(t *testing.T) {
	files := map[string][]byte{
		"users":    []byte(`[{"id":"a"}]`),
		"channels": []byte(`[]`),
		"settings": []byte(`[]`),
	}
	a := makeSnapshotDoc(files)
	b := makeSnapshotDoc(files)
	if !bytesEqual(a, b) || sha256hex(a) != sha256hex(b) {
		t.Fatal("快照文档非确定性")
	}
	parsed, err := parseSnapshotDoc(a)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(parsed["users"], files["users"]) {
		t.Fatal("快照重放字节不一致")
	}
	h := sha256.Sum256(a)
	if hex.EncodeToString(h[:]) != sha256hex(a) {
		t.Fatal("sha256hex 与 crypto/sha256 不一致")
	}
	_ = time.Now()
}
