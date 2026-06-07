package approval

import (
	"context"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// TestContainsShellMetacharacters is a small unit test for the helper.
// ---------------------------------------------------------------------------
func TestContainsShellMetacharacters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cmd  string
		want bool
	}{
		{"ls", false},
		{"ls; rm", true},
		{"ls | grep foo", true},
		{"echo `date`", true},
		{"echo $(date)", true},
		{"ls && echo done", true},
		{"ls || echo fail", true},
		{"echo $HOME", true},
		{"cat file.txt", false},
	}

	for _, tc := range tests {
		t.Run(tc.cmd, func(t *testing.T) {
			t.Parallel()
			got := containsShellMetacharacters(tc.cmd)
			if got != tc.want {
				t.Errorf("containsShellMetacharacters(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestResult_String verifies the Result.String() method.
// ---------------------------------------------------------------------------
func TestResult_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		r    Result
		want string
	}{
		{Approved, "Approved"},
		{Denied, "Denied"},
		{Pending, "Pending"},
		{Result(99), "Unknown"},
	}
	for _, tc := range tests {
		got := tc.r.String()
		if got != tc.want {
			t.Errorf("Result(%d).String() = %q, want %q", tc.r, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestNewChecker_DefaultMode verifies that an empty mode defaults to "smart".
// ---------------------------------------------------------------------------
func TestNewChecker_DefaultMode(t *testing.T) {
	t.Parallel()

	c := NewChecker("", nil, nil)
	if c.Mode() != "smart" {
		t.Errorf("Mode() = %q, want %q", c.Mode(), "smart")
	}
}

// ---------------------------------------------------------------------------
// TestChecker_SetMode verifies SetMode accepts valid modes and rejects invalid.
// ---------------------------------------------------------------------------
func TestChecker_SetMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode    string
		wantOK  bool
		wantVal string
	}{
		{"off", true, "off"},
		{"smart", true, "smart"},
		{"always", true, "always"},
		{"invalid", false, "smart"},
		{"", false, "smart"},
		{"OFF", false, "smart"},
	}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			// 每个 case 用独立的 Checker，避免状态污染
			c := NewChecker("smart", nil, nil)
			ok := c.SetMode(tc.mode)
			if ok != tc.wantOK {
				t.Errorf("SetMode(%q) = %v, want %v", tc.mode, ok, tc.wantOK)
			}
			if got := c.Mode(); got != tc.wantVal {
				t.Errorf("after SetMode(%q), Mode() = %q, want %q", tc.mode, got, tc.wantVal)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestChecker_Mode verifies Mode() returns the current mode.
// ---------------------------------------------------------------------------
func TestChecker_Mode(t *testing.T) {
	t.Parallel()

	c := NewChecker("always", nil, nil)
	if got := c.Mode(); got != "always" {
		t.Errorf("Mode() = %q, want %q", got, "always")
	}
}

// ---------------------------------------------------------------------------
// TestCheckTool verifies CheckTool delegates to Check for terminal tools
// and auto-approves non-terminal tools.
// ---------------------------------------------------------------------------
func TestCheckTool(t *testing.T) {
	t.Parallel()

	c := NewChecker("smart", nil, nil)
	ctx := context.Background()

	t.Run("non-terminal tool auto-approved", func(t *testing.T) {
		t.Parallel()
		result, _ := c.CheckTool(ctx, "read", map[string]any{"path": "/etc/passwd"})
		if result != Approved {
			t.Errorf("CheckTool(read) = %v, want Approved", result)
		}
	})

	t.Run("terminal tool delegates to Check", func(t *testing.T) {
		t.Parallel()
		result, _ := c.CheckTool(ctx, "terminal", map[string]any{"command": "ls"})
		if result != Approved {
			t.Errorf("CheckTool(terminal, ls) = %v, want Approved", result)
		}
	})

	t.Run("terminal tool dangerous command", func(t *testing.T) {
		t.Parallel()
		result, _ := c.CheckTool(ctx, "terminal", map[string]any{"command": "git push --force"})
		if result != Pending {
			t.Errorf("CheckTool(terminal, git push --force) = %v, want Pending", result)
		}
	})

	t.Run("terminal tool empty command", func(t *testing.T) {
		t.Parallel()
		result, _ := c.CheckTool(ctx, "terminal", map[string]any{"command": ""})
		if result != Approved {
			t.Errorf("CheckTool(terminal, empty) = %v, want Approved", result)
		}
	})

	t.Run("terminal tool missing command", func(t *testing.T) {
		t.Parallel()
		result, _ := c.CheckTool(ctx, "terminal", nil)
		if result != Approved {
			t.Errorf("CheckTool(terminal, nil) = %v, want Approved", result)
		}
	})
}

// ---------------------------------------------------------------------------
// TestTruncateForLog verifies the log truncation helper.
// ---------------------------------------------------------------------------
func TestTruncateForLog(t *testing.T) {
	t.Parallel()

	t.Run("short string unchanged", func(t *testing.T) {
		t.Parallel()
		got := truncateForLog("hello", 10)
		if got != "hello" {
			t.Errorf("truncateForLog(%q, 10) = %q, want %q", "hello", got, "hello")
		}
	})

	t.Run("long string truncated", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("a", 300)
		got := truncateForLog(long, 200)
		want := strings.Repeat("a", 200) + "..."
		if got != want {
			t.Errorf("truncateForLog(len=300, 200) len = %d, want %d", len(got), len(want))
		}
	})

	t.Run("exact length not truncated", func(t *testing.T) {
		t.Parallel()
		s := "hello"
		got := truncateForLog(s, 5)
		if got != s {
			t.Errorf("truncateForLog(exact) = %q, want %q", got, s)
		}
	})
}
