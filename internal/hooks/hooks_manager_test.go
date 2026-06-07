package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// ───────────────────────────── RegisterFromSpecs 测试 ─────────────────────────────

func TestRegisterFromSpecs(t *testing.T) {
	t.Run("批量注册有效 spec", func(t *testing.T) {
		mgr := NewHookManager("", true)
		err := mgr.RegisterFromSpecs([]HookSpec{
			{Event: EventPreToolCall, Command: "echo pre"},
			{Event: EventPostToolCall, Command: "echo post"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("空 command 跳过", func(t *testing.T) {
		mgr := NewHookManager("", true)
		err := mgr.RegisterFromSpecs([]HookSpec{
			{Event: EventPreToolCall, Command: ""},
		})
		if err != nil {
			t.Fatalf("空 command 应跳过: %v", err)
		}
	})

	t.Run("无效事件类型报错", func(t *testing.T) {
		mgr := NewHookManager("", true)
		err := mgr.RegisterFromSpecs([]HookSpec{
			{Event: "invalid", Command: "echo"},
		})
		if err == nil {
			t.Fatal("无效事件类型应返回错误")
		}
	})

	t.Run("空列表不报错", func(t *testing.T) {
		mgr := NewHookManager("", true)
		err := mgr.RegisterFromSpecs(nil)
		if err != nil {
			t.Fatalf("nil 列表不应报错: %v", err)
		}
	})
}

// TestRegisterFromSpecs_EmptyCommandSkipped 验证空 command 被跳过。
func TestRegisterFromSpecs_EmptyCommandSkipped(t *testing.T) {
	mgr := NewHookManager(t.TempDir(), false)
	err := mgr.RegisterFromSpecs([]HookSpec{
		{Event: EventPreToolCall, Command: ""},
	})
	if err != nil {
		t.Fatalf("空 command 应被跳过: %v", err)
	}
}

// ───────────────────────────── LoadFromDir 测试 ─────────────────────────────

func TestLoadFromDir(t *testing.T) {
	t.Run("空目录不报错", func(t *testing.T) {
		mgr := NewHookManager("", true)
		if err := mgr.LoadFromDir(""); err != nil {
			t.Fatalf("空目录不应报错: %v", err)
		}
	})

	t.Run("不存在的目录不报错", func(t *testing.T) {
		mgr := NewHookManager("", true)
		if err := mgr.LoadFromDir("/nonexistent/path"); err != nil {
			t.Fatalf("不存在的目录不应报错: %v", err)
		}
	})

	t.Run("有效 YAML 文件加载", func(t *testing.T) {
		dir := t.TempDir()
		yamlContent := "event: pre_tool_call\ncommand: echo\n"
		if err := os.WriteFile(filepath.Join(dir, "hook.yaml"), []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		mgr := NewHookManager("", true)
		if err := mgr.LoadFromDir(dir); err != nil {
			t.Fatalf("LoadFromDir 不应报错: %v", err)
		}
	})

	t.Run("子目录被跳过", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "subdir"), 0755); err != nil {
			t.Fatal(err)
		}

		mgr := NewHookManager("", true)
		if err := mgr.LoadFromDir(dir); err != nil {
			t.Fatalf("含子目录不应报错: %v", err)
		}
	})

	t.Run("非 YAML 文件被跳过", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not yaml"), 0644); err != nil {
			t.Fatal(err)
		}

		mgr := NewHookManager("", true)
		if err := mgr.LoadFromDir(dir); err != nil {
			t.Fatalf("非 YAML 文件不应报错: %v", err)
		}
	})

	t.Run("无效 YAML 跳过不报错", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("::invalid::"), 0644); err != nil {
			t.Fatal(err)
		}

		mgr := NewHookManager("", true)
		if err := mgr.LoadFromDir(dir); err != nil {
			t.Fatalf("无效 YAML 应跳过: %v", err)
		}
	})

	t.Run("无效事件类型跳过不报错", func(t *testing.T) {
		dir := t.TempDir()
		yamlContent := "event: invalid\ncommand: echo\n"
		if err := os.WriteFile(filepath.Join(dir, "bad_event.yaml"), []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		mgr := NewHookManager("", true)
		if err := mgr.LoadFromDir(dir); err != nil {
			t.Fatalf("无效事件应跳过: %v", err)
		}
	})

	t.Run("空 command 的 YAML 跳过", func(t *testing.T) {
		dir := t.TempDir()
		yamlContent := "event: pre_tool_call\ncommand: \"\"\n"
		if err := os.WriteFile(filepath.Join(dir, "empty_cmd.yaml"), []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		mgr := NewHookManager("", true)
		if err := mgr.LoadFromDir(dir); err != nil {
			t.Fatalf("空 command 应跳过: %v", err)
		}
	})
}

// TestLoadFromDir_BadYamlFile 验证损坏的 YAML 文件被跳过。
func TestLoadFromDir_BadYamlFile(t *testing.T) {
	dir := t.TempDir()
	// 写入无效 YAML
	badFile := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(badFile, []byte(":\n  :\n    - {\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := NewHookManager(dir, false)
	err := mgr.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("损坏的 YAML 文件应被跳过: %v", err)
	}
}

// TestLoadFromDir_NonexistentDir 验证不存在的目录不报错。
func TestLoadFromDir_NonexistentDir(t *testing.T) {
	mgr := NewHookManager(t.TempDir(), false)
	err := mgr.LoadFromDir("/nonexistent/directory/xyz")
	if err != nil {
		t.Fatalf("不存在的目录不应报错: %v", err)
	}
}

// ───────────────────────────── executeChain 未允许的 hook 测试 ─────────────────────────────

func TestExecuteChain_UnallowedHook(t *testing.T) {
	t.Run("未允许的 hook 且非终端 → 阻止 (pre)", func(t *testing.T) {
		mgr := NewHookManager("", false)
		hook, err := NewShellHook(HookSpec{
			Event:   EventPreToolCall,
			Command: "echo",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := mgr.Register(hook); err != nil {
			t.Fatal(err)
		}

		_, blocked, err := mgr.ExecutePreHooks(context.Background(), "file_read", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !blocked {
			t.Error("未允许的 hook 在非终端模式下应阻止")
		}
	})

	t.Run("未允许的 hook (post) → 不阻止", func(t *testing.T) {
		mgr := NewHookManager("", false)
		hook, err := NewShellHook(HookSpec{
			Event:   EventPostToolCall,
			Command: "echo",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := mgr.Register(hook); err != nil {
			t.Fatal(err)
		}

		err = mgr.ExecutePostHooks(context.Background(), "file_read", nil, "output")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// ───────────────────────────── 允许的 hook 执行完整流程测试 ─────────────────────────────

func TestExecuteChain_AllowedHookExecutes(t *testing.T) {
	t.Run("acceptAll=true 时 hook 正常执行", func(t *testing.T) {
		mgr := NewHookManager("", true)

		dir := t.TempDir()
		script := filepath.Join(dir, "allow.sh")
		content := "#!/bin/sh\necho '{\"decision\":\"allow\"}'\n"
		if err := os.WriteFile(script, []byte(content), 0755); err != nil {
			t.Fatal(err)
		}

		hook, err := NewShellHook(HookSpec{
			Event:      EventPreToolCall,
			Command:    "sh " + script,
			TimeoutSec: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := mgr.Register(hook); err != nil {
			t.Fatal(err)
		}

		resp, blocked, err := mgr.ExecutePreHooks(context.Background(), "file_read", map[string]any{"key": "val"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if blocked {
			t.Error("allow hook 不应阻止")
		}
		if resp != nil {
			t.Error("allow 链应返回 nil resp")
		}
	})
}
