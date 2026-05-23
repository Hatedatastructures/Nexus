package context

import (
	"strings"
	"testing"
)

func TestParseLogOutput_Valid(t *testing.T) {
	t.Parallel()

	input := "abc1234|Fix bug|Author|2h ago\ndef5678|Add feature|Other|1d ago"
	commits := parseLogOutput(input)

	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}

	if commits[0].Hash != "abc1234" {
		t.Errorf("commits[0].Hash = %q, want %q", commits[0].Hash, "abc1234")
	}
	if commits[0].Message != "Fix bug" {
		t.Errorf("commits[0].Message = %q, want %q", commits[0].Message, "Fix bug")
	}
	if commits[0].Author != "Author" {
		t.Errorf("commits[0].Author = %q, want %q", commits[0].Author, "Author")
	}
	if commits[0].Time != "2h ago" {
		t.Errorf("commits[0].Time = %q, want %q", commits[0].Time, "2h ago")
	}

	if commits[1].Hash != "def5678" {
		t.Errorf("commits[1].Hash = %q, want %q", commits[1].Hash, "def5678")
	}
	if commits[1].Message != "Add feature" {
		t.Errorf("commits[1].Message = %q, want %q", commits[1].Message, "Add feature")
	}
	if commits[1].Author != "Other" {
		t.Errorf("commits[1].Author = %q, want %q", commits[1].Author, "Other")
	}
	if commits[1].Time != "1d ago" {
		t.Errorf("commits[1].Time = %q, want %q", commits[1].Time, "1d ago")
	}
}

func TestParseLogOutput_Empty(t *testing.T) {
	t.Parallel()

	commits := parseLogOutput("")
	if len(commits) != 0 {
		t.Fatalf("expected 0 commits for empty input, got %d", len(commits))
	}
}

func TestParseLogOutput_InvalidFormat(t *testing.T) {
	t.Parallel()

	input := "only|two|fields\nabc1234|Fix bug|Author|2h ago\nnoPipesAtAll"
	commits := parseLogOutput(input)

	if len(commits) != 1 {
		t.Fatalf("expected 1 valid commit, got %d", len(commits))
	}
	if commits[0].Hash != "abc1234" {
		t.Errorf("commits[0].Hash = %q, want %q", commits[0].Hash, "abc1234")
	}
}

func TestGitContext_Render_Full(t *testing.T) {
	t.Parallel()

	gc := &GitContext{
		Branch: "main",
		RecentCommits: []GitCommit{
			{Hash: "abc1234", Message: "Fix bug", Author: "Alice", Time: "2h ago"},
			{Hash: "def5678", Message: "Add feature", Author: "Bob", Time: "1d ago"},
		},
		StagedFiles:  []string{"file1.go", "file2.go"},
		Status:       "M file1.go\n?? file3.go",
		DiffStaged:   "diff --git a/file1.go b/file1.go\n+new line",
		DiffUnstaged: "diff --git a/file2.go b/file2.go\n-removed line",
	}

	rendered := gc.Render()

	if !strings.Contains(rendered, "<git_context>") {
		t.Error("Render output missing <git_context> opening tag")
	}
	if !strings.Contains(rendered, "</git_context>") {
		t.Error("Render output missing </git_context> closing tag")
	}
	if !strings.Contains(rendered, "Branch: main") {
		t.Error("Render output missing Branch section")
	}
	if !strings.Contains(rendered, "Recent commits:") {
		t.Error("Render output missing Recent commits section")
	}
	if !strings.Contains(rendered, "abc1234 Fix bug (Alice, 2h ago)") {
		t.Error("Render output missing first commit")
	}
	if !strings.Contains(rendered, "def5678 Add feature (Bob, 1d ago)") {
		t.Error("Render output missing second commit")
	}
	if !strings.Contains(rendered, "Staged files: file1.go, file2.go") {
		t.Error("Render output missing Staged files section")
	}
	if !strings.Contains(rendered, "Status: M file1.go") {
		t.Error("Render output missing Status section")
	}
	if !strings.Contains(rendered, "Staged changes:") {
		t.Error("Render output missing Staged changes section")
	}
	if !strings.Contains(rendered, "Unstaged changes:") {
		t.Error("Render output missing Unstaged changes section")
	}
}

func TestGitContext_Render_Minimal(t *testing.T) {
	t.Parallel()

	gc := &GitContext{
		Branch: "feature-branch",
	}

	rendered := gc.Render()

	if !strings.Contains(rendered, "<git_context>") {
		t.Error("Render output missing <git_context> opening tag")
	}
	if !strings.Contains(rendered, "</git_context>") {
		t.Error("Render output missing </git_context> closing tag")
	}
	if !strings.Contains(rendered, "Branch: feature-branch") {
		t.Error("Render output missing Branch")
	}
	if strings.Contains(rendered, "Recent commits:") {
		t.Error("Render output should not contain Recent commits when empty")
	}
	if strings.Contains(rendered, "Staged files:") {
		t.Error("Render output should not contain Staged files when empty")
	}
	if strings.Contains(rendered, "Status:") {
		t.Error("Render output should not contain Status when empty")
	}
	if strings.Contains(rendered, "Staged changes:") {
		t.Error("Render output should not contain Staged changes when empty")
	}
	if strings.Contains(rendered, "Unstaged changes:") {
		t.Error("Render output should not contain Unstaged changes when empty")
	}
}

func TestGitContext_Render_WithDiffs(t *testing.T) {
	t.Parallel()

	gc := &GitContext{
		Branch:       "dev",
		DiffStaged:   "diff --git a/a.go b/a.go\n+added",
		DiffUnstaged: "diff --git a/b.go b/b.go\n-removed",
	}

	rendered := gc.Render()

	if !strings.Contains(rendered, "Staged changes:") {
		t.Error("Render output missing 'Staged changes:' section")
	}
	if !strings.Contains(rendered, "Unstaged changes:") {
		t.Error("Render output missing 'Unstaged changes:' section")
	}
	if !strings.Contains(rendered, gc.DiffStaged) {
		t.Error("Render output missing staged diff content")
	}
	if !strings.Contains(rendered, gc.DiffUnstaged) {
		t.Error("Render output missing unstaged diff content")
	}
}

func TestGitContext_String(t *testing.T) {
	t.Parallel()

	gc := &GitContext{
		Branch: "main",
		RecentCommits: []GitCommit{
			{Hash: "deadbeef", Message: "Init", Author: "Dev", Time: "3h ago"},
		},
	}

	if gc.String() != gc.Render() {
		t.Error("String() should delegate to Render() and return identical output")
	}
}
