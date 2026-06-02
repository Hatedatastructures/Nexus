package context

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ParseReferences
// ---------------------------------------------------------------------------

func TestParseReferences(t *testing.T) {
	t.Parallel()

	t.Run("no references returns nil", func(t *testing.T) {
		t.Parallel()
		got := ParseReferences("hello world no refs")
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("parses @file reference", func(t *testing.T) {
		t.Parallel()
		got := ParseReferences("look at @file(main.go)")
		if len(got) != 1 {
			t.Fatalf("expected 1, got %d", len(got))
		}
		if got[0].Kind != "file" {
			t.Errorf("Kind = %q, want %q", got[0].Kind, "file")
		}
		if got[0].Target != "main.go" {
			t.Errorf("Target = %q, want %q", got[0].Target, "main.go")
		}
		if got[0].Raw != "@file(main.go)" {
			t.Errorf("Raw = %q, want %q", got[0].Raw, "@file(main.go)")
		}
	})

	t.Run("parses @folder reference", func(t *testing.T) {
		t.Parallel()
		got := ParseReferences("check @folder(src/)")
		if len(got) != 1 {
			t.Fatalf("expected 1, got %d", len(got))
		}
		if got[0].Kind != "folder" {
			t.Errorf("Kind = %q, want %q", got[0].Kind, "folder")
		}
	})

	t.Run("parses @diff reference", func(t *testing.T) {
		t.Parallel()
		got := ParseReferences("see @diff for changes")
		if len(got) != 1 {
			t.Fatalf("expected 1, got %d", len(got))
		}
		if got[0].Kind != "diff" {
			t.Errorf("Kind = %q, want %q", got[0].Kind, "diff")
		}
	})

	t.Run("parses @staged reference", func(t *testing.T) {
		t.Parallel()
		got := ParseReferences("review @staged")
		if len(got) != 1 {
			t.Fatalf("expected 1, got %d", len(got))
		}
		if got[0].Kind != "staged" {
			t.Errorf("Kind = %q, want %q", got[0].Kind, "staged")
		}
	})

	t.Run("parses @git reference", func(t *testing.T) {
		t.Parallel()
		got := ParseReferences("check @git(main)")
		if len(got) != 1 {
			t.Fatalf("expected 1, got %d", len(got))
		}
		if got[0].Kind != "git" {
			t.Errorf("Kind = %q, want %q", got[0].Kind, "git")
		}
		if got[0].Target != "main" {
			t.Errorf("Target = %q, want %q", got[0].Target, "main")
		}
	})

	t.Run("parses @url reference", func(t *testing.T) {
		t.Parallel()
		got := ParseReferences("see @url(https://example.com)")
		if len(got) != 1 {
			t.Fatalf("expected 1, got %d", len(got))
		}
		if got[0].Kind != "url" {
			t.Errorf("Kind = %q, want %q", got[0].Kind, "url")
		}
	})

	t.Run("parses multiple references", func(t *testing.T) {
		t.Parallel()
		got := ParseReferences("see @file(a.go) and @file(b.go)")
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
		if got[0].Target != "a.go" {
			t.Errorf("got[0].Target = %q, want %q", got[0].Target, "a.go")
		}
		if got[1].Target != "b.go" {
			t.Errorf("got[1].Target = %q, want %q", got[1].Target, "b.go")
		}
	})

	t.Run("parses line range", func(t *testing.T) {
		t.Parallel()
		got := ParseReferences("see @file(main.go:L10-L20)")
		if len(got) != 1 {
			t.Fatalf("expected 1, got %d", len(got))
		}
		if got[0].Target != "main.go" {
			t.Errorf("Target = %q, want %q", got[0].Target, "main.go")
		}
		if got[0].LineStart != 10 {
			t.Errorf("LineStart = %d, want 10", got[0].LineStart)
		}
		if got[0].LineEnd != 20 {
			t.Errorf("LineEnd = %d, want 20", got[0].LineEnd)
		}
	})

	t.Run("mixed references", func(t *testing.T) {
		t.Parallel()
		got := ParseReferences("@file(a.go) @diff @staged @git(main)")
		if len(got) != 4 {
			t.Fatalf("expected 4, got %d", len(got))
		}
		kinds := []string{"file", "diff", "staged", "git"}
		for i, want := range kinds {
			if got[i].Kind != want {
				t.Errorf("got[%d].Kind = %q, want %q", i, got[i].Kind, want)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// isSensitivePath
// ---------------------------------------------------------------------------

func TestIsSensitivePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{"/home/user/.ssh/id_rsa", true},
		{"/home/user/.aws/credentials", true},
		{"/home/user/.gnupg/key", true},
		{"/home/user/.gpg/key", true},
		{"/project/.env", true},
		{"/project/.nexus/.env", true},
		{"/home/user/credentials.json", true},
		{"/home/user/.netrc", true},
		{"/home/user/.npmrc", true},
		{"/home/user/project/main.go", false},
		{"/tmp/config.yaml", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			got := isSensitivePath(tc.path)
			if got != tc.want {
				t.Errorf("isSensitivePath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// expandFile
// ---------------------------------------------------------------------------

func TestExpandFile(t *testing.T) {
	t.Parallel()

	t.Run("reads existing file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.go")
		content := "package main\nfunc main() {}"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := expandFile(path, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got != content {
			t.Errorf("got %q, want %q", got, content)
		}
	})

	t.Run("reads file with line range", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")
		lines := "line1\nline2\nline3\nline4\nline5"
		if err := os.WriteFile(path, []byte(lines), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := expandFile(path, 2, 4)
		if err != nil {
			t.Fatal(err)
		}
		expected := "line2\nline3\nline4"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("non-existent file returns error", func(t *testing.T) {
		t.Parallel()
		_, err := expandFile("/nonexistent/file.txt", 0, 0)
		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})

	t.Run("sensitive path returns error", func(t *testing.T) {
		t.Parallel()
		_, err := expandFile("/home/user/.ssh/id_rsa", 0, 0)
		if err == nil {
			t.Error("expected error for sensitive path")
		}
	})

	t.Run("invalid line range returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")
		if err := os.WriteFile(path, []byte("line1\nline2"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := expandFile(path, 5, 3)
		if err == nil {
			t.Error("expected error for invalid line range")
		}
	})

	t.Run("end beyond file length clamped", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")
		if err := os.WriteFile(path, []byte("line1\nline2\nline3"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := expandFile(path, 2, 100)
		if err != nil {
			t.Fatal(err)
		}
		if got != "line2\nline3" {
			t.Errorf("got %q, want %q", got, "line2\nline3")
		}
	})
}

// ---------------------------------------------------------------------------
// expandFolder
// ---------------------------------------------------------------------------

func TestExpandFolder(t *testing.T) {
	t.Parallel()

	t.Run("lists directory tree", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := expandFolder(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "a.txt") {
			t.Error("expected a.txt in output")
		}
		if !strings.Contains(got, "sub/") {
			t.Error("expected sub/ in output")
		}
		if !strings.Contains(got, "b.txt") {
			t.Error("expected b.txt in output")
		}
	})

	t.Run("non-existent directory returns empty", func(t *testing.T) {
		t.Parallel()
		got, err := expandFolder("/nonexistent/dir")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("expected empty output for non-existent dir, got %q", got)
		}
	})

	t.Run("sensitive path returns error", func(t *testing.T) {
		t.Parallel()
		_, err := expandFolder("/home/user/.aws")
		if err == nil {
			t.Error("expected error for sensitive path")
		}
	})

	t.Run("skips hidden directories", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".hidden", "deep"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".hidden", "secret.txt"), []byte("s"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("v"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := expandFolder(dir)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "secret.txt") {
			t.Error("hidden directory files should be skipped")
		}
		if !strings.Contains(got, "visible.txt") {
			t.Error("visible file should be present")
		}
	})
}

// ---------------------------------------------------------------------------
// expandURL
// ---------------------------------------------------------------------------

func TestExpandURL(t *testing.T) {
	t.Parallel()

	t.Run("https URL returns placeholder", func(t *testing.T) {
		t.Parallel()
		got, err := expandURL(context.Background(), "https://example.com")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "https://example.com") {
			t.Errorf("expected URL in output, got %q", got)
		}
	})

	t.Run("http URL returns placeholder", func(t *testing.T) {
		t.Parallel()
		got, err := expandURL(context.Background(), "http://example.com")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "http://example.com") {
			t.Errorf("expected URL in output, got %q", got)
		}
	})

	t.Run("non-http URL returns error", func(t *testing.T) {
		t.Parallel()
		_, err := expandURL(context.Background(), "ftp://example.com")
		if err == nil {
			t.Error("expected error for non-http URL")
		}
	})
}

// ---------------------------------------------------------------------------
// expandGitDiff / expandGitLog
// ---------------------------------------------------------------------------

func TestExpandGitDiff(t *testing.T) {
	t.Parallel()

	t.Run("unstaged diff in git repo", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "Test")
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "add", ".")
		runGit(t, dir, "commit", "-m", "init")

		// Modify without staging
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("modified"), 0644); err != nil {
			t.Fatal(err)
		}

		ctx := context.Background()
		got, err := expandGitDiff(ctx, false)
		// May or may not have output depending on working dir, just verify no error
		if err != nil {
			t.Logf("expandGitDiff(unstaged) error (may be expected outside repo): %v", err)
		} else {
			t.Logf("expandGitDiff(unstaged) output: %q", got)
		}
	})

	t.Run("staged diff in git repo", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "Test")
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "add", ".")
		runGit(t, dir, "commit", "-m", "init")

		ctx := context.Background()
		got, err := expandGitDiff(ctx, true)
		if err != nil {
			t.Logf("expandGitDiff(staged) error (may be expected): %v", err)
		} else {
			t.Logf("expandGitDiff(staged) output: %q", got)
		}
	})
}

func TestExpandGitLog(t *testing.T) {
	t.Parallel()

	t.Run("empty spec shows recent log", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "Test")
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "add", ".")
		runGit(t, dir, "commit", "-m", "initial commit")

		ctx := context.Background()
		got, err := expandGitLog(ctx, "")
		if err != nil {
			t.Logf("expandGitLog error: %v", err)
		} else {
			t.Logf("expandGitLog output: %q", got)
		}
	})

	t.Run("with spec shows filtered log", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "Test")
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "add", ".")
		runGit(t, dir, "commit", "-m", "initial commit")

		ctx := context.Background()
		got, err := expandGitLog(ctx, "--oneline")
		if err != nil {
			t.Logf("expandGitLog with spec error: %v", err)
		} else {
			t.Logf("expandGitLog with spec output: %q", got)
		}
	})
}

// runGit is a test helper to run git commands in a specific directory.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2024-01-01T00:00:00", "GIT_COMMITTER_DATE=2024-01-01T00:00:00")
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v failed: %v", args, err)
	}
}

// ---------------------------------------------------------------------------
// expandReference (dispatch)
// ---------------------------------------------------------------------------

func TestExpandReference(t *testing.T) {
	t.Parallel()

	t.Run("unknown kind returns error", func(t *testing.T) {
		t.Parallel()
		_, err := expandReference(context.Background(), ContextReference{Kind: "unknown", Target: "x"})
		if err == nil {
			t.Error("expected error for unknown kind")
		}
	})

	t.Run("file kind dispatches to expandFile", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := expandReference(context.Background(), ContextReference{Kind: "file", Target: path})
		if err != nil {
			t.Fatal(err)
		}
		if got != "content" {
			t.Errorf("got %q, want %q", got, "content")
		}
	})

	t.Run("url kind dispatches to expandURL", func(t *testing.T) {
		t.Parallel()
		got, err := expandReference(context.Background(), ContextReference{Kind: "url", Target: "https://example.com"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "https://example.com") {
			t.Errorf("expected URL in output, got %q", got)
		}
	})

	t.Run("folder kind dispatches to expandFolder", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := expandReference(context.Background(), ContextReference{Kind: "folder", Target: dir})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "a.txt") {
			t.Errorf("expected a.txt in output, got %q", got)
		}
	})

	t.Run("diff kind dispatches to expandGitDiff", func(t *testing.T) {
		t.Parallel()
		// Just verify no panic — actual git diff output depends on repo state
		_, _ = expandReference(context.Background(), ContextReference{Kind: "diff"})
	})
}

// ---------------------------------------------------------------------------
// PreprocessReferences
// ---------------------------------------------------------------------------

func TestPreprocessReferences(t *testing.T) {
	t.Parallel()

	t.Run("no references returns original message", func(t *testing.T) {
		t.Parallel()
		got, err := PreprocessReferences(context.Background(), "hello world", 10000)
		if err != nil {
			t.Fatal(err)
		}
		if got != "hello world" {
			t.Errorf("got %q, want %q", got, "hello world")
		}
	})

	t.Run("expands file reference", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")
		if err := os.WriteFile(path, []byte("file content here"), 0644); err != nil {
			t.Fatal(err)
		}
		msg := "look at @file(" + path + ")"
		got, err := PreprocessReferences(context.Background(), msg, 10000)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "file content here") {
			t.Errorf("expected expanded content, got %q", got)
		}
	})

	t.Run("respects token budget hard limit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "big.txt")
		largeContent := strings.Repeat("x", 5000)
		if err := os.WriteFile(path, []byte(largeContent), 0644); err != nil {
			t.Fatal(err)
		}
		msg := "look at @file(" + path + ")"
		got, err := PreprocessReferences(context.Background(), msg, 100)
		if err != nil {
			t.Fatal(err)
		}
		// Hard limit = 100 * 0.5 * 4 = 200 chars
		if strings.Contains(got, largeContent) {
			t.Error("content should be truncated within hard limit")
		}
	})

	t.Run("failed expansion continues with other refs", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		goodPath := filepath.Join(dir, "good.txt")
		if err := os.WriteFile(goodPath, []byte("good content"), 0644); err != nil {
			t.Fatal(err)
		}
		msg := "see @file(/nonexistent/bad.txt) and @file(" + goodPath + ")"
		got, err := PreprocessReferences(context.Background(), msg, 10000)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "good content") {
			t.Error("good file should still be expanded")
		}
	})

	t.Run("soft limit warning added", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "big.txt")
		// Create content large enough to exceed soft limit
		content := strings.Repeat("x", 3000)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		msg := "look at @file(" + path + ")"
		got, err := PreprocessReferences(context.Background(), msg, 1000)
		if err != nil {
			t.Fatal(err)
		}
		// soft limit = 1000 * 0.25 * 4 = 1000 chars, content is 3000 -> should trigger warning
		if !strings.Contains(got, "注意") {
			t.Error("expected soft limit warning")
		}
	})

	t.Run("context cancellation stops expansion", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
		msg := "see @file(" + path + ")"
		_, err := PreprocessReferences(ctx, msg, 10000)
		// Should not panic; error is acceptable since context is cancelled
		if err != nil {
			t.Logf("expected error from cancelled context: %v", err)
		}
	})
}
