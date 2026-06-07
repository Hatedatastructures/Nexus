package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ───────────────────────────── 覆盖率补充测试 ─────────────────────────────

// TestRegister_NilHook 验证注册 nil hook 返回错误。
func TestRegister_NilHook(t *testing.T) {
	mgr := NewHookManager(t.TempDir(), false)
	err := mgr.Register(nil)
	if err == nil {
		t.Fatal("应返回错误")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("错误信息应包含 nil: %v", err)
	}
}

// TestExecuteChain_HookFailureSkipped 验证 hook 执行失败时不终止链。
func TestExecuteChain_HookFailureSkipped(t *testing.T) {
	dir := t.TempDir()
	mgr := NewHookManager(dir, true)

	// 注册一个不存在的命令，执行会失败
	hook, err := NewShellHook(HookSpec{
		Event:   EventPreToolCall,
		Command: "sh /nonexistent/script.sh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Register(hook); err != nil {
		t.Fatal(err)
	}

	// 执行失败应被跳过，不返回错误
	resp, blocked, err := mgr.ExecutePreHooks(context.Background(), "tool", nil)
	if err != nil {
		t.Fatalf("hook 失败不应返回错误: %v", err)
	}
	if blocked {
		t.Error("hook 失败不应阻止")
	}
	if resp != nil {
		t.Error("hook 失败应返回 nil resp")
	}
}

// TestExecuteChain_BlockTerminatesChain 验证 block 响应终止 pre hook 链。
func TestExecuteChain_BlockTerminatesChain(t *testing.T) {
	dir := t.TempDir()
	mgr := NewHookManager(dir, true)

	// 创建一个返回 block 的脚本
	script := filepath.Join(dir, "block.sh")
	content := "#!/bin/sh\necho '{\"decision\":\"block\",\"reason\":\"blocked by test\"}'\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	hook, err := NewShellHook(HookSpec{
		Event:   EventPreToolCall,
		Command: "sh " + script,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Register(hook); err != nil {
		t.Fatal(err)
	}

	resp, blocked, err := mgr.ExecutePreHooks(context.Background(), "tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !blocked {
		t.Error("block hook 应阻止")
	}
	if resp == nil {
		t.Fatal("block 应返回非 nil resp")
	}
	if resp.Decision != "block" {
		t.Errorf("decision = %q, want block", resp.Decision)
	}
}

// TestExecuteChain_PostHooksExecuteAll 验证 post hook 执行。
func TestExecuteChain_PostHooksExecuteAll(t *testing.T) {
	dir := t.TempDir()
	mgr := NewHookManager(dir, true)

	// 创建一个正常返回的 post hook 脚本
	script := filepath.Join(dir, "post.sh")
	content := "#!/bin/sh\necho '{\"decision\":\"allow\"}'\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	hook, err := NewShellHook(HookSpec{
		Event:   EventPostToolCall,
		Command: "sh " + script,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Register(hook); err != nil {
		t.Fatal(err)
	}

	err = mgr.ExecutePostHooks(context.Background(), "tool", nil, "output")
	if err != nil {
		t.Fatalf("post hook 不应返回错误: %v", err)
	}
}

// TestExecuteChain_MatcherFiltersTool 验证 matcher 过滤不匹配的工具。
func TestExecuteChain_MatcherFiltersTool(t *testing.T) {
	dir := t.TempDir()
	mgr := NewHookManager(dir, true)

	hook, err := NewShellHook(HookSpec{
		Event:   EventPreToolCall,
		Command: "sh /nonexistent/script.sh",
		Matcher: "^bash_exec$",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Register(hook); err != nil {
		t.Fatal(err)
	}

	// 工具名不匹配 → hook 不执行
	resp, blocked, err := mgr.ExecutePreHooks(context.Background(), "file_read", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if blocked {
		t.Error("不匹配的 hook 不应阻止")
	}
	if resp != nil {
		t.Error("不匹配的 hook 应返回 nil resp")
	}
}

// TestExecuteChain_EventFiltering 验证 pre hook 不匹配 post 事件。
func TestExecuteChain_EventFiltering(t *testing.T) {
	dir := t.TempDir()
	mgr := NewHookManager(dir, true)

	// 注册 pre hook
	hook, err := NewShellHook(HookSpec{
		Event:   EventPreToolCall,
		Command: "sh /nonexistent/script.sh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Register(hook); err != nil {
		t.Fatal(err)
	}

	// 执行 post hooks → pre hook 不应匹配
	err = mgr.ExecutePostHooks(context.Background(), "tool", nil, "output")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestExecuteChain_NoMatchingHooks 验证无匹配 hook 时返回 nil。
func TestExecuteChain_NoMatchingHooks(t *testing.T) {
	dir := t.TempDir()
	mgr := NewHookManager(dir, true)

	resp, blocked, err := mgr.ExecutePreHooks(context.Background(), "tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if blocked {
		t.Error("无 hook 不应阻止")
	}
	if resp != nil {
		t.Error("无 hook 应返回 nil resp")
	}
}
