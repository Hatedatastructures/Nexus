package commands

import (
	"testing"
)

func TestSkillCommandName(t *testing.T) {
	t.Parallel()
	c := &SkillCommand{}
	if c.Name() != "skill" {
		t.Errorf("SkillCommand.Name() = %q, want %q", c.Name(), "skill")
	}
}

func TestSkillCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &SkillCommand{}
	if c.Synopsis() == "" {
		t.Error("SkillCommand.Synopsis() returned empty string")
	}
}

func TestSkillCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("skill")
	if !ok {
		t.Fatal("skill command not registered")
	}
	if _, isSkill := cmd.(*SkillCommand); !isSkill {
		t.Errorf("expected *SkillCommand, got %T", cmd)
	}
}

func TestExtractSkillName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want string
	}{
		{"git SSH", "git@github.com:user/my-skill.git", "my-skill"},
		{"HTTPS github", "https://github.com/user/cool-skill", "cool-skill"},
		{"HTTPS with .git", "https://github.com/user/repo.git", "repo"},
		{"HTTPS with .md", "https://example.com/path/skill.md", "skill"},
		{"trailing slash", "https://github.com/user/repo/", "repo"},
		{"query params", "https://github.com/user/repo?branch=main", "repo"},
		{"invalid", "not-a-url", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractSkillName(tt.url)
			if got != tt.want {
				t.Errorf("extractSkillName(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestIsGitURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"git SSH", "git@github.com:user/repo.git", true},
		{"HTTPS github .git", "https://github.com/user/repo.git", true},
		{"HTTPS github no .git", "https://github.com/user/repo", true},
		{"HTTPS github .md", "https://github.com/user/repo.md", false},
		{"random URL", "https://example.com/file.txt", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isGitURL(tt.url)
			if got != tt.want {
				t.Errorf("isGitURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
