package testutil

import (
	"context"
	"testing"

	"nexus-agent/internal/approval"
)

func TestFakeCheckerMode(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		f := NewFakeChecker()
		if f.Mode() != "off" {
			t.Errorf("Mode() = %q, want %q", f.Mode(), "off")
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		f := &FakeChecker{Mode_: "smart"}
		if f.Mode() != "smart" {
			t.Errorf("Mode() = %q, want %q", f.Mode(), "smart")
		}
	})
}

func TestFakeCheckerSetMode(t *testing.T) {
	t.Parallel()
	f := NewFakeChecker()
	f.SetMode("always")
	if f.Mode() != "always" {
		t.Errorf("Mode() = %q after SetMode, want %q", f.Mode(), "always")
	}
}

func TestFakeCheckerCheckDefault(t *testing.T) {
	t.Parallel()
	f := NewFakeChecker()
	result, reason := f.Check(context.Background(), "ls -la")
	if result != approval.Approved {
		t.Errorf("Check() = %v, want %v", result, approval.Approved)
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestFakeCheckerCheckRecordsCommand(t *testing.T) {
	t.Parallel()
	f := NewFakeChecker()
	_, _ = f.Check(context.Background(), "rm -rf /")
	_, _ = f.Check(context.Background(), "git push")
	if len(f.CheckedCommands) != 2 {
		t.Fatalf("CheckedCommands len = %d, want 2", len(f.CheckedCommands))
	}
	if f.CheckedCommands[0].Command != "rm -rf /" {
		t.Errorf("CheckedCommands[0].Command = %q", f.CheckedCommands[0].Command)
	}
}

func TestFakeCheckerCheckWithDeniedCommands(t *testing.T) {
	t.Parallel()
	f := NewFakeChecker()
	f.AddDeniedCommand("rm -rf /", "dangerous command")

	result, reason := f.Check(context.Background(), "rm -rf /")
	if result != approval.Denied {
		t.Errorf("Check() = %v, want %v", result, approval.Denied)
	}
	if reason != "dangerous command" {
		t.Errorf("reason = %q, want %q", reason, "dangerous command")
	}

	// Non-denied command should still be approved
	result2, _ := f.Check(context.Background(), "ls")
	if result2 != approval.Approved {
		t.Errorf("Check(ls) = %v, want %v", result2, approval.Approved)
	}
}

func TestFakeCheckerCheckWithCustomFunc(t *testing.T) {
	t.Parallel()
	f := &FakeChecker{
		CheckFunc: func(ctx context.Context, command string) (approval.Result, string) {
			return approval.Denied, "custom denial"
		},
	}
	result, reason := f.Check(context.Background(), "anything")
	if result != approval.Denied {
		t.Errorf("Check() = %v, want %v", result, approval.Denied)
	}
	if reason != "custom denial" {
		t.Errorf("reason = %q, want %q", reason, "custom denial")
	}
}

func TestFakeCheckerCheckWithDefaultResult(t *testing.T) {
	t.Parallel()
	f := &FakeChecker{
		DefaultResult: approval.Denied,
		DefaultReason: "default denied",
	}
	result, reason := f.Check(context.Background(), "cmd")
	if result != approval.Denied {
		t.Errorf("Check() = %v, want %v", result, approval.Denied)
	}
	if reason != "default denied" {
		t.Errorf("reason = %q, want %q", reason, "default denied")
	}
}

func TestFakeCheckerCheckToolDefault(t *testing.T) {
	t.Parallel()
	f := NewFakeChecker()
	result, _ := f.CheckTool(context.Background(), "bash", map[string]any{"command": "ls"})
	if result != approval.Approved {
		t.Errorf("CheckTool() = %v, want %v", result, approval.Approved)
	}
	if len(f.CheckedTools) != 1 {
		t.Fatalf("CheckedTools len = %d, want 1", len(f.CheckedTools))
	}
	if f.CheckedTools[0].ToolName != "bash" {
		t.Errorf("ToolName = %q, want %q", f.CheckedTools[0].ToolName, "bash")
	}
}

func TestFakeCheckerCheckToolWithCustomFunc(t *testing.T) {
	t.Parallel()
	f := &FakeChecker{
		CheckToolFunc: func(ctx context.Context, toolName string, args map[string]any) (approval.Result, string) {
			return approval.Denied, "tool denied"
		},
	}
	result, _ := f.CheckTool(context.Background(), "dangerous_tool", nil)
	if result != approval.Denied {
		t.Errorf("CheckTool() = %v, want %v", result, approval.Denied)
	}
}

func TestFakeCheckerCheckToolWithDefaultResult(t *testing.T) {
	t.Parallel()
	f := &FakeChecker{
		DefaultResult: approval.Denied,
		DefaultReason: "all denied",
	}
	result, reason := f.CheckTool(context.Background(), "tool", nil)
	if result != approval.Denied {
		t.Errorf("CheckTool() = %v, want %v", result, approval.Denied)
	}
	if reason != "all denied" {
		t.Errorf("reason = %q, want %q", reason, "all denied")
	}
}

func TestFakeCheckerReset(t *testing.T) {
	t.Parallel()
	f := NewFakeChecker()
	_, _ = f.Check(context.Background(), "cmd")
	_, _ = f.CheckTool(context.Background(), "tool", nil)
	f.Reset()
	if len(f.CheckedCommands) != 0 {
		t.Error("CheckedCommands not cleared")
	}
	if len(f.CheckedTools) != 0 {
		t.Error("CheckedTools not cleared")
	}
}

func TestFakeCheckerAddDeniedCommand(t *testing.T) {
	t.Parallel()
	f := NewFakeChecker()
	f.AddDeniedCommand("cmd1", "reason1")
	f.AddDeniedCommand("cmd2", "reason2")
	if len(f.DeniedCommands) != 2 {
		t.Fatalf("DeniedCommands len = %d, want 2", len(f.DeniedCommands))
	}
	if f.DeniedCommands["cmd1"] != "reason1" {
		t.Errorf("DeniedCommands[cmd1] = %q", f.DeniedCommands["cmd1"])
	}
}

func TestFakeCheckerSetDefaultResult(t *testing.T) {
	t.Parallel()
	f := NewFakeChecker()
	f.SetDefaultResult(approval.Denied, "new default")
	if f.DefaultResult != approval.Denied {
		t.Errorf("DefaultResult = %v, want %v", f.DefaultResult, approval.Denied)
	}
	if f.DefaultReason != "new default" {
		t.Errorf("DefaultReason = %q, want %q", f.DefaultReason, "new default")
	}
}

func TestNewDenyAllChecker(t *testing.T) {
	t.Parallel()
	f := NewDenyAllChecker()
	if f.Mode() != "always" {
		t.Errorf("Mode() = %q, want %q", f.Mode(), "always")
	}
	result, reason := f.Check(context.Background(), "any command")
	if result != approval.Denied {
		t.Errorf("Check() = %v, want %v", result, approval.Denied)
	}
	_ = reason
}

func TestFakeCheckerAddDeniedCommandInitializesMap(t *testing.T) {
	t.Parallel()
	f := &FakeChecker{} // DeniedCommands is nil
	f.AddDeniedCommand("test", "test reason")
	if f.DeniedCommands == nil {
		t.Fatal("DeniedCommands should be initialized")
	}
}
