package gateway

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ChunkMessage
// ---------------------------------------------------------------------------

func TestChunkMessage(t *testing.T) {
	t.Parallel()

	t.Run("short text returns single chunk", func(t *testing.T) {
		t.Parallel()
		got := ChunkMessage("hello", 100)
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("got %v, want [hello]", got)
		}
	})

	t.Run("zero maxLen uses default", func(t *testing.T) {
		t.Parallel()
		got := ChunkMessage("hello", 0)
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("got %v, want [hello]", got)
		}
	})

	t.Run("negative maxLen uses default", func(t *testing.T) {
		t.Parallel()
		got := ChunkMessage("hello", -5)
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("got %v, want [hello]", got)
		}
	})

	t.Run("splits ASCII text", func(t *testing.T) {
		t.Parallel()
		text := "abcdefghij"
		got := ChunkMessage(text, 5)
		if len(got) < 2 {
			t.Fatalf("expected multiple chunks, got %d", len(got))
		}
		// Verify all chunks together contain the original text (minus headers)
		var combined strings.Builder
		for _, c := range got {
			// Strip header like [1/3]
			idx := strings.Index(c, "] ")
			if idx >= 0 {
				combined.WriteString(c[idx+2:])
			} else {
				combined.WriteString(c)
			}
		}
		if combined.String() != text {
			t.Errorf("combined = %q, want %q", combined.String(), text)
		}
	})

	t.Run("handles emoji correctly", func(t *testing.T) {
		t.Parallel()
		// Each emoji is 2 UTF-16 code units
		text := "😀😃😄😁😆😅"
		got := ChunkMessage(text, 5)
		if len(got) < 2 {
			t.Errorf("expected multiple chunks for emoji, got %d", len(got))
		}
	})

	t.Run("exact fit returns single chunk", func(t *testing.T) {
		t.Parallel()
		text := "hello"
		got := ChunkMessage(text, len(text))
		if len(got) != 1 {
			t.Errorf("expected 1 chunk for exact fit, got %d", len(got))
		}
	})

	t.Run("empty string returns single chunk", func(t *testing.T) {
		t.Parallel()
		got := ChunkMessage("", 100)
		if len(got) != 1 || got[0] != "" {
			t.Errorf("got %v, want empty string in slice", got)
		}
	})

	t.Run("sequential headers", func(t *testing.T) {
		t.Parallel()
		text := strings.Repeat("a", 20)
		got := ChunkMessage(text, 5)
		for i, c := range got {
			expected := strings.HasPrefix(c, "[")
			if !expected {
				t.Errorf("chunk %d missing header: %q", i, c)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// ChunkMessageSimple
// ---------------------------------------------------------------------------

func TestChunkMessageSimple(t *testing.T) {
	t.Parallel()

	t.Run("short text returns single chunk", func(t *testing.T) {
		t.Parallel()
		got := ChunkMessageSimple("hello", 100)
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("got %v, want [hello]", got)
		}
	})

	t.Run("zero maxLen uses default", func(t *testing.T) {
		t.Parallel()
		got := ChunkMessageSimple("hello", 0)
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("got %v, want [hello]", got)
		}
	})

	t.Run("splits long text by rune", func(t *testing.T) {
		t.Parallel()
		text := "你好世界测试文本"
		got := ChunkMessageSimple(text, 3)
		if len(got) < 2 {
			t.Errorf("expected multiple chunks, got %d", len(got))
		}
	})

	t.Run("chunks have headers", func(t *testing.T) {
		t.Parallel()
		text := strings.Repeat("x", 20)
		got := ChunkMessageSimple(text, 5)
		for i, c := range got {
			if !strings.HasPrefix(c, "[") {
				t.Errorf("chunk %d missing header: %q", i, c)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// 截取UTF16
// ---------------------------------------------------------------------------

func TestTruncateUTF16(t *testing.T) {
	t.Parallel()

	t.Run("zero maxUnits returns empty", func(t *testing.T) {
		t.Parallel()
		got := 截取UTF16("hello", 0)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("negative maxUnits returns empty", func(t *testing.T) {
		t.Parallel()
		got := 截取UTF16("hello", -1)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("short string returned as-is", func(t *testing.T) {
		t.Parallel()
		got := 截取UTF16("hello", 100)
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("truncates ASCII", func(t *testing.T) {
		t.Parallel()
		got := 截取UTF16("hello", 3)
		if got != "hel" {
			t.Errorf("got %q, want %q", got, "hel")
		}
	})

	t.Run("truncates emoji at code unit boundary", func(t *testing.T) {
		t.Parallel()
		// Each emoji is 2 UTF-16 code units
		got := 截取UTF16("😀😃😄", 3)
		// 3 code units = 1 emoji (2 units) + 1 unit from surrogate pair → replacement char
		if got != "😀�" {
			t.Errorf("got %q, want %q", got, "😀�")
		}
	})

	t.Run("exact boundary", func(t *testing.T) {
		t.Parallel()
		got := 截取UTF16("😀😃", 4)
		if got != "😀😃" {
			t.Errorf("got %q, want %q", got, "😀😃")
		}
	})

	t.Run("empty string", func(t *testing.T) {
		t.Parallel()
		got := 截取UTF16("", 10)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("CJK characters", func(t *testing.T) {
		t.Parallel()
		// CJK chars are 1 UTF-16 code unit each
		got := 截取UTF16("你好世界", 2)
		if got != "你好" {
			t.Errorf("got %q, want %q", got, "你好")
		}
	})
}

// ---------------------------------------------------------------------------
// UTF16Len
// ---------------------------------------------------------------------------

func TestUTF16Len(t *testing.T) {
	t.Parallel()

	t.Run("empty string", func(t *testing.T) {
		t.Parallel()
		if got := UTF16Len(""); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})

	t.Run("ASCII", func(t *testing.T) {
		t.Parallel()
		if got := UTF16Len("abc"); got != 3 {
			t.Errorf("got %d, want 3", got)
		}
	})

	t.Run("emoji is 2 code units", func(t *testing.T) {
		t.Parallel()
		if got := UTF16Len("😀"); got != 2 {
			t.Errorf("got %d, want 2", got)
		}
	})

	t.Run("mixed content", func(t *testing.T) {
		t.Parallel()
		// "a" = 1, "😀" = 2, "b" = 1 → total 4
		if got := UTF16Len("a😀b"); got != 4 {
			t.Errorf("got %d, want 4", got)
		}
	})

	t.Run("CJK", func(t *testing.T) {
		t.Parallel()
		if got := UTF16Len("你好"); got != 2 {
			t.Errorf("got %d, want 2", got)
		}
	})
}

// ---------------------------------------------------------------------------
// TruncateMessage
// ---------------------------------------------------------------------------

func TestTruncateMessage(t *testing.T) {
	t.Parallel()

	t.Run("short text returned as-is", func(t *testing.T) {
		t.Parallel()
		got := TruncateMessage("hello", 100)
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("zero maxLen uses default", func(t *testing.T) {
		t.Parallel()
		got := TruncateMessage("hello", 0)
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("truncates and adds suffix", func(t *testing.T) {
		t.Parallel()
		text := strings.Repeat("a", 100)
		got := TruncateMessage(text, 50)
		if !strings.HasSuffix(got, "...[消息已截断]") {
			t.Errorf("expected truncation suffix, got %q", got)
		}
	})

	t.Run("exact length no truncation", func(t *testing.T) {
		t.Parallel()
		text := "hello"
		got := TruncateMessage(text, len(text))
		if got != text {
			t.Errorf("got %q, want %q", got, text)
		}
	})

	t.Run("very small maxLen returns suffix only", func(t *testing.T) {
		t.Parallel()
		got := TruncateMessage("hello world", 1)
		// suffix doesn't fit, returns just suffix
		if got != "...[消息已截断]" && !strings.Contains(got, "...[消息已截断]") {
			t.Errorf("expected truncation suffix, got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// SplitAtNewlines
// ---------------------------------------------------------------------------

func TestSplitAtNewlines(t *testing.T) {
	t.Parallel()

	t.Run("short text returns single chunk", func(t *testing.T) {
		t.Parallel()
		got := SplitAtNewlines("hello", 100)
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("got %v, want [hello]", got)
		}
	})

	t.Run("zero maxLen uses default", func(t *testing.T) {
		t.Parallel()
		got := SplitAtNewlines("hello", 0)
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("got %v, want [hello]", got)
		}
	})

	t.Run("splits at newline boundaries", func(t *testing.T) {
		t.Parallel()
		text := "line1\nline2\nline3\nline4"
		got := SplitAtNewlines(text, 12)
		if len(got) < 2 {
			t.Errorf("expected multiple chunks, got %d: %v", len(got), got)
		}
	})

	t.Run("long line gets hard-split", func(t *testing.T) {
		t.Parallel()
		longLine := strings.Repeat("a", 100)
		text := longLine + "\nshort"
		got := SplitAtNewlines(text, 30)
		if len(got) < 2 {
			t.Errorf("expected multiple chunks for long line, got %d", len(got))
		}
	})

	t.Run("preserves content", func(t *testing.T) {
		t.Parallel()
		text := "line1\nline2\nline3"
		got := SplitAtNewlines(text, 100)
		if len(got) != 1 {
			t.Fatalf("expected 1 chunk, got %d", len(got))
		}
		if got[0] != text {
			t.Errorf("got %q, want %q", got[0], text)
		}
	})

	t.Run("empty string returns empty slice", func(t *testing.T) {
		t.Parallel()
		got := SplitAtNewlines("", 100)
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %v", got)
		}
	})
}
