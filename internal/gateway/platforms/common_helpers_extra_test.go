package platforms

import (
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// getEnvList
// ---------------------------------------------------------------------------

func TestGetEnvList(t *testing.T) {
	// Do NOT use t.Parallel() here: os.Setenv is process-wide and not safe
	// under parallel sub-tests.

	t.Run("unset env returns nil", func(t *testing.T) {
		got := getEnvList("NEXUS_TEST_UNSET_ENV_VAR_12345")
		if got != nil {
			t.Errorf("expected nil for unset env, got %v", got)
		}
	})

	t.Run("empty env returns nil", func(t *testing.T) {
		_ = os.Setenv("NEXUS_TEST_ENV_LIST", "")
		defer func() { _ = os.Unsetenv("NEXUS_TEST_ENV_LIST") }()
		got := getEnvList("NEXUS_TEST_ENV_LIST")
		if got != nil {
			t.Errorf("expected nil for empty env, got %v", got)
		}
	})

	t.Run("single value", func(t *testing.T) {
		_ = os.Setenv("NEXUS_TEST_ENV_LIST", "hello")
		defer func() { _ = os.Unsetenv("NEXUS_TEST_ENV_LIST") }()
		got := getEnvList("NEXUS_TEST_ENV_LIST")
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("getEnvList() = %v, want [hello]", got)
		}
	})

	t.Run("comma separated", func(t *testing.T) {
		_ = os.Setenv("NEXUS_TEST_ENV_LIST", "a, b, c")
		defer func() { _ = os.Unsetenv("NEXUS_TEST_ENV_LIST") }()
		got := getEnvList("NEXUS_TEST_ENV_LIST")
		if len(got) != 3 {
			t.Fatalf("length = %d, want 3", len(got))
		}
		want := []string{"a", "b", "c"}
		for i, w := range want {
			if got[i] != w {
				t.Errorf("got[%d] = %q, want %q", i, got[i], w)
			}
		}
	})

	t.Run("trailing comma", func(t *testing.T) {
		_ = os.Setenv("NEXUS_TEST_ENV_LIST", "a,b,")
		defer func() { _ = os.Unsetenv("NEXUS_TEST_ENV_LIST") }()
		got := getEnvList("NEXUS_TEST_ENV_LIST")
		if len(got) != 2 {
			t.Errorf("expected 2 items with trailing comma, got %d: %v", len(got), got)
		}
	})

	t.Run("whitespace only items are skipped", func(t *testing.T) {
		_ = os.Setenv("NEXUS_TEST_ENV_LIST", "  ,  ,  ")
		defer func() { _ = os.Unsetenv("NEXUS_TEST_ENV_LIST") }()
		got := getEnvList("NEXUS_TEST_ENV_LIST")
		if len(got) != 0 {
			t.Errorf("expected 0 items for whitespace-only, got %d: %v", len(got), got)
		}
	})
}

// ---------------------------------------------------------------------------
// formatInt
// ---------------------------------------------------------------------------

func TestFormatInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{-1, "-1"},
		{999999, "999999"},
	}

	for _, tc := range tests {
		t.Run(string(rune('0'+tc.input%10)), func(t *testing.T) {
			t.Parallel()
			got := formatInt(tc.input)
			if got != tc.expected {
				t.Errorf("formatInt(%d) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseInt
// ---------------------------------------------------------------------------

func TestParseInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected int
		hasErr   bool
	}{
		{"0", 0, false},
		{"42", 42, false},
		{"  7  ", 7, false},
		{"abc", 0, true},
		{"12abc", 0, true},
		{"", 0, false}, // TrimSpace("") == "" -> loop never runs -> returns 0, nil
		{"-1", 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got, err := parseInt(tc.input)
			if tc.hasErr && err == nil {
				t.Errorf("parseInt(%q) expected error, got nil", tc.input)
			}
			if !tc.hasErr && err != nil {
				t.Errorf("parseInt(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.expected {
				t.Errorf("parseInt(%q) = %d, want %d", tc.input, got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isLoopback
// ---------------------------------------------------------------------------

func TestIsLoopback(t *testing.T) {
	t.Parallel()

	// isLoopback strips the leading ":" from addr, so "127.0.0.1:8080"
	// becomes "127.0.0.1:8080" (no stripping of port). The function only
	// does TrimPrefix(addr, ":"), which removes a leading colon.
	// Inputs that start with ":" like ":8080" become "8080".
	tests := []struct {
		addr string
		want bool
	}{
		{":8080", false}, // TrimPrefix(":8080", ":") -> "8080" -> not loopback
		{"0.0.0.0:8080", false},
		{"127.0.0.1", true},
		{"localhost", true},
		{"::1", false}, // TrimPrefix("::1", ":") -> ":1" -> not loopback
		{"192.168.1.1:8080", false},
	}

	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			t.Parallel()
			got := isLoopback(tc.addr)
			if got != tc.want {
				t.Errorf("isLoopback(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// maxAPIResponseSize
// ---------------------------------------------------------------------------

func TestMaxAPIResponseSize(t *testing.T) {
	t.Parallel()

	if maxAPIResponseSize != 10<<20 {
		t.Errorf("maxAPIResponseSize = %d, want %d", maxAPIResponseSize, 10<<20)
	}
}

// ---------------------------------------------------------------------------
// Strings helper (SanitizeInput is not present, skip)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Verify adapter base URL construction
// ---------------------------------------------------------------------------

func TestTelegramBaseURL(t *testing.T) {
	t.Parallel()

	a := NewTelegramAdapter("123:ABC")
	if !strings.Contains(a.baseURL, "123:ABC") {
		t.Errorf("baseURL = %q, should contain token", a.baseURL)
	}
	if !strings.HasPrefix(a.baseURL, "https://api.telegram.org/bot") {
		t.Errorf("baseURL = %q, should have correct prefix", a.baseURL)
	}
}

func TestDiscordBaseURL(t *testing.T) {
	t.Parallel()

	a := NewDiscordAdapter("token")
	if a.baseURL != "https://discord.com/api/v10" {
		t.Errorf("baseURL = %q, want %q", a.baseURL, "https://discord.com/api/v10")
	}
}

func TestSlackBaseURL(t *testing.T) {
	t.Parallel()

	a := NewSlackAdapter("xoxb-test", "xapp-test")
	if a.baseURL != "https://slack.com/api" {
		t.Errorf("baseURL = %q, want %q", a.baseURL, "https://slack.com/api")
	}
}
