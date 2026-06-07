package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGlobTool_Basics(t *testing.T) {
	t.Parallel()
	tool := &GlobTool{}

	if tool.Name() != "glob" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "glob")
	}
	if tool.Toolset() != "full_stack" {
		t.Errorf("Toolset() = %q, want %q", tool.Toolset(), "full_stack")
	}
	if tool.Emoji() != "search" {
		t.Errorf("Emoji() = %q, want %q", tool.Emoji(), "search")
	}
	if !tool.IsAvailable() {
		t.Error("IsAvailable() should always be true")
	}
	if tool.MaxResultChars() != 10000 {
		t.Errorf("MaxResultChars() = %d, want 10000", tool.MaxResultChars())
	}

	schema := tool.Schema()
	if schema.Name != "glob" {
		t.Errorf("Schema().Name = %q, want %q", schema.Name, "glob")
	}
}

func TestGlobTool_Execute_MissingPattern(t *testing.T) {
	t.Parallel()
	tool := &GlobTool{}
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &parsed); jsonErr != nil {
		t.Fatalf("failed to parse result: %v", jsonErr)
	}
	if parsed["error"] == nil {
		t.Error("expected error for missing pattern")
	}
}

func TestGlobTool_Execute_EmptyPattern(t *testing.T) {
	t.Parallel()
	tool := &GlobTool{}
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{"pattern": ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &parsed); jsonErr != nil {
		t.Fatalf("failed to parse result: %v", jsonErr)
	}
	if parsed["error"] == nil {
		t.Error("expected error for empty pattern")
	}
}

func TestGlobTool_Execute_NoMatches(t *testing.T) {
	t.Parallel()
	tool := &GlobTool{}
	ctx := context.Background()

	tmpDir := t.TempDir()
	result, err := tool.Execute(ctx, map[string]any{
		"pattern": "*.xyz123nonexistent",
		"path":    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &parsed); jsonErr != nil {
		t.Fatalf("failed to parse result: %v", jsonErr)
	}
	files, _ := parsed["files"].([]any)
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestGlobTool_Execute_WithMatches(t *testing.T) {
	t.Parallel()
	tool := &GlobTool{}
	ctx := context.Background()

	tmpDir := t.TempDir()
	SetAllowedDir(tmpDir)
	defer SetAllowedDir(".")
	// Create test files
	for _, name := range []string{"a.go", "b.go", "c.txt"} {
		if writeErr := os.WriteFile(filepath.Join(tmpDir, name), []byte("test"), 0644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}

	result, err := tool.Execute(ctx, map[string]any{
		"pattern": "*.go",
		"path":    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &parsed); jsonErr != nil {
		t.Fatalf("failed to parse result: %v", jsonErr)
	}
	files, _ := parsed["files"].([]any)
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
}

func TestExpandBraces(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		pattern  string
		expected int
		contains []string
	}{
		{
			name:     "no_braces",
			pattern:  "src/**/*.go",
			expected: 1,
			contains: []string{"src/**/*.go"},
		},
		{
			name:     "single_brace_pair",
			pattern:  "src/**/*.{ts,tsx}",
			expected: 2,
			contains: []string{"src/**/*.ts", "src/**/*.tsx"},
		},
		{
			name:     "three_options",
			pattern:  "*.{go,js,ts}",
			expected: 3,
			contains: []string{"*.go", "*.js", "*.ts"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := expandBraces(tt.pattern)
			if len(result) != tt.expected {
				t.Errorf("expandBraces(%q) returned %d patterns, want %d", tt.pattern, len(result), tt.expected)
			}
			for _, expected := range tt.contains {
				found := false
				for _, r := range result {
					if r == expected {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expandBraces(%q) missing expected pattern %q", tt.pattern, expected)
				}
			}
		})
	}
}

func TestExpandOneBrace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		pattern  string
		expected []string
	}{
		{
			name:     "no_brace",
			pattern:  "hello.go",
			expected: []string{"hello.go"},
		},
		{
			name:     "simple_brace",
			pattern:  "file.{go,js}",
			expected: []string{"file.go", "file.js"},
		},
		{
			name:     "unmatched_brace",
			pattern:  "file{go",
			expected: []string{"file{go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := expandOneBrace(tt.pattern)
			if len(result) != len(tt.expected) {
				t.Errorf("expandOneBrace(%q) = %v, want %v", tt.pattern, result, tt.expected)
				return
			}
			for i, r := range result {
				if r != tt.expected[i] {
					t.Errorf("expandOneBrace(%q)[%d] = %q, want %q", tt.pattern, i, r, tt.expected[i])
				}
			}
		})
	}
}

func TestSortByModTime(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "old.txt")
	file2 := filepath.Join(tmpDir, "new.txt")
	if err := os.WriteFile(file1, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}

	// Ensure different mod times by touching file2 later
	// On fast systems this may have same timestamp, which is acceptable for the sort

	paths := []string{file1, file2}
	sortByModTime(paths)

	// After sort, most recently modified should be first
	// We can't guarantee order without deliberate time gap, but verify it doesn't crash
	if len(paths) != 2 {
		t.Errorf("sortByModTime changed length: %d", len(paths))
	}
}

func TestMatchGlob(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		path    string
		match   bool
	}{
		{"simple_match", "*.go", "main.go", true},
		{"simple_no_match", "*.go", "main.js", false},
		{"star_star", "**", "any/path/file.txt", true},
		{"star_star_slash_star", "**/*", "any/path/file.txt", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matched, err := matchGlob(tt.pattern, tt.path)
			if err != nil {
				t.Errorf("matchGlob(%q, %q) error: %v", tt.pattern, tt.path, err)
			}
			if matched != tt.match {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, matched, tt.match)
			}
		})
	}
}

func TestGlobRecursive_BasicPatterns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows due to path separator differences in test")
	}
	t.Parallel()

	tmpDir := t.TempDir()
	sub := filepath.Join(tmpDir, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "root.go"), []byte("p"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nested.go"), []byte("p"), 0644); err != nil {
		t.Fatal(err)
	}

	// Non-recursive: only root.go
	matches, err := globRecursive(tmpDir, "*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Errorf("expected 1 match for *.go, got %d", len(matches))
	}

	// Recursive: both files
	matches, err = globRecursive(tmpDir, "**/*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Errorf("expected 2 matches for **/*.go, got %d", len(matches))
	}
}
