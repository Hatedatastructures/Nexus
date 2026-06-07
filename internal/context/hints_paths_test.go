package context

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// ---------------------------------------------------------------------------
// DiscoverHintsFromPaths
// ---------------------------------------------------------------------------

func TestDiscoverHintsFromPaths(t *testing.T) {
	t.Parallel()

	t.Run("empty paths returns nil", func(t *testing.T) {
		t.Parallel()
		ht := NewHintTracker()
		got := ht.DiscoverHintsFromPaths(nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("discovers from file path", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		filePath := filepath.Join(dir, "main.go")
		if err := os.WriteFile(filePath, []byte("package main"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("file dir hints"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got := ht.DiscoverHintsFromPaths([]string{filePath})
		if len(got) != 1 {
			t.Fatalf("expected 1 hint, got %d", len(got))
		}
		if got[0] != "file dir hints" {
			t.Errorf("got %q, want %q", got[0], "file dir hints")
		}
	})

	t.Run("discovers from directory path", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("dir hints"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got := ht.DiscoverHintsFromPaths([]string{dir})
		if len(got) != 1 {
			t.Fatalf("expected 1 hint, got %d", len(got))
		}
	})

	t.Run("deduplicates across multiple paths", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("shared hint"), 0644); err != nil {
			t.Fatal(err)
		}
		file1 := filepath.Join(dir, "a.go")
		file2 := filepath.Join(dir, "b.go")
		if err := os.WriteFile(file1, []byte("a"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file2, []byte("b"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got := ht.DiscoverHintsFromPaths([]string{file1, file2})
		if len(got) != 1 {
			t.Errorf("expected 1 deduplicated hint, got %d", len(got))
		}
	})

	t.Run("non-existent path skipped", func(t *testing.T) {
		t.Parallel()
		ht := NewHintTracker()
		got := ht.DiscoverHintsFromPaths([]string{"/nonexistent/path/file.go"})
		if len(got) != 0 {
			t.Errorf("expected 0 hints for non-existent path, got %d", len(got))
		}
	})
}

// ---------------------------------------------------------------------------
// AllHints
// ---------------------------------------------------------------------------

func TestAllHints(t *testing.T) {
	t.Parallel()

	t.Run("empty tracker returns empty", func(t *testing.T) {
		t.Parallel()
		ht := NewHintTracker()
		got := ht.AllHints()
		if len(got) != 0 {
			t.Errorf("expected empty, got %d", len(got))
		}
	})

	t.Run("returns all discovered hints", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("hint a"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("hint b"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		ht.DiscoverHints(dir)
		got := ht.AllHints()
		if len(got) != 2 {
			t.Fatalf("expected 2 hints, got %d", len(got))
		}
		sort.Strings(got)
		if got[0] != "hint a" || got[1] != "hint b" {
			t.Errorf("expected [hint a, hint b], got %v", got)
		}
	})
}
