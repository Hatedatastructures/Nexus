package hooks

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// ───────────────────────────── 持久化 Allowlist ─────────────────────────────

const allowlistFilename = "shell-hooks-allowlist.json"

// Allowlist 管理 hook 命令的允许列表。
// 线程安全，支持持久化到磁盘。
type Allowlist struct {
	mu       sync.RWMutex
	entries  map[string]bool
	dir      string // 持久化目录 (空 = 不持久化)
	acceptAll bool  // 自动接受所有 hook
}

// NewAllowlist 创建 Allowlist。
//   - dir: 持久化目录，空字符串表示不持久化
//   - acceptAll: true 时 IsAllowed 始终返回 true
func NewAllowlist(dir string, acceptAll bool) *Allowlist {
	a := &Allowlist{
		entries:   make(map[string]bool),
		dir:       dir,
		acceptAll: acceptAll,
	}
	a.load()
	return a
}

// IsAllowed 检查指定命令是否在允许列表中。
// acceptAll 模式下始终返回 true。
func (a *Allowlist) IsAllowed(command string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.acceptAll || a.entries[command]
}

// Add 将命令添加到允许列表并持久化。
func (a *Allowlist) Add(command string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries[command] = true
	a.save()
}

// Remove 从允许列表移除命令并持久化。
func (a *Allowlist) Remove(command string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.entries, command)
	a.save()
}

// Entries 返回当前允许列表的快照。
func (a *Allowlist) Entries() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	keys := make([]string, 0, len(a.entries))
	for k := range a.entries {
		keys = append(keys, k)
	}
	return keys
}

// ───────────────────────────── 持久化 ─────────────────────────────

// save 将允许列表写入磁盘。调用者需持有写锁。
func (a *Allowlist) save() {
	if a.dir == "" {
		return
	}

	path := filepath.Join(a.dir, allowlistFilename)
	data, err := json.MarshalIndent(a.entries, "", "  ")
	if err != nil {
		slog.Error("failed to serialize allowlist", "err", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		slog.Error("failed to save allowlist", "path", path, "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Error("failed to rename allowlist", "path", path, "err", err)
	}
}

// load 从磁盘加载允许列表。
func (a *Allowlist) load() {
	if a.dir == "" {
		return
	}

	path := filepath.Join(a.dir, allowlistFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("failed to load allowlist", "path", path, "err", err)
		}
		return
	}

	var entries map[string]bool
	if err := json.Unmarshal(data, &entries); err != nil {
		slog.Warn("failed to parse allowlist", "path", path, "err", err)
		return
	}
	a.entries = entries
}
