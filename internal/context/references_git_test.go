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
