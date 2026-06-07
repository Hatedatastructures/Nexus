package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ───────────────────────────── Allowlist 完整测试 ─────────────────────────────

func TestAllowlist(t *testing.T) {
	t.Run("acceptAll 模式始终允许", func(t *testing.T) {
		a := NewAllowlist("", true)
		if !a.IsAllowed("anything") {
			t.Error("acceptAll 模式应始终返回 true")
		}
	})

	t.Run("默认拒绝未知命令", func(t *testing.T) {
		a := NewAllowlist("", false)
		if a.IsAllowed("unknown") {
			t.Error("未知命令应被拒绝")
		}
	})

	t.Run("Add 后允许", func(t *testing.T) {
		a := NewAllowlist("", false)
		a.Add("echo hello")
		if !a.IsAllowed("echo hello") {
			t.Error("Add 后应允许")
		}
	})

	t.Run("Remove 后拒绝", func(t *testing.T) {
		a := NewAllowlist("", false)
		a.Add("echo hello")
		a.Remove("echo hello")
		if a.IsAllowed("echo hello") {
			t.Error("Remove 后应拒绝")
		}
	})

	t.Run("Entries 返回快照", func(t *testing.T) {
		a := NewAllowlist("", false)
		a.Add("cmd1")
		a.Add("cmd2")
		entries := a.Entries()
		if len(entries) != 2 {
			t.Errorf("Entries() len = %d, want 2", len(entries))
		}
	})
}

func TestAllowlist_Persistence(t *testing.T) {
	t.Run("Add 持久化到磁盘", func(t *testing.T) {
		dir := t.TempDir()
		a := NewAllowlist(dir, false)
		a.Add("persistent_cmd")

		data, err := os.ReadFile(filepath.Join(dir, allowlistFilename))
		if err != nil {
			t.Fatalf("allowlist 文件应存在: %v", err)
		}
		var entries map[string]bool
		if err := json.Unmarshal(data, &entries); err != nil {
			t.Fatalf("JSON 解析失败: %v", err)
		}
		if !entries["persistent_cmd"] {
			t.Error("persistent_cmd 应在持久化数据中")
		}
	})

	t.Run("从磁盘加载", func(t *testing.T) {
		dir := t.TempDir()
		entries := map[string]bool{"loaded_cmd": true}
		data, _ := json.Marshal(entries)
		if err := os.WriteFile(filepath.Join(dir, allowlistFilename), data, 0600); err != nil {
			t.Fatal(err)
		}

		a := NewAllowlist(dir, false)
		if !a.IsAllowed("loaded_cmd") {
			t.Error("应从磁盘加载 loaded_cmd")
		}
	})

	t.Run("Remove 持久化删除", func(t *testing.T) {
		dir := t.TempDir()
		a := NewAllowlist(dir, false)
		a.Add("to_remove")
		a.Remove("to_remove")

		a2 := NewAllowlist(dir, false)
		if a2.IsAllowed("to_remove") {
			t.Error("Remove 后重新加载应拒绝")
		}
	})

	t.Run("空目录不持久化", func(t *testing.T) {
		a := NewAllowlist("", false)
		a.Add("cmd") // 不应 panic
	})

	t.Run("无效 JSON 文件跳过", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, allowlistFilename), []byte("invalid json"), 0600); err != nil {
			t.Fatal(err)
		}
		a := NewAllowlist(dir, false)
		if a.IsAllowed("anything") {
			t.Error("无效 JSON 文件后应拒绝未知命令")
		}
	})

	t.Run("Entries 空列表", func(t *testing.T) {
		a := NewAllowlist("", false)
		entries := a.Entries()
		if len(entries) != 0 {
			t.Errorf("空 Entries len = %d, want 0", len(entries))
		}
	})
}

// TestAllowlist_SaveToReadOnlyDir 验证 save 失败时不会 panic。
func TestAllowlist_SaveToReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	// 创建一个只读目录
	readOnlyDir := filepath.Join(dir, "readonly")
	if err := os.Mkdir(readOnlyDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0755) })

	a := NewAllowlist(readOnlyDir, false)
	// Add 内部调用 save，但目录只读 → save 失败但不应 panic
	a.Add("some_command")
}

// TestAllowlist_LoadCorruptFile 验证加载损坏文件时不会 panic。
func TestAllowlist_LoadCorruptFile(t *testing.T) {
	dir := t.TempDir()
	// 写入损坏的 JSON
	path := filepath.Join(dir, allowlistFilename)
	if err := os.WriteFile(path, []byte("{corrupt json!!!"), 0600); err != nil {
		t.Fatal(err)
	}

	a := NewAllowlist(dir, false)
	// 加载损坏文件应跳过，entries 为空
	if len(a.Entries()) != 0 {
		t.Errorf("损坏文件应导致空 entries, got %v", a.Entries())
	}
}

// TestAllowlist_Remove 验证 Remove 从列表中移除条目。
func TestAllowlist_Remove(t *testing.T) {
	dir := t.TempDir()
	a := NewAllowlist(dir, false)
	a.Add("cmd1")
	a.Add("cmd2")

	if !a.IsAllowed("cmd1") {
		t.Error("cmd1 should be allowed")
	}

	a.Remove("cmd1")
	if a.IsAllowed("cmd1") {
		t.Error("cmd1 should be removed")
	}
	if !a.IsAllowed("cmd2") {
		t.Error("cmd2 should still be allowed")
	}
}

// ───────────────────────────── AllowlistRef 测试 ─────────────────────────────

func TestAllowlistRef(t *testing.T) {
	mgr := NewHookManager("", true)
	ref := mgr.AllowlistRef()
	if ref == nil {
		t.Fatal("AllowlistRef 不应返回 nil")
	}
}

// ───────────────────────────── promptAllow 测试 ─────────────────────────────

func TestPromptAllow_NonTerminal(t *testing.T) {
	mgr := NewHookManager("", false)
	result := mgr.promptAllow("test_cmd")
	if result {
		t.Error("非终端 stdin 应默认拒绝")
	}
}
