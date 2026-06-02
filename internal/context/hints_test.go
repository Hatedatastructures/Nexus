package context

import (
	"os"
	"path/filepath"
	"sort"
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
// ExtractPathsFromArgs
// ---------------------------------------------------------------------------

func TestExtractPathsFromArgs(t *testing.T) {
	t.Parallel()

	t.Run("empty map returns nil", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("extracts path key", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(map[string]any{"path": "/tmp/file.go"})
		if len(got) != 1 || got[0] != "/tmp/file.go" {
			t.Errorf("expected [/tmp/file.go], got %v", got)
		}
	})

	t.Run("extracts file key", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(map[string]any{"file": "config.yaml"})
		if len(got) != 1 || got[0] != "config.yaml" {
			t.Errorf("expected [config.yaml], got %v", got)
		}
	})

	t.Run("extracts directory key", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(map[string]any{"directory": "/home/user"})
		if len(got) != 1 || got[0] != "/home/user" {
			t.Errorf("expected [/home/user], got %v", got)
		}
	})

	t.Run("extracts dir key", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(map[string]any{"dir": "/opt"})
		if len(got) != 1 || got[0] != "/opt" {
			t.Errorf("expected [/opt], got %v", got)
		}
	})

	t.Run("extracts workdir key", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(map[string]any{"workdir": "/project"})
		if len(got) != 1 || got[0] != "/project" {
			t.Errorf("expected [/project], got %v", got)
		}
	})

	t.Run("extracts cwd key", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(map[string]any{"cwd": "/current"})
		if len(got) != 1 || got[0] != "/current" {
			t.Errorf("expected [/current], got %v", got)
		}
	})

	t.Run("extracts filename key", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(map[string]any{"filename": "output.txt"})
		if len(got) != 1 || got[0] != "output.txt" {
			t.Errorf("expected [output.txt], got %v", got)
		}
	})

	t.Run("extracts target key", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(map[string]any{"target": "/dest"})
		if len(got) != 1 || got[0] != "/dest" {
			t.Errorf("expected [/dest], got %v", got)
		}
	})

	t.Run("skips empty string values", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(map[string]any{"path": ""})
		if len(got) != 0 {
			t.Errorf("expected 0 paths, got %d", len(got))
		}
	})

	t.Run("skips non-string values", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(map[string]any{"path": 42})
		if len(got) != 0 {
			t.Errorf("expected 0 paths for non-string, got %d", len(got))
		}
	})

	t.Run("extracts paths from command", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(map[string]any{"command": "cat /etc/hosts"})
		if len(got) == 0 {
			t.Error("expected paths from command")
		}
		found := false
		for _, p := range got {
			if p == "/etc/hosts" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected /etc/hosts in results, got %v", got)
		}
	})

	t.Run("extracts multiple keys", func(t *testing.T) {
		t.Parallel()
		got := ExtractPathsFromArgs(map[string]any{
			"path": "/tmp/a.go",
			"file": "/tmp/b.go",
		})
		if len(got) != 2 {
			t.Errorf("expected 2 paths, got %d: %v", len(got), got)
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

// ---------------------------------------------------------------------------
// extractPathsFromCommand
// ---------------------------------------------------------------------------

func TestExtractPathsFromCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  string
		want []string
	}{
		{"empty string", "", nil},
		{"simple path", "cat /etc/hosts", []string{"/etc/hosts"}},
		{"windows path", "type C:\\Users\\file.txt", []string{"C:\\Users\\file.txt"}},
		{"dotted file", "cat config.json", []string{"config.json"}},
		{"skips flags", "ls -la /tmp", []string{"/tmp"}},
		{"skips pipe", "cat file.txt | grep foo", []string{"file.txt"}},
		{"skips redirect", "echo hi > output.txt", []string{"output.txt"}},
		{"skips double redirect", "echo hi >> output.txt", []string{"output.txt"}},
		{"skips http urls", "curl http://example.com", []string{}},
		{"skips short args", "ls -a", nil},
		{"multiple paths", "cp /tmp/a.go /tmp/b.go", []string{"/tmp/a.go", "/tmp/b.go"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractPathsFromCommand(tc.cmd)
			if tc.want == nil && len(got) != 0 {
				t.Errorf("expected empty, got %v", got)
			}
			if tc.want != nil {
				if len(got) != len(tc.want) {
					t.Errorf("expected %v, got %v", tc.want, got)
					return
				}
				for i, w := range tc.want {
					if got[i] != w {
						t.Errorf("got[%d] = %q, want %q", i, got[i], w)
					}
				}
			}
		})
	}
}
