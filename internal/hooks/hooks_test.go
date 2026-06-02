package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ───────────────────────────── HookResponse 方法测试 ─────────────────────────────

func TestHookResponse_IsBlock(t *testing.T) {
	tests := []struct {
		decision string
		want     bool
	}{
		{"block", true},
		{"allow", false},
		{"modify", false},
		{"", false},
	}
	for _, tt := range tests {
		r := &HookResponse{Decision: tt.decision}
		if got := r.IsBlock(); got != tt.want {
			t.Errorf("IsBlock(%q) = %v, want %v", tt.decision, got, tt.want)
		}
	}
}

func TestHookResponse_IsModify(t *testing.T) {
	tests := []struct {
		decision string
		want     bool
	}{
		{"modify", true},
		{"allow", false},
		{"block", false},
		{"", false},
	}
	for _, tt := range tests {
		r := &HookResponse{Decision: tt.decision}
		if got := r.IsModify(); got != tt.want {
			t.Errorf("IsModify(%q) = %v, want %v", tt.decision, got, tt.want)
		}
	}
}

// ───────────────────────────── CompileMatcher 测试 ─────────────────────────────

func TestCompileMatcher(t *testing.T) {
	t.Run("空字符串返回 nil", func(t *testing.T) {
		re, err := CompileMatcher("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if re != nil {
			t.Error("空字符串应返回 nil")
		}
	})

	t.Run("有效正则", func(t *testing.T) {
		re, err := CompileMatcher("file_.*")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if re == nil {
			t.Fatal("有效正则不应返回 nil")
		}
		if !re.MatchString("file_read") {
			t.Error("应匹配 file_read")
		}
	})

	t.Run("无效正则返回错误", func(t *testing.T) {
		_, err := CompileMatcher("[invalid")
		if err == nil {
			t.Fatal("无效正则应返回错误")
		}
	})
}

// ───────────────────────────── ValidateEvent 测试 ─────────────────────────────

func TestValidateEvent(t *testing.T) {
	tests := []struct {
		event   string
		wantErr bool
	}{
		{EventPreToolCall, false},
		{EventPostToolCall, false},
		{"invalid_event", true},
		{"", true},
		{"PRE_TOOL_CALL", true},
	}
	for _, tt := range tests {
		err := ValidateEvent(tt.event)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateEvent(%q) err = %v, wantErr %v", tt.event, err, tt.wantErr)
		}
	}
}

// ───────────────────────────── ShellHook 创建与字段测试 ─────────────────────────────

func TestNewShellHook(t *testing.T) {
	t.Run("有效 pre_tool_call hook", func(t *testing.T) {
		hook, err := NewShellHook(HookSpec{
			Event:   EventPreToolCall,
			Command: "echo",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hook.Event() != EventPreToolCall {
			t.Errorf("Event() = %q, want %q", hook.Event(), EventPreToolCall)
		}
		if hook.Command() != "echo" {
			t.Errorf("Command() = %q, want %q", hook.Command(), "echo")
		}
		if hook.TimeoutSec() != 60 {
			t.Errorf("default TimeoutSec = %d, want 60", hook.TimeoutSec())
		}
	})

	t.Run("自定义超时", func(t *testing.T) {
		hook, err := NewShellHook(HookSpec{
			Event:      EventPostToolCall,
			Command:    "echo",
			TimeoutSec: 120,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hook.TimeoutSec() != 120 {
			t.Errorf("TimeoutSec = %d, want 120", hook.TimeoutSec())
		}
	})

	t.Run("超时上限 300", func(t *testing.T) {
		hook, err := NewShellHook(HookSpec{
			Event:      EventPreToolCall,
			Command:    "echo",
			TimeoutSec: 500,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hook.TimeoutSec() != 300 {
			t.Errorf("TimeoutSec = %d, want 300", hook.TimeoutSec())
		}
	})

	t.Run("无效事件类型", func(t *testing.T) {
		_, err := NewShellHook(HookSpec{
			Event:   "invalid",
			Command: "echo",
		})
		if err == nil {
			t.Fatal("无效事件应返回错误")
		}
	})

	t.Run("空 command", func(t *testing.T) {
		_, err := NewShellHook(HookSpec{
			Event:   EventPreToolCall,
			Command: "",
		})
		if err == nil {
			t.Fatal("空 command 应返回错误")
		}
	})

	t.Run("无效 matcher", func(t *testing.T) {
		_, err := NewShellHook(HookSpec{
			Event:   EventPreToolCall,
			Command: "echo",
			Matcher: "[invalid",
		})
		if err == nil {
			t.Fatal("无效 matcher 应返回错误")
		}
	})

	t.Run("Name 使用 command", func(t *testing.T) {
		hook, err := NewShellHook(HookSpec{
			Event:   EventPreToolCall,
			Command: "echo hello",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hook.Name() != "echo hello" {
			t.Errorf("Name() = %q, want %q", hook.Name(), "echo hello")
		}
	})
}

func TestShellHook_Match(t *testing.T) {
	t.Run("空 matcher 匹配所有", func(t *testing.T) {
		hook, err := NewShellHook(HookSpec{
			Event:   EventPreToolCall,
			Command: "echo",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !hook.Match("anything") {
			t.Error("空 matcher 应匹配所有")
		}
	})

	t.Run("正则匹配", func(t *testing.T) {
		hook, err := NewShellHook(HookSpec{
			Event:   EventPreToolCall,
			Command: "echo",
			Matcher: "file_.*",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !hook.Match("file_read") {
			t.Error("应匹配 file_read")
		}
		if hook.Match("bash") {
			t.Error("不应匹配 bash")
		}
	})
}

// ───────────────────────────── validateHookCommand 测试 ─────────────────────────────

func TestValidateHookCommand(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		wantErr bool
	}{
		{"echo 不在白名单", "echo hello", true},
		{"bash 在白名单", "bash script.sh", false},
		{"sh 在白名单", "sh run.sh", false},
		{"node 在白名单", "node hook.js", false},
		{"python3 在白名单", "python3 hook.py", false},
		{"python 在白名单", "python hook.py", false},
		{"zsh 在白名单", "zsh hook.sh", false},
		{"dash 在白名单", "dash hook.sh", false},
		{"空命令", "", true},
		{"只有空白", "   ", true},
		{"分号", "echo; rm -rf /", true},
		{"换行符", "echo\nrm", true},
		{"回车符", "echo\rrm", true},
		{"AND 链", "echo && rm", true},
		{"OR 链", "echo || rm", true},
		{"命令替换括号", "echo $(whoami)", true},
		{"反引号", "echo `whoami`", true},
		{"未知命令不在白名单", "curl http://example.com", true},
		{"绝对路径需存在", "/nonexistent/binary", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHookCommand(tt.cmd)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateHookCommand(%q) err = %v, wantErr %v", tt.cmd, err, tt.wantErr)
			}
		})
	}
}

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

// TestAllowlist_SaveToReadOnlyDir 验证 save 失败时不会 panic。
func TestAllowlist_SaveToReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	// 创建一个只读目录
	readOnlyDir := filepath.Join(dir, "readonly")
	if err := os.Mkdir(readOnlyDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(readOnlyDir, 0755) })

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

// TestValidateHookCommand_AbsolutePathRedirected 验证路径重定向被拒绝。
func TestValidateHookCommand_AbsolutePathRedirected(t *testing.T) {
	err := validateHookCommand("/usr/bin/../bin/sh")
	if err == nil {
		t.Error("被重定向的路径应被拒绝")
	}
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

// TestLoadFromDir_NonexistentDir 验证不存在的目录不报错。
func TestLoadFromDir_NonexistentDir(t *testing.T) {
	mgr := NewHookManager(t.TempDir(), false)
	err := mgr.LoadFromDir("/nonexistent/directory/xyz")
	if err != nil {
		t.Fatalf("不存在的目录不应报错: %v", err)
	}
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

// TestParseResponse_BlockWithReason 验证 block 响应补充默认 reason。
func TestParseResponse_BlockWithReason(t *testing.T) {
	executor := NewShellExecutor()
	dir := t.TempDir()

	script := filepath.Join(dir, "block_no_reason.sh")
	content := "#!/bin/sh\necho '{\"decision\":\"block\"}'\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	hook := &ShellHook{
		name:       "test",
		event:      EventPreToolCall,
		command:    "sh " + script,
		timeoutSec: 5,
	}

	resp, err := executor.Execute(context.Background(), hook, &HookEvent{EventName: EventPreToolCall})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Reason == "" {
		t.Error("block 无 reason 时应补充默认 reason")
	}
}

// TestParseResponse_InvalidJSONDefaultsBlock 验证无效 JSON 默认返回 block。
func TestParseResponse_InvalidJSON(t *testing.T) {
	executor := NewShellExecutor()
	dir := t.TempDir()

	script := filepath.Join(dir, "invalid.sh")
	content := "#!/bin/sh\necho 'not json at all'\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	hook := &ShellHook{
		name:       "test",
		event:      EventPreToolCall,
		command:    "sh " + script,
		timeoutSec: 5,
	}

	resp, err := executor.Execute(context.Background(), hook, &HookEvent{EventName: EventPreToolCall})
	if err != nil {
		t.Fatalf("parseResponse 失败应返回 block resp 而非 error: %v", err)
	}
	if resp.Decision != "block" {
		t.Errorf("解析失败应默认 block, got %q", resp.Decision)
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

// ───────────────────────────── parseResponse 测试 ─────────────────────────────

func TestParseResponse(t *testing.T) {
	t.Run("完整 HookResponse JSON", func(t *testing.T) {
		input := `{"decision":"block","reason":"forbidden","message":"msg"}`
		resp, err := parseResponse([]byte(input))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Decision != "block" {
			t.Errorf("Decision = %q, want %q", resp.Decision, "block")
		}
		if resp.Reason != "forbidden" {
			t.Errorf("Reason = %q, want %q", resp.Reason, "forbidden")
		}
		if resp.Message != "msg" {
			t.Errorf("Message = %q, want %q", resp.Message, "msg")
		}
	})

	t.Run("精简 decision-only JSON", func(t *testing.T) {
		input := `{"decision":"allow"}`
		resp, err := parseResponse([]byte(input))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Decision != "allow" {
			t.Errorf("Decision = %q, want %q", resp.Decision, "allow")
		}
	})

	t.Run("无效 JSON", func(t *testing.T) {
		_, err := parseResponse([]byte("not json"))
		if err == nil {
			t.Fatal("无效 JSON 应返回错误")
		}
	})

	t.Run("空 decision", func(t *testing.T) {
		input := `{"decision":""}`
		_, err := parseResponse([]byte(input))
		if err == nil {
			t.Fatal("空 decision 应返回错误")
		}
	})

	t.Run("JSON 缺少 decision 字段", func(t *testing.T) {
		input := `{"reason":"test"}`
		_, err := parseResponse([]byte(input))
		if err == nil {
			t.Fatal("缺少 decision 字段应返回错误")
		}
	})
}

// ───────────────────────────── ShellExecutor.Execute 测试 ─────────────────────────────

func TestShellExecutor_Execute(t *testing.T) {
	t.Run("sh 脚本返回非 HookResponse JSON → 默认 block", func(t *testing.T) {
		dir := t.TempDir()
		script := filepath.Join(dir, "echo_raw.sh")
		content := "#!/bin/sh\necho 'not json'\n"
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

		executor := NewShellExecutor()
		event := &HookEvent{
			EventName: EventPreToolCall,
			ToolName:  "file_read",
		}
		resp, err := executor.Execute(context.Background(), hook, event)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp == nil {
			t.Fatal("resp should not be nil")
		}
		if resp.Decision != "block" {
			t.Errorf("Decision = %q, want %q", resp.Decision, "block")
		}
	})

	t.Run("block 无 reason 时自动补充", func(t *testing.T) {
		dir := t.TempDir()
		script := filepath.Join(dir, "block_hook.sh")
		content := "#!/bin/sh\necho '{\"decision\":\"block\"}'\n"
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

		executor := NewShellExecutor()
		resp, err := executor.Execute(context.Background(), hook, &HookEvent{
			EventName: EventPreToolCall,
			ToolName:  "file_write",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Decision != "block" {
			t.Errorf("Decision = %q, want %q", resp.Decision, "block")
		}
		if resp.Reason != "被 shell hook 阻止" {
			t.Errorf("Reason = %q, want default reason", resp.Reason)
		}
	})

	t.Run("无效命令报错", func(t *testing.T) {
		hook, err := NewShellHook(HookSpec{
			Event:      EventPreToolCall,
			Command:    "curl http://evil.com",
			TimeoutSec: 5,
		})
		if err != nil {
			t.Fatal(err)
		}

		executor := NewShellExecutor()
		_, err = executor.Execute(context.Background(), hook, &HookEvent{})
		if err == nil {
			t.Fatal("无效命令应返回错误")
		}
	})

	t.Run("超时", func(t *testing.T) {
		dir := t.TempDir()
		script := filepath.Join(dir, "sleep_hook.sh")
		content := "#!/bin/sh\nsleep 10\n"
		if err := os.WriteFile(script, []byte(content), 0755); err != nil {
			t.Fatal(err)
		}

		hook, err := NewShellHook(HookSpec{
			Event:      EventPreToolCall,
			Command:    "sh " + script,
			TimeoutSec: 1,
		})
		if err != nil {
			t.Fatal(err)
		}

		executor := NewShellExecutor()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_, err = executor.Execute(ctx, hook, &HookEvent{})
		if err == nil {
			t.Fatal("超时执行应返回错误")
		}
	})
}

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

// ───────────────────────────── parseHookFile 测试 ─────────────────────────────

func TestParseHookFile(t *testing.T) {
	t.Run("有效 YAML", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "hook.yaml")
		content := "event: pre_tool_call\ncommand: echo hello\nmatcher: file_.*\ntimeout: 30\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		spec, err := parseHookFile(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if spec.Event != EventPreToolCall {
			t.Errorf("Event = %q, want %q", spec.Event, EventPreToolCall)
		}
		if spec.Command != "echo hello" {
			t.Errorf("Command = %q, want %q", spec.Command, "echo hello")
		}
		if spec.Matcher != "file_.*" {
			t.Errorf("Matcher = %q, want %q", spec.Matcher, "file_.*")
		}
		if spec.TimeoutSec != 30 {
			t.Errorf("TimeoutSec = %d, want 30", spec.TimeoutSec)
		}
	})

	t.Run("文件不存在", func(t *testing.T) {
		_, err := parseHookFile("/nonexistent/file.yaml")
		if err == nil {
			t.Fatal("不存在的文件应返回错误")
		}
	})

	t.Run("无效 YAML 解析为空 spec", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "bad.yaml")
		if err := os.WriteFile(path, []byte("::invalid::"), 0644); err != nil {
			t.Fatal(err)
		}

		spec, err := parseHookFile(path)
		if err != nil {
			// YAML 解析器可能报错，也可能不报错，取决于内容
			return
		}
		// 如果没报错，spec 应该是零值
		if spec.Event != "" || spec.Command != "" {
			t.Errorf("无效 YAML 应解析为零值 spec, got event=%q command=%q", spec.Event, spec.Command)
		}
	})
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

// ───────────────────────────── HookEvent JSON 序列化测试 ─────────────────────────────

func TestHookEvent_JSON(t *testing.T) {
	event := &HookEvent{
		EventName:  EventPreToolCall,
		ToolName:   "file_write",
		ToolInput:  map[string]any{"path": "/tmp/test.txt"},
		ToolOutput: "",
		SessionID:  "sess-1",
		CWD:        "/home/user",
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("JSON marshal error: %v", err)
	}
	if !strings.Contains(string(data), "file_write") {
		t.Error("JSON 应包含 tool_name")
	}
	if strings.Contains(string(data), "tool_output") {
		t.Error("空的 ToolOutput 不应出现在 JSON 中 (omitempty)")
	}
}

// ───────────────────────────── NewShellExecutor 测试 ─────────────────────────────

func TestNewShellExecutor(t *testing.T) {
	e := NewShellExecutor()
	if e == nil {
		t.Fatal("NewShellExecutor 不应返回 nil")
	}
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
