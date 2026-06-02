package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// ───────────────────────────── ShellHookManager 测试 ─────────────────────────────

func TestNewShellHookManager(t *testing.T) {
	mgr := NewShellHookManager("", true)
	if mgr == nil {
		t.Fatal("NewShellHookManager 不应返回 nil")
	}
	if mgr.inner == nil {
		t.Fatal("内部 HookManager 不应为 nil")
	}
}

func TestShellHookManager_RegisterHooks(t *testing.T) {
	mgr := NewShellHookManager("", true)

	// 注册有效的 hook 规格
	err := mgr.RegisterHooks([]ShellHookSpec{
		{Event: "pre_tool_call", Command: "echo allow"},
	})
	if err != nil {
		t.Fatalf("RegisterHooks 不应返回错误: %v", err)
	}

	// 注册空 command 的规格应跳过 (不报错)
	err = mgr.RegisterHooks([]ShellHookSpec{
		{Event: "pre_tool_call", Command: ""},
	})
	if err != nil {
		t.Fatalf("空 command 应被跳过: %v", err)
	}

	// 注册无效事件类型应报错
	err = mgr.RegisterHooks([]ShellHookSpec{
		{Event: "invalid_event", Command: "echo test"},
	})
	if err == nil {
		t.Fatal("无效事件类型应返回错误")
	}
}

func TestShellHookManager_LoadFromDir(t *testing.T) {
	// 空目录应成功
	mgr := NewShellHookManager("", true)
	err := mgr.LoadFromDir("")
	if err != nil {
		t.Fatalf("空目录不应返回错误: %v", err)
	}

	// 不存在的目录应成功 (静默跳过)
	err = mgr.LoadFromDir("/nonexistent/path")
	if err != nil {
		t.Fatalf("不存在的目录不应返回错误: %v", err)
	}

	// 创建临时目录并放入 YAML hook 文件
	tmpDir := t.TempDir()
	yamlContent := "event: pre_tool_call\ncommand: echo hello\n"
	if writeErr := os.WriteFile(filepath.Join(tmpDir, "hook.yaml"), []byte(yamlContent), 0644); writeErr != nil {
		t.Fatalf("写入测试 YAML 失败: %v", writeErr)
	}

	mgr2 := NewShellHookManager("", true)
	err = mgr2.LoadFromDir(tmpDir)
	if err != nil {
		t.Fatalf("LoadFromDir 不应返回错误: %v", err)
	}

	// 无效 YAML 应被跳过 (不报错)
	if writeErr := os.WriteFile(filepath.Join(tmpDir, "bad.yaml"), []byte("::invalid yaml::"), 0644); writeErr != nil {
		t.Fatalf("写入无效 YAML 失败: %v", writeErr)
	}
}

func TestShellHookManager_ExecuteHook(t *testing.T) {
	mgr := NewShellHookManager("", true)

	// 无 hook 注册时不应阻止
	resp, blocked, err := mgr.ExecuteHook(context.Background(), "file_read", nil, "session-1")
	if err != nil {
		t.Fatalf("ExecuteHook 不应返回错误: %v", err)
	}
	if blocked {
		t.Error("无 hook 注册时不应阻止")
	}
	if resp != nil {
		t.Error("无 hook 注册时 resp 应为 nil")
	}
}

func TestShellHookManager_ExecutePostHook(t *testing.T) {
	mgr := NewShellHookManager("", true)

	// 无 hook 注册时不应报错
	err := mgr.ExecutePostHook(context.Background(), "file_read", nil, "output")
	if err != nil {
		t.Fatalf("ExecutePostHook 不应返回错误: %v", err)
	}
}
