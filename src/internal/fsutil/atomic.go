// Package fsutil 提供天衡内部共用的原子落盘与权限加固原语。
//
// 与 src/internal/store/hardening.go 同源同一套保证，但保持零依赖、可被任意
// 内部包复用（affinity / grouping / redact / routingjournal 等）。
//
// 保证：
//  1. 原子写：CreateTemp(0600) → Write → fsync → Close → rename → 目录 fsync。
//     任何时刻的崩溃都不会留下半截文件，读到的永远是「上一版完整内容」或「新版完整内容」。
//  2. 权限：文件 0600、目录 0700。
//  3. 完整性：写出的内容返回 SHA-256，供调用方做篡改检测。
package fsutil

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile 原子地把 data 写入 path（0600），返回内容 SHA-256 十六进制。
// 目录不存在时按 0700 创建。
func AtomicWriteFile(path string, data []byte) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("fsutil: mkdir %s: %w", dir, err)
	}
	// MkdirAll 不会收紧「已存在」目录的权限（例如安装包预建或从别处复制来的
	// 0755 目录），因此这里显式强制 0700 —— 天衡要求状态目录绝不对他人可读。
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("fsutil: chmod dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("fsutil: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// rename 成功后该文件已不存在；失败路径上清理残留。
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("fsutil: chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("fsutil: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("fsutil: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("fsutil: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("fsutil: rename: %w", err)
	}
	// 目录 fsync：确保改名本身落盘（否则崩溃后可能读到旧名字）。
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("fsutil: chmod final: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// AppendLine 以 0600 权限向 path 追加一行（自动补 '\n'）。
// 追加语义用于按日分片的日志（routing / masking），不保证原子替换，
// 但保证单次写是 O_APPEND 的单次系统调用，不会与其他写者交错。
func AppendLine(path string, line []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("fsutil: mkdir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("fsutil: chmod dir %s: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("fsutil: open append: %w", err)
	}
	defer func() { _ = f.Close() }()
	if len(line) == 0 || line[len(line)-1] != '\n' {
		line = append(line, '\n')
	}
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("fsutil: append: %w", err)
	}
	return nil
}

// ReadFileIfExists 读取文件；不存在时返回 (nil, false, nil) 而非错误，
// 用于「首次运行尚无状态文件」的正常路径。
func ReadFileIfExists(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return b, true, nil
}
