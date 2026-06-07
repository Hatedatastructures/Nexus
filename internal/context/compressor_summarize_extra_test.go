package context

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// redactSensitiveData
// ---------------------------------------------------------------------------

func TestRedactSensitiveData(t *testing.T) {
	t.Parallel()

	t.Run("redacts API key", func(t *testing.T) {
		t.Parallel()
		input := `api_key=sk-abc123def456ghi789jkl012mno`
		got := redactSensitiveData(input)
		if strings.Contains(got, "sk-abc123def456ghi789jkl012mno") {
			t.Error("API key should be redacted")
		}
	})

	t.Run("redacts password", func(t *testing.T) {
		t.Parallel()
		input := `password=mysecret123 host=db.example.com`
		got := redactSensitiveData(input)
		if strings.Contains(got, "mysecret123") {
			t.Error("password should be redacted")
		}
	})

	t.Run("redacts bearer token", func(t *testing.T) {
		t.Parallel()
		input := `token=abc123tokenxyz`
		got := redactSensitiveData(input)
		if strings.Contains(got, "abc123tokenxyz") {
			t.Error("bearer token should be redacted")
		}
	})

	t.Run("redacts OpenAI-style key", func(t *testing.T) {
		t.Parallel()
		input := `key=sk-abcdefghijklmnopqrstuvwxyz123456`
		got := redactSensitiveData(input)
		if !strings.Contains(got, "[REDACTED]") {
			t.Error("OpenAI key should be redacted")
		}
	})

	t.Run("redacts JWT pattern", func(t *testing.T) {
		t.Parallel()
		input := `token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0`
		got := redactSensitiveData(input)
		if !strings.Contains(got, "[REDACTED]") {
			t.Error("JWT should be redacted")
		}
	})

	t.Run("non-sensitive data unchanged", func(t *testing.T) {
		t.Parallel()
		input := `{"command":"go test ./...","path":"main.go"}`
		got := redactSensitiveData(input)
		if got != input {
			t.Errorf("non-sensitive data should be unchanged, got %q", got)
		}
	})

	t.Run("redacts connection string", func(t *testing.T) {
		t.Parallel()
		input := `connection_string=postgres://user:pass@host/db`
		got := redactSensitiveData(input)
		if strings.Contains(got, "postgres://user:pass") {
			t.Error("connection string should be redacted")
		}
	})

	t.Run("empty input returns empty", func(t *testing.T) {
		t.Parallel()
		got := redactSensitiveData("")
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

// ---------------------------------------------------------------------------
// truncateStr
// ---------------------------------------------------------------------------

func TestTruncateStr(t *testing.T) {
	t.Parallel()

	t.Run("short string unchanged", func(t *testing.T) {
		t.Parallel()
		got := truncateStr("hello", 10)
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("long string truncated", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("a", 200)
		got := truncateStr(long, 50)
		if len(got) > 53 {
			t.Errorf("truncated string too long: %d chars", len(got))
		}
		if !strings.HasSuffix(got, "...") {
			t.Errorf("truncated string should end with '...', got %q", got)
		}
	})

	t.Run("exact length not truncated", func(t *testing.T) {
		t.Parallel()
		s := "hello"
		got := truncateStr(s, 5)
		if got != s {
			t.Errorf("got %q, want %q", got, s)
		}
	})

	t.Run("newlines replaced with spaces", func(t *testing.T) {
		t.Parallel()
		got := truncateStr("line1\nline2\nline3", 50)
		if strings.Contains(got, "\n") {
			t.Error("newlines should be replaced with spaces")
		}
	})

	t.Run("trims whitespace", func(t *testing.T) {
		t.Parallel()
		got := truncateStr("  hello  ", 10)
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})
}

// ---------------------------------------------------------------------------
// buildSummaryPrompt
// ---------------------------------------------------------------------------

func TestBuildSummaryPrompt(t *testing.T) {
	t.Parallel()

	t.Run("first time summary includes template", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		got := c.buildSummaryPrompt("content here", "", 3000, "")
		if !strings.Contains(got, "消息统计") {
			t.Error("prompt should contain template section")
		}
		if !strings.Contains(got, "content here") {
			t.Error("prompt should contain content")
		}
	})

	t.Run("iterative update includes previous summary", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		got := c.buildSummaryPrompt("new content", "previous summary text", 3000, "")
		if !strings.Contains(got, "previous summary text") {
			t.Error("prompt should contain previous summary")
		}
		if !strings.Contains(got, "new content") {
			t.Error("prompt should contain new content")
		}
	})

	t.Run("focus topic included when provided", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		got := c.buildSummaryPrompt("content", "", 3000, "authentication")
		if !strings.Contains(got, "authentication") {
			t.Error("prompt should contain focus topic")
		}
	})

	t.Run("custom template used when set", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.SummaryTemplate = "## Custom Section\n[custom content]"
		got := c.buildSummaryPrompt("content", "", 3000, "")
		if !strings.Contains(got, "Custom Section") {
			t.Error("prompt should use custom template")
		}
		if strings.Contains(got, "消息统计") {
			t.Error("prompt should not contain default template when custom is set")
		}
	})
}
