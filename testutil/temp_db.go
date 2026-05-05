// Package testutil 提供测试用的模拟对象和辅助工具。
// 本文件提供临时 SQLite 数据库的创建辅助函数。
package testutil

import (
	"path/filepath"
	"testing"

	"nexus-agent/internal/state"
)

// ───────────────────────────── 临时数据库辅助函数 ─────────────────────────────

// NewTempStore 创建一个临时 SQLite 数据库用于测试。
// 使用 t.TempDir() 自动管理临时目录，测试结束时自动清理。
// 返回 state.Store 实例和清理函数。
func NewTempStore(t *testing.T) *state.Store {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := state.NewStore(dbPath)
	if err != nil {
		t.Fatalf("创建临时数据库失败: %v", err)
	}

	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("关闭临时数据库失败: %v", err)
		}
	})

	return store
}

// NewTempStoreWithPath 创建临时数据库并返回存储实例和数据库路径。
// 当测试需要直接访问数据库文件时使用。
func NewTempStoreWithPath(t *testing.T) (*state.Store, string) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := state.NewStore(dbPath)
	if err != nil {
		t.Fatalf("创建临时数据库失败: %v", err)
	}

	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("关闭临时数据库失败: %v", err)
		}
	})

	return store, dbPath
}
