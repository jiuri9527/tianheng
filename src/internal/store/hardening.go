// Package store —— 数据库层「绝对强硬」加固实现。
//
// 本文件只在 src/internal/store/ 内部新增实现，不改动任何导出类型/函数签名。
// 提供：
//  1. 原子写：os.CreateTemp(0600) → Write → File.Sync → Close → Rename → 目录 fsync；
//     所有状态文件 0600、数据目录与备份目录 0700。
//  2. SHA-256 完整性链：每个提交写入 state.integrity，含全链记录（seq/prev/state/chain），
//     启动时逐环校验 + 逐表 SHA-256 校验；任何篡改/删除返回明确的 *IntegrityError，绝不静默接受。
//  3. WAL/快照：写前追加 WAL（含完整快照 + 校验和 + fsync），崩溃后从 WAL 重放恢复；
//     备份轮转只保留最近 N 份。
//  4. 所有落盘写路径由 writeMu 串行化，db/索引由 mu 保护，消除并发竞态。
//  5. 字段级加密沿用 crypto.AES-256-GCM（api_key 三段密文）与口令哈希，本文件不解密、不回显明文。
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
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- 常量 ----------

const (
	integrityFileName      = "state.integrity"
	walFileName            = "wal.log"
	backupsDirName         = "backups"
	defaultBackupRetention = 5
	maxBackupRetention     = 100
	genesisSeed            = "llm-hub-store-integrity-genesis-v1"
	integrityFormatVersion = 2
)

var (
	// ErrIntegrityMissing 表示存在状态文件但缺少完整性基线，Strict 模式下拒绝加载。
	ErrIntegrityMissing = errors.New("state.integrity 缺失：拒绝在缺少完整性基线的情况下加载已有状态文件")
	// ErrWALUncommitted 表示存在比当前提交更新的 WAL 记录（疑似上次写入崩溃）。
	ErrWALUncommitted = errors.New("检测到未提交的 WAL 记录（疑似上次写入崩溃），请调用 Recover() 恢复")
)

// ---------- 新增导出类型 ----------

// Options 是 NewWithOptions 的加固选项。零值即安全默认（启用 WAL、保留 5 份备份、TOFU 采纳遗留数据）。
type Options struct {
	BackupRetention int  // 备份轮转保留份数；<=0 取默认 5，>100 钳制到 100
	Strict          bool // 为 true 时，存在遗留状态文件但无 state.integrity 直接报错（拒绝 TOFU 采纳）
	DisableWAL      bool // 为 true 时关闭 WAL（不推荐，仅用于特殊离线分析）
}

// DefaultOptions 返回安全默认选项。
func DefaultOptions() Options {
	return Options{BackupRetention: defaultBackupRetention}
}

// IntegrityError 是明确的完整性校验失败（含被篡改文件与期望/实际摘要）。
type IntegrityError struct {
	File     string
	Reason   string
	Expected string
	Actual   string
}

func (e *IntegrityError) Error() string {
	return fmt.Sprintf("store 完整性校验失败 [%s]: %s (expected=%s actual=%s)",
		e.File, e.Reason, e.Expected, e.Actual)
}

// IsIntegrityError 判断错误是否为完整性校验失败。
func IsIntegrityError(err error) bool {
	var e *IntegrityError
	return errors.As(err, &e)
}

// IntegrityStatus 是当前完整性链的快照。
type IntegrityStatus struct {
	Dir             string
	Sequence        uint64
	StateHash       string
	Head            string
	ChainLength     int
	TableCount      int
	Backups         int
	BackupRetention int
	WALRecords      int
	Recovered       bool
	LegacyAdopted   bool
	WALEnabled      bool
}

// ---------- 内部数据结构 ----------

// chainRecord 是完整性链的一环：Chain = sha256(Prev || State || Seq)。
type chainRecord struct {
	Seq   uint64 `json:"seq"`
	Prev  string `json:"prev"`
	State string `json:"state"`
	Chain string `json:"chain"`
	Time  int64  `json:"time"`
}

// integrityManifest 是 state.integrity 的内容，自身带 Checksum 防篡改。
type integrityManifest struct {
	Version       int               `json:"version"`
	Sequence      uint64            `json:"sequence"`
	StateHash     string            `json:"state_hash"`
	Tables        map[string]string `json:"tables"` // 表名 -> 该文件 SHA-256
	Chain         []chainRecord     `json:"chain"`  // 全链历史，逐环可校验
	UpdatedAt     int64             `json:"updated_at"`
	AdoptedLegacy bool              `json:"adopted_legacy"`
	Checksum      string            `json:"checksum"`
}

// walRecord 是一条写前日志：含完整快照（base64）与其校验和。
type walRecord struct {
	Seq      uint64 `json:"seq"`
	State    string `json:"state"`    // sha256(快照文档)
	Prev     string `json:"prev"`     // 写入时的链头
	Snapshot string `json:"snapshot"` // base64(快照文档 JSON)
	Checksum string `json:"checksum"` // sha256(Seq|State|Prev|Snapshot)
	Time     int64  `json:"time"`
}

// snapshotDoc 是可重放的确定性快照文档（map 由 encoding/json 排序，字节稳定）。
type snapshotDoc struct {
	Tables map[string]string `json:"tables"` // 表名 -> base64(表文件字节)
}

// ---------- 摘要 / 链 ----------

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func genesisHash() string { return sha256hex([]byte(genesisSeed)) }

func chainHash(prev, state string, seq uint64) string {
	return sha256hex([]byte(prev + "|" + state + "|" + strconv.FormatUint(seq, 10)))
}

func manifestChecksum(m integrityManifest) string {
	c := m
	c.Checksum = ""
	b, _ := json.Marshal(c) // struct 字段顺序固定，map 键由 encoding/json 排序 → 确定性
	return sha256hex(b)
}

func headOf(m *integrityManifest) string {
	if m == nil || len(m.Chain) == 0 {
		return genesisHash()
	}
	return m.Chain[len(m.Chain)-1].Chain
}

func walChecksum(r walRecord) string {
	return sha256hex([]byte(strconv.FormatUint(r.Seq, 10) + "|" + r.State + "|" + r.Prev + "|" + r.Snapshot))
}

func tableDigests(files map[string][]byte) map[string]string {
	out := map[string]string{}
	for name, b := range files {
		out[name] = sha256hex(b)
	}
	return out
}

func makeSnapshotDoc(files map[string][]byte) []byte {
	m := map[string]string{}
	for k, v := range files {
		m[k] = base64.StdEncoding.EncodeToString(v)
	}
	b, _ := json.Marshal(snapshotDoc{Tables: m})
	return b
}

func parseSnapshotDoc(b []byte) (map[string][]byte, error) {
	var d snapshotDoc
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for k, v := range d.Tables {
		raw, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("快照表 %s base64 解码失败: %w", k, err)
		}
		out[k] = raw
	}
	return out, nil
}

// ---------- 路径 / 权限 / 原子写 ----------

func statePath(dir, name string) string { return filepath.Join(dir, name+".json") }
func manifestPath(dir string) string    { return filepath.Join(dir, integrityFileName) }
func walPath(dir string) string         { return filepath.Join(dir, walFileName) }
func backupsPath(dir string) string     { return filepath.Join(dir, backupsDirName) }

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("打开目录以 fsync 失败: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("目录 fsync 失败: %w", err)
	}
	return nil
}

// atomicWriteFile 把 data 原子写入 path：唯一临时文件 0600 → 写入 → fsync → 关闭
// → rename → 目录 fsync。任何一步失败都返回错误，绝不留下半成品状态文件。
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("临时文件 chmod 0600 失败: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("临时文件 fsync 失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename 原子替换失败: %w", err)
	}
	return syncDir(dir)
}

// ensureLayout 创建 0700 目录并强制收紧所有状态文件权限为 0600、目录为 0700。
func ensureLayout(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("数据目录 chmod 0700 失败: %w", err)
	}
	bdir := backupsPath(dir)
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		return fmt.Errorf("创建备份目录失败: %w", err)
	}
	if err := os.Chmod(bdir, 0o700); err != nil {
		return fmt.Errorf("备份目录 chmod 0700 失败: %w", err)
	}
	// 清理上次崩溃遗留的临时文件
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

// HardenPermissions 强制所有状态文件 0600、目录/备份目录 0700（可随时调用）。
func (s *Store) HardenPermissions() error {
	if s.dir == "" {
		return nil
	}
	if err := ensureLayout(s.dir); err != nil {
		return err
	}
	paths := []string{manifestPath(s.dir), walPath(s.dir)}
	for _, name := range tableNames {
		paths = append(paths, statePath(s.dir, name))
	}
	bdir := backupsPath(s.dir)
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := os.Chmod(p, 0o600); err != nil {
			return fmt.Errorf("状态文件 %s chmod 0600 失败: %w", p, err)
		}
	}
	bes, err := os.ReadDir(bdir)
	if err == nil {
		for _, e := range bes {
			if e.IsDir() {
				continue
			}
			if err := os.Chmod(filepath.Join(bdir, e.Name()), 0o600); err != nil {
				return fmt.Errorf("备份文件 chmod 0600 失败: %w", err)
			}
		}
	}
	return nil
}

// ---------- 清单 / 链 / 文件校验 ----------

func readManifest(dir string) (*integrityManifest, bool, error) {
	data, err := os.ReadFile(manifestPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, false, &IntegrityError{File: integrityFileName, Reason: "state.integrity 为空（疑似被截断/篡改）"}
	}
	var m integrityManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false, &IntegrityError{File: integrityFileName, Reason: "state.integrity JSON 解析失败（疑似篡改）", Actual: err.Error()}
	}
	return &m, true, nil
}

func writeManifest(dir string, m *integrityManifest) error {
	m.Checksum = manifestChecksum(*m)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(manifestPath(dir), b)
}

// verifyFiles 校验清单自身、完整性链、逐表 SHA-256 与整体状态哈希（只读）。
func verifyFiles(dir string, m *integrityManifest) error {
	if m.Checksum == "" || m.Checksum != manifestChecksum(*m) {
		return &IntegrityError{File: integrityFileName,
			Reason:   "state.integrity 自身校验和不匹配（疑似篡改）",
			Expected: m.Checksum, Actual: manifestChecksum(*m)}
	}
	if m.Version != integrityFormatVersion {
		return &IntegrityError{File: integrityFileName, Reason: "state.integrity 版本不受支持",
			Expected: strconv.Itoa(integrityFormatVersion), Actual: strconv.Itoa(m.Version)}
	}
	if len(m.Chain) == 0 {
		return &IntegrityError{File: integrityFileName, Reason: "完整性链为空"}
	}
	head := m.Chain[len(m.Chain)-1]
	if head.Seq != m.Sequence || head.State != m.StateHash {
		return &IntegrityError{File: integrityFileName,
			Reason:   "链头与 sequence/state_hash 不一致",
			Expected: fmt.Sprintf("seq=%d state=%s", m.Sequence, m.StateHash),
			Actual:   fmt.Sprintf("seq=%d state=%s", head.Seq, head.State)}
	}
	prev := genesisHash()
	for i, r := range m.Chain {
		if r.Prev != prev {
			return &IntegrityError{File: integrityFileName,
				Reason:   fmt.Sprintf("完整性链第 %d 环 Prev 断链", i),
				Expected: prev, Actual: r.Prev}
		}
		if r.Chain != chainHash(prev, r.State, r.Seq) {
			return &IntegrityError{File: integrityFileName,
				Reason:   fmt.Sprintf("完整性链第 %d 环 Chain 校验失败", i),
				Expected: chainHash(prev, r.State, r.Seq), Actual: r.Chain}
		}
		if i > 0 && r.Seq <= m.Chain[i-1].Seq {
			return &IntegrityError{File: integrityFileName,
				Reason: fmt.Sprintf("完整性链第 %d 环 sequence 非单调递增", i)}
		}
		prev = r.Chain
	}
	files := map[string][]byte{}
	for _, name := range tableNames {
		p := statePath(dir, name)
		b, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				return &IntegrityError{File: name + ".json", Reason: "状态文件缺失（疑似被删除/篡改）",
					Expected: m.Tables[name], Actual: "<missing>"}
			}
			return err
		}
		got := sha256hex(b)
		want := m.Tables[name]
		if want == "" || got != want {
			return &IntegrityError{File: name + ".json",
				Reason:   "状态文件 SHA-256 与 state.integrity 不一致（疑似篡改）",
				Expected: want, Actual: got}
		}
		files[name] = b
	}
	state := sha256hex(makeSnapshotDoc(files))
	if state != m.StateHash {
		return &IntegrityError{File: "snapshot",
			Reason:   "整体状态 SHA-256 与 state.integrity 不一致（疑似篡改）",
			Expected: m.StateHash, Actual: state}
	}
	return nil
}

// newManifest 基于前一清单构造新清单并追加一环完整性链。
func newManifest(prev *integrityManifest, files map[string][]byte, canonical []byte, seq uint64) *integrityManifest {
	state := sha256hex(canonical)
	prevHead := genesisHash()
	chain := []chainRecord{}
	adopted := false
	if prev != nil {
		prevHead = headOf(prev)
		chain = append(chain, prev.Chain...)
		adopted = prev.AdoptedLegacy
	}
	now := time.Now().Unix()
	chain = append(chain, chainRecord{
		Seq: seq, Prev: prevHead, State: state,
		Chain: chainHash(prevHead, state, seq), Time: now,
	})
	m := &integrityManifest{
		Version: integrityFormatVersion, Sequence: seq, StateHash: state,
		Tables: tableDigests(files), Chain: chain, UpdatedAt: now, AdoptedLegacy: adopted,
	}
	m.Checksum = manifestChecksum(*m)
	return m
}

// ---------- WAL ----------

func appendWALRecord(dir string, r walRecord) error {
	r.Time = time.Now().Unix()
	r.Checksum = walChecksum(r)
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	p := walPath(dir)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("打开 WAL 失败: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("追加 WAL 失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("WAL fsync 失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDir(dir)
}

// readWAL 返回全部有效记录；遇到尾部半截/损坏记录即停止（视为上次崩溃的未完成写入）。
func readWAL(dir string) ([]walRecord, bool, error) {
	data, err := os.ReadFile(walPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(data) == 0 {
		return nil, false, nil
	}
	parts := strings.Split(string(data), "\n")
	out := []walRecord{}
	trailing := false
	for i, p := range parts {
		if p == "" {
			if i == len(parts)-1 {
				continue // 末尾换行
			}
			continue
		}
		var r walRecord
		if err := json.Unmarshal([]byte(p), &r); err != nil || r.Checksum == "" || r.Checksum != walChecksum(r) {
			trailing = true
			break
		}
		out = append(out, r)
	}
	return out, trailing, nil
}

func truncateWAL(dir string) error {
	p := walPath(dir)
	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDir(dir)
}

func lastWALRecord(recs []walRecord) *walRecord {
	if len(recs) == 0 {
		return nil
	}
	r := recs[len(recs)-1]
	return &r
}

// ---------- 备份轮转 ----------

func listBackups(dir string) ([]string, error) {
	bdir := backupsPath(dir)
	entries, err := os.ReadDir(bdir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "snapshot-") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // 名称含 20 位补零 seq，字典序即时间序
	return names, nil
}

func rotateBackups(dir string, canonical []byte, seq uint64, retain int) (int, error) {
	if retain < 1 {
		retain = 1
	}
	if retain > maxBackupRetention {
		retain = maxBackupRetention
	}
	bdir := backupsPath(dir)
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		return 0, err
	}
	if err := os.Chmod(bdir, 0o700); err != nil {
		return 0, err
	}
	name := fmt.Sprintf("snapshot-%020d.json", seq)
	if err := atomicWriteFile(filepath.Join(bdir, name), canonical); err != nil {
		return 0, err
	}
	names, err := listBackups(dir)
	if err != nil {
		return 0, err
	}
	for len(names) > retain {
		if err := os.Remove(filepath.Join(bdir, names[0])); err != nil {
			return 0, err
		}
		names = names[1:]
	}
	return len(names), nil
}

// ---------- 提交 / 恢复 / 采纳 ----------

// writeCommittedState 把一份已由 WAL 保护的快照提交为当前状态（表 → 清单 → 备份 → 清空 WAL）。
func writeCommittedState(dir string, opts Options, files map[string][]byte, m *integrityManifest) error {
	for _, name := range tableNames {
		if err := atomicWriteFile(statePath(dir, name), files[name]); err != nil {
			return fmt.Errorf("提交表 %s 失败: %w", name, err)
		}
	}
	canonical := makeSnapshotDoc(files)
	if _, err := rotateBackups(dir, canonical, m.Sequence, normalizeRetention(opts)); err != nil {
		return fmt.Errorf("备份轮转失败: %w", err)
	}
	if err := writeManifest(dir, m); err != nil {
		return fmt.Errorf("提交 state.integrity 失败: %w", err)
	}
	if err := truncateWAL(dir); err != nil {
		return fmt.Errorf("WAL 检查点失败: %w", err)
	}
	return nil
}

func normalizeRetention(opts Options) int {
	if opts.BackupRetention <= 0 {
		return defaultBackupRetention
	}
	return opts.BackupRetention
}

func anyTableExists(dir string) bool {
	for _, name := range tableNames {
		if _, err := os.Stat(statePath(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// adoptLegacy 为「有遗留表文件但无 state.integrity」建立 TOFU 完整性基线：
// 逐表校验 JSON 合法后规范化写出全部 10 张表，并生成第 1 环完整性链。
// 这是显式的信任首次使用（LegacyAdopted=true），此后任何改动都会被严格检测。
func adoptLegacy(dir string, opts Options) (*integrityManifest, error) {
	files := map[string][]byte{}
	for _, name := range tableNames {
		p := statePath(dir, name)
		b, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				files[name] = []byte("[]")
				continue
			}
			return nil, err
		}
		// 遗留文件必须是合法 JSON 表（数组/对象），损坏即拒绝采纳
		var probe interface{}
		if err := json.Unmarshal(b, &probe); err != nil {
			return nil, &IntegrityError{File: name + ".json",
				Reason: "遗留状态文件 JSON 损坏，拒绝建立完整性基线", Actual: err.Error()}
		}
		files[name] = b
	}
	canonical := makeSnapshotDoc(files)
	m := newManifest(nil, files, canonical, 1)
	m.AdoptedLegacy = true
	m.Checksum = manifestChecksum(*m)
	if err := writeCommittedState(dir, opts, files, m); err != nil {
		return nil, err
	}
	return m, nil
}

// recoverFromWAL 崩溃恢复：用 WAL 中最后一条有效记录重放完整快照，重建表文件、清单与链环。
func recoverFromWAL(dir string, opts Options, rec walRecord, prev *integrityManifest) (*integrityManifest, error) {
	snapBytes, err := base64.StdEncoding.DecodeString(rec.Snapshot)
	if err != nil {
		return nil, &IntegrityError{File: walFileName, Reason: "WAL 快照 base64 解码失败", Actual: err.Error()}
	}
	files, err := parseSnapshotDoc(snapBytes)
	if err != nil {
		return nil, &IntegrityError{File: walFileName, Reason: "WAL 快照解析失败", Actual: err.Error()}
	}
	for _, name := range tableNames {
		if _, ok := files[name]; !ok {
			files[name] = []byte("[]")
		}
	}
	canonical := makeSnapshotDoc(files)
	if sha256hex(canonical) != rec.State {
		return nil, &IntegrityError{File: walFileName,
			Reason:   "WAL 快照 SHA-256 与记录不一致（WAL 被篡改）",
			Expected: rec.State, Actual: sha256hex(canonical)}
	}
	expectedPrev := genesisHash()
	if prev != nil {
		expectedPrev = headOf(prev)
	}
	if rec.Prev != expectedPrev {
		return nil, &IntegrityError{File: walFileName,
			Reason:   "WAL 记录的 Prev 与当前链头不一致（WAL 不属于该状态谱系）",
			Expected: expectedPrev, Actual: rec.Prev}
	}
	m := newManifest(prev, files, canonical, rec.Seq)
	if err := writeCommittedState(dir, opts, files, m); err != nil {
		return nil, err
	}
	return m, nil
}

// openState 在启动时完成：权限加固 → 清单校验/链式校验 → WAL 崩溃恢复 →
// 遗留数据 TOFU 采纳 → 逐表 SHA-256 校验。任何篡改返回明确错误。
func openState(dir string, opts Options) (*integrityManifest, bool, bool, error) {
	if dir == "" {
		return &integrityManifest{Version: integrityFormatVersion, Tables: map[string]string{}, Chain: []chainRecord{}}, false, false, nil
	}
	if err := ensureLayout(dir); err != nil {
		return nil, false, false, err
	}
	m, mOK, err := readManifest(dir)
	if err != nil {
		return nil, false, false, err
	}
	if mOK && (m.Checksum == "" || m.Checksum != manifestChecksum(*m)) {
		return nil, false, false, &IntegrityError{File: integrityFileName,
			Reason:   "state.integrity 自身校验和不匹配（疑似篡改）",
			Expected: m.Checksum, Actual: manifestChecksum(*m)}
	}
	walRecs, _, err := readWAL(dir)
	if err != nil {
		return nil, false, false, err
	}
	last := lastWALRecord(walRecs)
	recovered, adopted := false, false

	if !mOK {
		if last != nil {
			m, err = recoverFromWAL(dir, opts, *last, nil)
			if err != nil {
				return nil, false, false, err
			}
			recovered = true
		} else if anyTableExists(dir) {
			if opts.Strict {
				return nil, false, false, ErrIntegrityMissing
			}
			m, err = adoptLegacy(dir, opts)
			if err != nil {
				return nil, false, false, err
			}
			adopted = true
		} else {
			m = &integrityManifest{Version: integrityFormatVersion, Tables: map[string]string{}, Chain: []chainRecord{}}
		}
	} else {
		if last != nil && last.Seq > m.Sequence {
			m, err = recoverFromWAL(dir, opts, *last, m)
			if err != nil {
				return nil, false, false, err
			}
			recovered = true
		} else if err := truncateWAL(dir); err != nil {
			return nil, false, false, err
		}
		if err := verifyFiles(dir, m); err != nil {
			return nil, false, false, err
		}
	}
	return m, recovered, adopted, nil
}

// ---------- 新增导出函数（签名全部为新增，不改动既有导出 API） ----------

// NewWithOptions 以加固选项打开存储。等价于 New(dir) 但允许配置备份份数/严格模式/WAL。
func NewWithOptions(dir string, opts Options) (*Store, error) {
	s := &Store{dir: dir, db: newDatabase(), opts: opts}
	m, recovered, adopted, err := openState(dir, opts)
	if err != nil {
		return nil, err
	}
	if dir != "" {
		if err := s.load(); err != nil {
			return nil, err
		}
		if err := s.HardenPermissions(); err != nil {
			return nil, err
		}
	}
	s.manifest = *m
	s.rebuild()
	s.ensureDefaults()
	s.status = s.computeStatus(recovered, adopted)
	return s, nil
}

// Verify 只读校验当前磁盘状态：清单自身、完整性链、逐表 SHA-256、整体状态哈希、未提交 WAL。
func (s *Store) Verify() error {
	if s.dir == "" {
		return nil
	}
	m, ok, err := readManifest(s.dir)
	if err != nil {
		return err
	}
	if !ok {
		if anyTableExists(s.dir) {
			return ErrIntegrityMissing
		}
		return nil
	}
	if m.Checksum == "" || m.Checksum != manifestChecksum(*m) {
		return &IntegrityError{File: integrityFileName, Reason: "state.integrity 自身校验和不匹配（疑似篡改）",
			Expected: m.Checksum, Actual: manifestChecksum(*m)}
	}
	recs, _, err := readWAL(s.dir)
	if err != nil {
		return err
	}
	if last := lastWALRecord(recs); last != nil && last.Seq > m.Sequence {
		return ErrWALUncommitted
	}
	return verifyFiles(s.dir, m)
}

// Recover 显式重放 WAL 并从恢复后的文件重新加载内存数据库（运维/测试用）。
func (s *Store) Recover() error {
	if s.dir == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	m, recovered, adopted, err := openState(s.dir, s.opts)
	if err != nil {
		return err
	}
	s.manifest = *m
	s.mu.Lock()
	s.db = newDatabase()
	if err := s.load(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.rebuild()
	s.mu.Unlock()
	s.status = s.computeStatus(recovered, adopted)
	return nil
}

// Backup 立刻为当前已提交状态生成一份备份并轮转，返回备份文件路径。
func (s *Store) Backup() (string, error) {
	if s.dir == "" {
		return "", errors.New("纯内存存储无备份")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	files := map[string][]byte{}
	for _, name := range tableNames {
		b, err := os.ReadFile(statePath(s.dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				b = []byte("[]")
			} else {
				return "", err
			}
		}
		files[name] = b
	}
	canonical := makeSnapshotDoc(files)
	seq := s.manifest.Sequence
	if _, err := rotateBackups(s.dir, canonical, seq, normalizeRetention(s.opts)); err != nil {
		return "", err
	}
	return filepath.Join(backupsPath(s.dir), fmt.Sprintf("snapshot-%020d.json", seq)), nil
}

// SetBackupRetention 设置备份轮转保留份数（1..100）。
func (s *Store) SetBackupRetention(n int) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if n < 1 {
		n = 1
	}
	if n > maxBackupRetention {
		n = maxBackupRetention
	}
	s.opts.BackupRetention = n
	s.status.BackupRetention = n
}

// IntegrityStatus 返回当前完整性链快照。
func (s *Store) IntegrityStatus() IntegrityStatus {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	st := s.computeStatus(s.status.Recovered, s.status.LegacyAdopted)
	s.status = st
	return st
}

// VerifyDir 独立校验一个数据目录（不构造 Store）。
func VerifyDir(dir string) error {
	if dir == "" {
		return nil
	}
	m, ok, err := readManifest(dir)
	if err != nil {
		return err
	}
	if !ok {
		if anyTableExists(dir) {
			return ErrIntegrityMissing
		}
		return nil
	}
	if m.Checksum == "" || m.Checksum != manifestChecksum(*m) {
		return &IntegrityError{File: integrityFileName, Reason: "state.integrity 自身校验和不匹配（疑似篡改）",
			Expected: m.Checksum, Actual: manifestChecksum(*m)}
	}
	recs, _, err := readWAL(dir)
	if err != nil {
		return err
	}
	if last := lastWALRecord(recs); last != nil && last.Seq > m.Sequence {
		return ErrWALUncommitted
	}
	return verifyFiles(dir, m)
}

func (s *Store) computeStatus(recovered, adopted bool) IntegrityStatus {
	st := IntegrityStatus{
		Dir: s.dir, Sequence: s.manifest.Sequence, StateHash: s.manifest.StateHash,
		Head: headOf(&s.manifest), ChainLength: len(s.manifest.Chain),
		TableCount: len(tableNames), BackupRetention: normalizeRetention(s.opts),
		Recovered: recovered, LegacyAdopted: adopted || s.manifest.AdoptedLegacy,
		WALEnabled: !s.opts.DisableWAL,
	}
	if s.dir != "" {
		if names, err := listBackups(s.dir); err == nil {
			st.Backups = len(names)
		}
		if recs, _, err := readWAL(s.dir); err == nil {
			st.WALRecords = len(recs)
		}
	}
	return st
}

// ---------- 加固后的 Save ----------

// saveHardened 是 Save() 的实现：writeMu 串行化 → 一致性快照 → WAL 前置追加 →
// 原子提交全部表 → 备份轮转 → 推进完整性链 → 写 state.integrity → WAL 检查点。
func (s *Store) saveHardened() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// 在 RLock 下构建一致性快照；writeMu 保证不会有第二个 Save 交错写文件。
	s.mu.RLock()
	files := map[string][]byte{}
	for _, name := range tableNames {
		b, err := json.MarshalIndent(s.tablePtr(name), "", "  ")
		if err != nil {
			s.mu.RUnlock()
			return fmt.Errorf("序列化表 %s 失败: %w", name, err)
		}
		files[name] = b
	}
	s.mu.RUnlock()

	canonical := makeSnapshotDoc(files)
	state := sha256hex(canonical)
	seq := s.manifest.Sequence + 1
	prevHead := headOf(&s.manifest)

	// 1) 写前日志（含完整快照与校验和，fsync 后才允许动状态文件）
	if !s.opts.DisableWAL {
		if err := appendWALRecord(s.dir, walRecord{
			Seq: seq, State: state, Prev: prevHead,
			Snapshot: base64.StdEncoding.EncodeToString(canonical),
		}); err != nil {
			return err
		}
	}

	// 2) 原子提交表文件（每份 0600 + fsync + rename + 目录 fsync）
	for _, name := range tableNames {
		if err := atomicWriteFile(statePath(s.dir, name), files[name]); err != nil {
			return fmt.Errorf("原子写表 %s 失败: %w", name, err)
		}
	}

	// 3) 备份轮转（保留最近 N 份）
	if _, err := rotateBackups(s.dir, canonical, seq, normalizeRetention(s.opts)); err != nil {
		return fmt.Errorf("备份轮转失败: %w", err)
	}

	// 4) 推进完整性链并写 state.integrity
	m := newManifest(&s.manifest, files, canonical, seq)
	if err := writeManifest(s.dir, m); err != nil {
		return fmt.Errorf("写 state.integrity 失败: %w", err)
	}

	// 5) WAL 检查点：提交完成后清空 WAL
	if err := truncateWAL(s.dir); err != nil {
		return fmt.Errorf("WAL 检查点失败: %w", err)
	}

	s.manifest = *m
	s.status = s.computeStatus(s.status.Recovered, s.status.LegacyAdopted)
	return nil
}

// 编译期断言：新增错误类型满足 error 接口。
var _ error = (*IntegrityError)(nil)
