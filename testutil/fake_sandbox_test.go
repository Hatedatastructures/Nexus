package testutil

import (
	"context"
	"fmt"
	"testing"
	"time"

	"nexus-agent/internal/sandbox"
)

func TestFakeEnvironmentExecute(t *testing.T) {
	t.Parallel()

	t.Run("default result", func(t *testing.T) {
		t.Parallel()
		env := NewFakeEnvironment("")
		result, err := env.Execute(context.Background(), "echo hello", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ExitCode != 0 {
			t.Errorf("ExitCode = %d, want 0", result.ExitCode)
		}
	})

	t.Run("records command", func(t *testing.T) {
		t.Parallel()
		env := NewFakeEnvironment("")
		_, _ = env.Execute(context.Background(), "ls -la", nil)
		cmds := env.ExecutedCommandsText()
		if len(cmds) != 1 || cmds[0] != "ls -la" {
			t.Errorf("ExecutedCommandsText() = %v, want [ls -la]", cmds)
		}
	})

	t.Run("preset command output", func(t *testing.T) {
		t.Parallel()
		env := NewFakeEnvironment("")
		env.SetCommandOutput("git", &sandbox.ExecuteResult{
			Stdout:   "v1.21.0",
			ExitCode: 0,
		})
		result, err := env.Execute(context.Background(), "git --version", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Stdout != "v1.21.0" {
			t.Errorf("Stdout = %q, want %q", result.Stdout, "v1.21.0")
		}
	})

	t.Run("preset command error", func(t *testing.T) {
		t.Parallel()
		env := NewFakeEnvironment("")
		env.SetCommandError("rm", fmt.Errorf("permission denied"))
		_, err := env.Execute(context.Background(), "rm -rf /", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if err.Error() != "permission denied" {
			t.Errorf("error = %q, want %q", err.Error(), "permission denied")
		}
	})

	t.Run("global execute error", func(t *testing.T) {
		t.Parallel()
		env := &FakeEnvironment{ExecuteError: fmt.Errorf("system error")}
		_, err := env.Execute(context.Background(), "anything", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("default result fallback", func(t *testing.T) {
		t.Parallel()
		env := &FakeEnvironment{
			DefaultResult: &sandbox.ExecuteResult{Stdout: "fallback", ExitCode: 0},
		}
		result, err := env.Execute(context.Background(), "unknown", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Stdout != "fallback" {
			t.Errorf("Stdout = %q, want %q", result.Stdout, "fallback")
		}
	})
}

func TestFakeEnvironmentExecuteBackground(t *testing.T) {
	t.Parallel()

	t.Run("default handle", func(t *testing.T) {
		t.Parallel()
		env := NewFakeEnvironment("")
		handle, err := env.ExecuteBackground(context.Background(), "sleep 10", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if handle == nil {
			t.Fatal("expected non-nil handle")
		}
	})

	t.Run("preset background handle", func(t *testing.T) {
		t.Parallel()
		expectedHandle := NewFakeProcessHandle("out", "err", 0)
		env := &FakeEnvironment{BackgroundHandle: expectedHandle}
		handle, err := env.ExecuteBackground(context.Background(), "cmd", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if handle != expectedHandle {
			t.Error("expected preset handle to be returned")
		}
	})

	t.Run("background error", func(t *testing.T) {
		t.Parallel()
		env := &FakeEnvironment{BackgroundError: fmt.Errorf("bg error")}
		_, err := env.ExecuteBackground(context.Background(), "cmd", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestFakeEnvironmentCWD(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		env := &FakeEnvironment{}
		if env.CWD() != "/tmp" {
			t.Errorf("CWD() = %q, want %q", env.CWD(), "/tmp")
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		env := NewFakeEnvironment("/home/user/project")
		if env.CWD() != "/home/user/project" {
			t.Errorf("CWD() = %q, want %q", env.CWD(), "/home/user/project")
		}
	})

	t.Run("update", func(t *testing.T) {
		t.Parallel()
		env := NewFakeEnvironment("")
		env.UpdateCWD("/new/path")
		if env.CWD() != "/new/path" {
			t.Errorf("CWD() = %q, want %q", env.CWD(), "/new/path")
		}
	})
}

func TestFakeEnvironmentCleanup(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		env := NewFakeEnvironment("")
		if err := env.Cleanup(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !env.CleanupCalled {
			t.Error("CleanupCalled should be true")
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()
		env := &FakeEnvironment{CleanupError: fmt.Errorf("cleanup failed")}
		if err := env.Cleanup(); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestFakeEnvironmentReset(t *testing.T) {
	t.Parallel()
	env := NewFakeEnvironment("")
	_, _ = env.Execute(context.Background(), "cmd", nil)
	env.Reset()
	if len(env.ExecutedCommands) != 0 {
		t.Error("ExecutedCommands not cleared")
	}
	if env.CleanupCalled {
		t.Error("CleanupCalled should be false after Reset")
	}
}

func TestFakeEnvironmentLastCommand(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		env := NewFakeEnvironment("")
		_, err := env.LastCommand()
		if err == nil {
			t.Fatal("expected error for no commands")
		}
	})

	t.Run("returns last", func(t *testing.T) {
		t.Parallel()
		env := NewFakeEnvironment("")
		_, _ = env.Execute(context.Background(), "first", nil)
		_, _ = env.Execute(context.Background(), "second", nil)
		last, err := env.LastCommand()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if last != "second" {
			t.Errorf("LastCommand() = %q, want %q", last, "second")
		}
	})
}

func TestNewFakeEnvironmentWithOutputs(t *testing.T) {
	t.Parallel()
	env := NewFakeEnvironmentWithOutputs("/tmp", map[string]string{
		"echo": "hello world",
		"pwd":  "/tmp/test",
	})
	result, err := env.Execute(context.Background(), "echo hi", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Stdout != "hello world" {
		t.Errorf("Stdout = %q, want %q", result.Stdout, "hello world")
	}

	result, err = env.Execute(context.Background(), "pwd", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Stdout != "/tmp/test" {
		t.Errorf("Stdout = %q, want %q", result.Stdout, "/tmp/test")
	}
}

// ───────────────────────────── FakeProcessHandle ─────────────────────────────

func TestFakeProcessHandlePoll(t *testing.T) {
	t.Parallel()
	h := NewFakeProcessHandle("out", "err", 42)
	code, err := h.Poll()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code == nil || *code != 42 {
		t.Errorf("Poll() = %v, want 42", code)
	}
}

func TestFakeProcessHandleKill(t *testing.T) {
	t.Parallel()
	h := NewFakeProcessHandle("out", "err", 0)
	if err := h.Kill(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	code, _ := h.Poll()
	if code == nil || *code != 137 {
		t.Errorf("after Kill(), Poll() = %v, want 137", code)
	}
}

func TestFakeProcessHandleWait(t *testing.T) {
	t.Parallel()
	h := NewFakeProcessHandle("out", "err", 5)
	code, err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 5 {
		t.Errorf("Wait() = %d, want 5", code)
	}
}

func TestFakeProcessHandleWaitError(t *testing.T) {
	t.Parallel()
	h := &FakeProcessHandle{waitErr: fmt.Errorf("wait failed")}
	_, err := h.Wait(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFakeProcessHandleStdoutStderr(t *testing.T) {
	t.Parallel()
	h := NewFakeProcessHandle("output data", "error data", 0)

	stdout := h.Stdout()
	buf := make([]byte, 100)
	n, _ := stdout.Read(buf)
	if string(buf[:n]) != "output data" {
		t.Errorf("Stdout() = %q, want %q", string(buf[:n]), "output data")
	}

	stderr := h.Stderr()
	n, _ = stderr.Read(buf)
	if string(buf[:n]) != "error data" {
		t.Errorf("Stderr() = %q, want %q", string(buf[:n]), "error data")
	}
}

func TestNewRunningProcessHandle(t *testing.T) {
	t.Parallel()
	h := NewRunningProcessHandle("partial", "")
	code, _ := h.Poll()
	if code != nil {
		t.Errorf("running process Poll() should be nil, got %v", code)
	}
}

func TestFakeEnvironmentDefaultCWD(t *testing.T) {
	t.Parallel()
	env := NewFakeEnvironment("")
	if env.CWD() != "/tmp/test" {
		t.Errorf("NewFakeEnvironment('') CWD() = %q, want %q", env.CWD(), "/tmp/test")
	}
}

func TestFakeEnvironmentDuration(t *testing.T) {
	t.Parallel()
	env := NewFakeEnvironment("")
	result, _ := env.Execute(context.Background(), "cmd", nil)
	if result.Duration != time.Millisecond {
		t.Errorf("default Duration = %v, want %v", result.Duration, time.Millisecond)
	}
}
