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

func TestNewShellExecutor(t *testing.T) {
	e := NewShellExecutor()
	if e == nil {
		t.Fatal("NewShellExecutor 不应返回 nil")
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
