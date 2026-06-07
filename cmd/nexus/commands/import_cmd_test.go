package commands

import (
	"testing"
)

func TestImportCommandName(t *testing.T) {
	t.Parallel()
	c := &ImportCommand{}
	if c.Name() != "import" {
		t.Errorf("ImportCommand.Name() = %q, want %q", c.Name(), "import")
	}
}

func TestImportCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &ImportCommand{}
	if c.Synopsis() == "" {
		t.Error("ImportCommand.Synopsis() returned empty string")
	}
}

func TestImportCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("import")
	if !ok {
		t.Fatal("import command not registered")
	}
	if _, isImport := cmd.(*ImportCommand); !isImport {
		t.Errorf("expected *ImportCommand, got %T", cmd)
	}
}

func TestExtractSkillNameFromURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want string
	}{
		{"git SSH", "git@github.com:user/my-skill.git", "my-skill"},
		{"HTTPS github", "https://github.com/user/another-skill", "another-skill"},
		{"HTTPS with .git", "https://github.com/user/skill3.git", "skill3"},
		{"HTTPS with .md", "https://example.com/skills/my-skill.md", "my-skill"},
		{"HTTPS with trailing slash", "https://github.com/user/repo/", "repo"},
		{"HTTPS with query params", "https://github.com/user/repo?ref=main", "repo"},
		{"invalid URL", "not-a-url", ""},
		{"git SSH with single segment", "git@github.com:user/solo.git", "solo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractSkillNameFromURL(tt.url)
			if got != tt.want {
				t.Errorf("extractSkillNameFromURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestIsGitURLFromImport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"git SSH github", "git@github.com:user/repo.git", true},
		{"HTTPS github", "https://github.com/user/repo.git", true},
		{"random domain", "https://random.example.com/repo.git", false},
		{"git SSH without colon", "git@github.comnocolon", false},
		{"non-git URL", "not-a-url", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isGitURLFromImport(tt.url)
			if got != tt.want {
				t.Errorf("isGitURLFromImport(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
