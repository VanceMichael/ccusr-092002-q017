// Package store 提供术语与用途协议服务的 SQLite 持久层。
// 中心只保存不可逆摘要与协议元数据：不接收原始病历，也不执行模型训练。
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"

	"github.com/vancemichael/092002-medical-collaboration-control/migrations"
)

// ErrNotFound 表示目标记录不存在。
var ErrNotFound = errors.New("记录不存在")

// ErrConflict 表示当前状态与请求冲突（如重复确认、已撤回）。
var ErrConflict = errors.New("状态冲突")

// Store 封装数据库访问。
type Store struct {
	db *sql.DB
}

// Open 打开（必要时创建）数据库文件并应用全部迁移。
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据目录失败: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// SQLite 单写者：串行化连接，避免 SQLITE_BUSY。
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

// migrate 按文件名顺序应用全部迁移；迁移脚本均为幂等。
func migrate(db *sql.DB) error {
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		script, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return err
		}
		if _, err := db.Exec(string(script)); err != nil {
			return fmt.Errorf("应用迁移 %s 失败: %w", name, err)
		}
	}
	return nil
}

// newID 生成带前缀的随机标识，如 TERM-1a2b3c4d5e6f。
func newID(prefix string) string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}

// now 返回带时区偏移的 ISO 8601 时间（UTC）。
func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
