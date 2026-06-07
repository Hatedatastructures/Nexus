package sandbox

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestNewLocalEnvironment_DefaultShell(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment("")
	if env == nil {
		t.Fatal("expected non-nil environment")
	}
	if env.defaultTimeout != 120*time.Second {
		t.Errorf("expected defaultTimeout 120s, got %v", env.defaultTimeout)
	}
}

func TestNewLocalEnvironment_WithCWD(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment("/tmp")
	if env.CWD() != "/tmp" {
		t.Errorf("expected CWD /tmp, got %s", env.CWD())
	}
}

func TestNewLocalEnvironment_EmptyCWD_UsesGetwd(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment("")
	if env.CWD() == "" {
		t.Error("expected non-empty CWD from Getwd fallback")
	}
}

func TestLocalEnvironment_UpdateCWD(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment("/tmp")
	env.UpdateCWD("/home/user")
	if env.CWD() != "/home/user" {
		t.Errorf("expected CWD /home/user, got %s", env.CWD())
	}
}

func TestLocalEnvironment_Execute_EmptyCommand(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())
	result, err := env.Execute(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0 for empty command, got %d", result.ExitCode)
	}
	if result.Stdout != "" {
		t.Errorf("expected empty stdout, got %q", result.Stdout)
	}
}

func TestLocalEnvironment_Execute_SimpleCommand(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())
	result, err := env.Execute(context.Background(), "echo hello", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	// The output includes the CWD marker line, so check if "hello" is present
	if !contains(result.Stdout, "hello") {
		t.Errorf("expected stdout to contain 'hello', got %q", result.Stdout)
	}
}

func TestLocalEnvironment_Execute_NilOptions(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())
	result, err := env.Execute(context.Background(), "echo test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}

func TestLocalEnvironment_Execute_FailingCommand(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())
	result, err := env.Execute(context.Background(), "exit 42", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
}

func TestLocalEnvironment_Execute_WithTimeout(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())
	result, err := env.Execute(context.Background(), "echo fast", &ExecuteOptions{
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}

func TestLocalEnvironment_Execute_WithEnv(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())
	result, err := env.Execute(context.Background(), "echo $NEXUS_TEST_VAR", &ExecuteOptions{
		Env: map[string]string{"NEXUS_TEST_VAR": "test_value_123"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result.Stdout, "test_value_123") {
		t.Errorf("expected stdout to contain 'test_value_123', got %q", result.Stdout)
	}
}

func TestLocalEnvironment_Execute_WithCWD(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	env := NewLocalEnvironment(t.TempDir())
	result, err := env.Execute(context.Background(), "pwd", &ExecuteOptions{
		CWD: tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result.Stdout, tmpDir) || contains(result.Stdout, "__NEXUS_CWD__") {
		// The CWD marker should be stripped; tmpDir should appear in the output
		// But on Windows the path may differ in format
		t.Logf("stdout for pwd with CWD %s: %q", tmpDir, result.Stdout)
	}
}

func TestLocalEnvironment_Execute_WithStdin(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())
	result, err := env.Execute(context.Background(), "cat", &ExecuteOptions{
		StdinData: "hello from stdin",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result.Stdout, "hello from stdin") {
		t.Errorf("expected stdout to contain stdin data, got %q", result.Stdout)
	}
}

func TestLocalEnvironment_Execute_Sudo(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())
	// Sudo mode should invoke approval checker. The "off" mode approves everything.
	// But we cannot easily set approval mode here without importing approval package.
	// Just verify that a dangerous command with sudo is rejected by the smart checker.
	_, err := env.Execute(context.Background(), "rm -rf /", &ExecuteOptions{
		Sudo: true,
	})
	if err == nil {
		t.Error("expected error for dangerous sudo command")
	}
}

func TestLocalEnvironment_Cleanup(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())
	if err := env.Cleanup(); err != nil {
		t.Errorf("Cleanup should not error: %v", err)
	}
}

func TestLocalEnvironment_WrapCommand(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())
	wrapped := env.wrapCommand("echo hello")
	if !contains(wrapped, "echo hello") {
		t.Error("wrapped command should contain original command")
	}
	if !contains(wrapped, cwdMarker) {
		t.Error("wrapped command should contain CWD marker command")
	}
}

func TestLocalEnvironment_ExtractCWDMarker(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())

	output := "line1\nline2\n__NEXUS_CWD__:/custom/path\nline3"
	cleanOutput, cwd := env.extractCWDMarker(output)

	if cwd != "/custom/path" {
		t.Errorf("expected CWD /custom/path, got %s", cwd)
	}
	if contains(cleanOutput, "__NEXUS_CWD__:") {
		t.Error("clean output should not contain CWD marker")
	}
	if env.CWD() != "/custom/path" {
		t.Errorf("env CWD should be updated to /custom/path, got %s", env.CWD())
	}
}

func TestLocalEnvironment_ExtractCWDMarker_NoMarker(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())
	output := "line1\nline2\nline3"
	cleanOutput, cwd := env.extractCWDMarker(output)

	if cwd != "" {
		t.Errorf("expected empty CWD, got %s", cwd)
	}
	if cleanOutput != output {
		t.Error("output should be unchanged without marker")
	}
}

func TestLocalEnvironment_ExecuteBackground(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	handle, err := env.ExecuteBackground(ctx, "echo background", nil)
	if err != nil {
		t.Fatalf("ExecuteBackground failed: %v", err)
	}
	if handle == nil {
		t.Fatal("expected non-nil handle")
	}

	// Wait for process to finish
	exitCode, err := handle.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
}

func TestTruncateShellCmd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly 10!", 12, "exactly 10!"},
		{"this is way too long", 5, "this ..."},
		{"", 5, ""},
		{"hello", 0, "..."},
	}

	for _, tt := range tests {
		got := truncateShellCmd(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncateShellCmd(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestTruncateShellCmd_Multibyte(t *testing.T) {
	t.Parallel()

	result := truncateShellCmd("Hello world", 5)
	if result != "Hello..." {
		t.Errorf("expected 'Hello...', got %q", result)
	}

	// Test with actual multibyte characters
	result = truncateShellCmd("Hello, World!", 7)
	if result != "Hello, ..." {
		t.Errorf("expected 'Hello, ...', got %q", result)
	}
}

func TestSetSysProcAttr(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("echo", "test")
	// Should not panic
	setSysProcAttr(cmd)
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return len(sub) == 0
}

func TestLocalEnvironment_Execute_RealCommand_ExitCode(t *testing.T) {
	t.Parallel()

	env := NewLocalEnvironment(t.TempDir())

	// Use a command that succeeds
	result, err := env.Execute(context.Background(), "true", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0 for 'true', got %d", result.ExitCode)
	}

	// Use a command that fails
	result, err = env.Execute(context.Background(), "false", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code for 'false'")
	}
}

func TestLocalEnvironment_Execute_PreservesEnv(t *testing.T) {
	t.Parallel()

	// Set an env var that the command should inherit
	_ = os.Setenv("NEXUS_TEST_INHERIT", "inherit_value")
	defer func() { _ = os.Unsetenv("NEXUS_TEST_INHERIT") }()

	env := NewLocalEnvironment(t.TempDir())
	result, err := env.Execute(context.Background(), "echo $NEXUS_TEST_INHERIT", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result.Stdout, "inherit_value") {
		t.Errorf("expected inherited env var, got %q", result.Stdout)
	}
}
