package sandbox

import (
	"testing"
	"time"
)

func TestExitCodeConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		code     int
		expected int
	}{
		{"success", ExitCodeSuccess, 0},
		{"general", ExitCodeGeneral, 1},
		{"timeout", ExitCodeTimeout, 124},
		{"signal_base", ExitCodeSignal, 128},
		{"sigterm", ExitCodeSIGTERM, 143},
		{"sigkill", ExitCodeSIGKILL, 137},
	}

	for _, tt := range tests {
		if tt.code != tt.expected {
			t.Errorf("%s: expected %d, got %d", tt.name, tt.expected, tt.code)
		}
	}
}

func TestExecuteOptions_Defaults(t *testing.T) {
	t.Parallel()

	opts := &ExecuteOptions{}
	if opts.CWD != "" {
		t.Error("default CWD should be empty")
	}
	if opts.Timeout != 0 {
		t.Error("default Timeout should be 0")
	}
	if opts.StdinData != "" {
		t.Error("default StdinData should be empty")
	}
	if opts.Login {
		t.Error("default Login should be false")
	}
	if opts.Sudo {
		t.Error("default Sudo should be false")
	}
}

func TestExecuteResult_Fields(t *testing.T) {
	t.Parallel()

	result := &ExecuteResult{
		Stdout:      "hello",
		Stderr:      "warning",
		ExitCode:    0,
		Duration:    100 * time.Millisecond,
		Interrupted: false,
		CWD:         "/home/user",
	}

	if result.Stdout != "hello" {
		t.Errorf("expected stdout 'hello', got %s", result.Stdout)
	}
	if result.Stderr != "warning" {
		t.Errorf("expected stderr 'warning', got %s", result.Stderr)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.Interrupted {
		t.Error("should not be interrupted")
	}
	if result.CWD != "/home/user" {
		t.Errorf("expected CWD /home/user, got %s", result.CWD)
	}
}
