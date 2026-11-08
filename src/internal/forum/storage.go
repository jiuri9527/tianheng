package forum

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"llm-hub/src/internal/config"
)

// errStorageUnavailable 在启动校验失败 / 落盘失败时返回，绝不退化成假数据。
var errStorageUnavailable = errors.New("forum storage unavailable")

// fileStore 持有 forum.json 的完整内存镜像。
// mu 覆盖「所有写」：任何变更都在 write() 的写锁内完成并同步落盘后才释放，
// 读操作走 RLock，杜绝并发下的丢更新与半写文件。
type fileStore struct {
	mu      sync.RWMutex
	path    string
	db      database
	healthy bool
}

// openFileStore 打开（或初始化）独立 JSON 文件。
//
// DataDir 为空时回退到 "./.llmhub-data"，与 cmd/server/main.go 对 store.New 的
// 回退保持一致，保证 forum.json 与其它运行时 JSON 位于同一数据目录。
//
// 「启动校验可读」：文件存在则必须能被打开、完整读取且 JSON 可解析；任何一步
// 失败都返回 error（由 Register 降级为 503），绝不静默吞掉后使用空数据冒充成功。
func openFileStore(cfg *config.Config) (*fileStore, error) {
	dir := cfg.DataDir
	if dir == "" {
		dir = "./.llmhub-data"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("forum: 创建数据目录失败: %w", err)
	}
	path := filepath.Join(dir, "forum.json")
	fs := &fileStore{path: path, db: newDatabase(), healthy: true}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 真实空态：文件不存在时不预创建、不写任何种子数据；
			// 首次真实提交（write）才会原子落盘。
			return fs, nil
		}
		return nil, fmt.Errorf("forum: forum.json 不可读: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("forum: 读取 forum.json 失败: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		// 空文件可读但无内容：按真实空态处理。
		return fs, nil
	}
	var d database
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("forum: forum.json JSON 解析失败: %w", err)
	}
	fs.db = d
	fs.normalize()
	return fs, nil
}

// normalize 保证切片非 nil（JSON 输出 [] 而非 null），并以真实 Like / Reply
// 记录重算计数；防止手工编辑文件造成「点赞数/回复数」与真实记录不一致的假数据。
func (fs *fileStore) normalize() {
	if fs.db.Boards == nil {
		fs.db.Boards = []*Board{}
	}
	if fs.db.Posts == nil {
		fs.db.Posts = []*Post{}
	}
	if fs.db.Replies == nil {
		fs.db.Replies = []*Reply{}
	}
	if fs.db.Likes == nil {
		fs.db.Likes = []*Like{}
	}
	byID := make(map[string]*Post, len(fs.db.Posts))
	for _, p := range fs.db.Posts {
		if p == nil {
			continue
		}
		p.LikeCount = 0
		p.ReplyCount = 0
		byID[p.ID] = p
	}
	for _, r := range fs.db.Replies {
		if r == nil {
			continue
		}
		if p := byID[r.PostID]; p != nil {
			p.ReplyCount++
		}
	}
	for _, l := range fs.db.Likes {
		if l == nil {
			continue
		}
		if p := byID[l.PostID]; p != nil {
			p.LikeCount++
		}
	}
}

// cloneDatabase 深拷贝全部实体（含指针元素），用于落盘失败时回滚内存状态。
func cloneDatabase(src *database) database {
	out := database{
		Boards:  make([]*Board, 0, len(src.Boards)),
		Posts:   make([]*Post, 0, len(src.Posts)),
		Replies: make([]*Reply, 0, len(src.Replies)),
		Likes:   make([]*Like, 0, len(src.Likes)),
	}
	for _, b := range src.Boards {
		if b != nil {
			cp := *b
			out.Boards = append(out.Boards, &cp)
		}
	}
	for _, p := range src.Posts {
		if p != nil {
			cp := *p
			out.Posts = append(out.Posts, &cp)
		}
	}
	for _, r := range src.Replies {
		if r != nil {
			cp := *r
			out.Replies = append(out.Replies, &cp)
		}
	}
	for _, l := range src.Likes {
		if l != nil {
			cp := *l
			out.Likes = append(out.Likes, &cp)
		}
	}
	return out
}

// write 在写锁内执行变更并原子落盘。所有 POST/PUT/DELETE 都通过此函数，
// 满足「mutex 覆盖所有写」。fn 出错或落盘失败时回滚到调用前快照，
// 保证内存与 forum.json 永不出现半写/不一致。
func (fs *fileStore) write(fn func(*database) error) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if !fs.healthy {
		return errStorageUnavailable
	}
	snapshot := cloneDatabase(&fs.db)
	if err := fn(&fs.db); err != nil {
		fs.db = snapshot
		return err
	}
	if err := fs.save(); err != nil {
		fs.db = snapshot
		return err
	}
	return nil
}

// read 在读锁内构造响应数据；调用方必须在 fn 内完成深拷贝，不得把内部
// 指针带出锁外。
func (fs *fileStore) read(fn func(*database)) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	fn(&fs.db)
}

// save 原子写：tmp(0600) -> fsync -> rename -> 目录 fsync。
// 必须在 fs.mu 写锁内调用。
func (fs *fileStore) save() error {
	data, err := json.MarshalIndent(fs.db, "", "  ")
	if err != nil {
		return fmt.Errorf("forum: 序列化失败: %w", err)
	}
	dir := filepath.Dir(fs.path)
	tmp := filepath.Join(dir, ".forum.json.tmp")

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("forum: 创建临时文件失败: %w", err)
	}
	// 已存在的 tmp 可能权限被改，显式收紧到 0600。
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("forum: 设置 0600 失败: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("forum: 写临时文件失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("forum: fsync 失败: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("forum: 关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, fs.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("forum: rename 失败: %w", err)
	}
	// 同步目录项，确保 rename 在崩溃后仍可见。
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
