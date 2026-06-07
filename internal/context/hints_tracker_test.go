package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// NewHintTracker
// ---------------------------------------------------------------------------

func TestNewHintTracker(t *testing.T) {
	t.Parallel()

	t.Run("creates initialized tracker", func(t *testing.T) {
		t.Parallel()
		ht := NewHintTracker()
		if ht == nil {
			t.Fatal("expected non-nil tracker")
		}
		if ht.visited == nil {
			t.Error("visited map should be initialized")
		}
		if ht.hints == nil {
			t.Error("hints map should be initialized")
		}
		if len(ht.visited) != 0 {
			t.Errorf("visited should be empty, got %d entries", len(ht.visited))
		}
		if len(ht.hints) != 0 {
			t.Errorf("hints should be empty, got %d entries", len(ht.hints))
		}
	})
}

// ---------------------------------------------------------------------------
// readHintFile
// ---------------------------------------------------------------------------

func TestReadHintFile(t *testing.T) {
	t.Parallel()

	t.Run("reads existing file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.md")
		content := "hello world"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := readHintFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != content {
			t.Errorf("got %q, want %q", got, content)
		}
	})

	t.Run("trims whitespace", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "ws.md")
		if err := os.WriteFile(path, []byte("  hello  \n"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := readHintFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("truncates large file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "big.md")
		largeContent := strings.Repeat("x", maxHintFileChars+1000)
		if err := os.WriteFile(path, []byte(largeContent), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := readHintFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) > maxHintFileChars {
			t.Errorf("content should be truncated to %d chars, got %d", maxHintFileChars, len(got))
		}
	})

	t.Run("non-existent file returns error", func(t *testing.T) {
		t.Parallel()
		_, err := readHintFile("/nonexistent/path/file.md")
		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})

	t.Run("exact maxHintFileChars not truncated", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "exact.md")
		content := strings.Repeat("a", maxHintFileChars)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := readHintFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != maxHintFileChars {
			t.Errorf("got %d chars, want %d", len(got), maxHintFileChars)
		}
	})
}
