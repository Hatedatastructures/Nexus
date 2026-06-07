package context

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// DiscoverHints
// ---------------------------------------------------------------------------

func TestDiscoverHints(t *testing.T) {
	t.Parallel()

	t.Run("empty dir returns nil", func(t *testing.T) {
		t.Parallel()
		ht := NewHintTracker()
		got := ht.DiscoverHints("")
		if got != nil {
			t.Errorf("expected nil for empty dir, got %v", got)
		}
	})

	t.Run("finds AGENTS.md in directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agent hints"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got := ht.DiscoverHints(dir)
		if len(got) != 1 {
			t.Fatalf("expected 1 hint, got %d", len(got))
		}
		if got[0] != "agent hints" {
			t.Errorf("got %q, want %q", got[0], "agent hints")
		}
	})

	t.Run("finds CLAUDE.md in directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("claude hints"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got := ht.DiscoverHints(dir)
		if len(got) != 1 {
			t.Fatalf("expected 1 hint, got %d", len(got))
		}
	})

	t.Run("finds .cursorrules in directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".cursorrules"), []byte("cursor rules"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got := ht.DiscoverHints(dir)
		if len(got) != 1 {
			t.Fatalf("expected 1 hint, got %d", len(got))
		}
	})

	t.Run("finds .clinerules in directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".clinerules"), []byte("cline rules"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got := ht.DiscoverHints(dir)
		if len(got) != 1 {
			t.Fatalf("expected 1 hint, got %d", len(got))
		}
	})

	t.Run("finds hints in ancestor directory", func(t *testing.T) {
		t.Parallel()
		parent := t.TempDir()
		child := filepath.Join(parent, "sub1", "sub2")
		if err := os.MkdirAll(child, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("parent hints"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got := ht.DiscoverHints(child)
		if len(got) < 1 {
			t.Fatalf("expected at least 1 hint from ancestor, got %d", len(got))
		}
		found := false
		for _, h := range got {
			if h == "parent hints" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected to find parent hints")
		}
	})

	t.Run("does not revisit already visited directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("first call"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got1 := ht.DiscoverHints(dir)
		if len(got1) != 1 {
			t.Fatalf("first call: expected 1, got %d", len(got1))
		}
		got2 := ht.DiscoverHints(dir)
		if len(got2) != 0 {
			t.Errorf("second call: expected 0 (already visited), got %d", len(got2))
		}
	})

	t.Run("skips empty hint files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(""), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got := ht.DiscoverHints(dir)
		if len(got) != 0 {
			t.Errorf("expected 0 hints for empty file, got %d", len(got))
		}
	})

	t.Run("finds multiple hint files in same directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("claude"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got := ht.DiscoverHints(dir)
		if len(got) != 2 {
			t.Errorf("expected 2 hints, got %d", len(got))
		}
	})

	t.Run("deduplicates across calls", func(t *testing.T) {
		t.Parallel()
		parent := t.TempDir()
		child := filepath.Join(parent, "sub")
		if err := os.MkdirAll(child, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("parent hint"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got1 := ht.DiscoverHints(child)
		if len(got1) < 1 {
			t.Fatalf("expected at least 1 from child, got %d", len(got1))
		}
		got2 := ht.DiscoverHints(parent)
		if len(got2) != 0 {
			t.Errorf("expected 0 (parent already visited), got %d", len(got2))
		}
	})

	t.Run("stops at maxHintAncestors levels", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		deep := base
		for i := 0; i < 7; i++ {
			deep = filepath.Join(deep, "d")
			if err := os.MkdirAll(deep, 0755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(base, "AGENTS.md"), []byte("deep hint"), 0644); err != nil {
			t.Fatal(err)
		}
		ht := NewHintTracker()
		got := ht.DiscoverHints(deep)
		for _, h := range got {
			if h == "deep hint" {
				t.Error("should not find hint beyond maxHintAncestors levels")
			}
		}
	})
}
