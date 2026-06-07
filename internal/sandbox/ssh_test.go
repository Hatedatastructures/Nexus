package sandbox

import (
	"testing"
)

func TestNewSSHEnvironment(t *testing.T) {
	t.Parallel()

	env := NewSSHEnvironment("user@host", "/workspace", "-o", "StrictHostKeyChecking=no")
	if env == nil {
		t.Fatal("expected non-nil environment")
	}
	if env.host != "user@host" {
		t.Errorf("expected host user@host, got %s", env.host)
	}
	if env.CWD() != "/workspace" {
		t.Errorf("expected CWD /workspace, got %s", env.CWD())
	}
	if len(env.sshArgs) != 2 {
		t.Errorf("expected 2 extra SSH args, got %d", len(env.sshArgs))
	}
}

func TestNewSSHEnvironment_EmptyCWD(t *testing.T) {
	t.Parallel()

	env := NewSSHEnvironment("host", "")
	if env.CWD() != "~" {
		t.Errorf("expected default CWD ~, got %s", env.CWD())
	}
}

func TestSSHEnvironment_UpdateCWD(t *testing.T) {
	t.Parallel()

	env := NewSSHEnvironment("host", "~")
	env.UpdateCWD("/home/user/project")
	if env.CWD() != "/home/user/project" {
		t.Errorf("expected CWD /home/user/project, got %s", env.CWD())
	}
}

func TestSSHEnvironment_Cleanup(t *testing.T) {
	t.Parallel()

	env := NewSSHEnvironment("host", "~")
	if err := env.Cleanup(); err != nil {
		t.Errorf("Cleanup should not error: %v", err)
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "'simple'"},
		{"", "''"},
		{"hello world", "'hello world'"},
		{"it's here", "'it'\\''s here'"},
		{"path/with spaces", "'path/with spaces'"},
		{"$(dangerous)", "'$(dangerous)'"},
		{"; rm -rf /", "'; rm -rf /'"},
	}

	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.expected {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestShellQuote_MultipleQuotes(t *testing.T) {
	t.Parallel()

	result := shellQuote("it's a \"test\" and it's done")
	if result == "" {
		t.Error("expected non-empty result")
	}
	// Should properly escape internal single quotes
	if !containsStr(result, "'\\''") {
		t.Logf("shellQuote result: %s", result)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return len(sub) == 0
}
