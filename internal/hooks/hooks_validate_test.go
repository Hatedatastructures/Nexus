package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

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

// TestValidateHookCommand_AbsolutePathRedirected 验证路径重定向被拒绝。
func TestValidateHookCommand_AbsolutePathRedirected(t *testing.T) {
	err := validateHookCommand("/usr/bin/../bin/sh")
	if err == nil {
		t.Error("被重定向的路径应被拒绝")
	}
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
