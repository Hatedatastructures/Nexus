package skill

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ---- sanitizeSkillName ----

func TestSanitizeSkillName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid name", "my-skill", false},
		{"valid with underscores", "my_skill_v2", false},
		{"valid with dots", "skill.js", false},
		{"empty name", "", true},
		{"path traversal", "../etc/passwd", true},
		{"double dot", "skill..name", true},
		{"forward slash", "skill/name", true},
		{"backslash", `skill\name`, true},
		{"null byte", "skill\x00name", true},
		{"too long", strings.Repeat("a", 65), true},
		{"max length ok", strings.Repeat("a", 64), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sanitizeSkillName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("sanitizeSkillName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

// ---- ParseSkillMarkdown ----

func TestParseSkillMarkdown(t *testing.T) {
	t.Parallel()

	t.Run("valid skill", func(t *testing.T) {
		t.Parallel()
		content := []byte("---\nname: my-skill\ndescription: A test skill\nversion: 1.0.0\n---\n\n# Body\nHello world\n")
		skill, err := ParseSkillMarkdown(content)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if skill.Name != "my-skill" {
			t.Errorf("expected name 'my-skill', got %q", skill.Name)
		}
		if skill.Description != "A test skill" {
			t.Errorf("expected 'A test skill', got %q", skill.Description)
		}
		if skill.Version != "1.0.0" {
			t.Errorf("expected version '1.0.0', got %q", skill.Version)
		}
		if !strings.Contains(skill.Body, "Hello world") {
			t.Errorf("expected body to contain 'Hello world', got %q", skill.Body)
		}
	})

	t.Run("missing frontmatter", func(t *testing.T) {
		t.Parallel()
		_, err := ParseSkillMarkdown([]byte("no frontmatter here"))
		if err == nil {
			t.Fatal("expected error for missing frontmatter")
		}
	})

	t.Run("missing closing delimiter", func(t *testing.T) {
		t.Parallel()
		_, err := ParseSkillMarkdown([]byte("---\nname: test\nno closing"))
		if err == nil {
			t.Fatal("expected error for missing closing delimiter")
		}
	})

	t.Run("missing name", func(t *testing.T) {
		t.Parallel()
		_, err := ParseSkillMarkdown([]byte("---\ndescription: test\n---\nbody"))
		if err == nil {
			t.Fatal("expected error for missing name")
		}
	})

	t.Run("missing description", func(t *testing.T) {
		t.Parallel()
		_, err := ParseSkillMarkdown([]byte("---\nname: test\n---\nbody"))
		if err == nil {
			t.Fatal("expected error for missing description")
		}
	})

	t.Run("name too long", func(t *testing.T) {
		t.Parallel()
		longName := strings.Repeat("a", 65)
		_, err := ParseSkillMarkdown([]byte("---\nname: " + longName + "\ndescription: test\n---\nbody"))
		if err == nil {
			t.Fatal("expected error for name too long")
		}
	})

	t.Run("platforms as list", func(t *testing.T) {
		t.Parallel()
		content := []byte("---\nname: test\ndescription: desc\nplatforms: [macos, linux]\n---\nbody")
		skill, err := ParseSkillMarkdown(content)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(skill.Platforms) != 2 {
			t.Fatalf("expected 2 platforms, got %d", len(skill.Platforms))
		}
		if skill.Platforms[0] != "macos" {
			t.Errorf("expected 'macos', got %q", skill.Platforms[0])
		}
	})

	t.Run("platforms as string", func(t *testing.T) {
		t.Parallel()
		content := []byte("---\nname: test\ndescription: desc\nplatforms: linux\n---\nbody")
		skill, err := ParseSkillMarkdown(content)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(skill.Platforms) != 1 || skill.Platforms[0] != "linux" {
			t.Errorf("expected ['linux'], got %v", skill.Platforms)
		}
	})

	t.Run("extra fields preserved", func(t *testing.T) {
		t.Parallel()
		content := []byte("---\nname: test\ndescription: desc\nauthor: me\ncustom: value\n---\nbody")
		skill, err := ParseSkillMarkdown(content)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if skill.Fields["author"] != "me" {
			t.Errorf("expected author='me', got %v", skill.Fields["author"])
		}
		if skill.Fields["custom"] != "value" {
			t.Errorf("expected custom='value', got %v", skill.Fields["custom"])
		}
	})

	t.Run("description truncated at 1024", func(t *testing.T) {
		t.Parallel()
		longDesc := strings.Repeat("x", 1100)
		content := []byte("---\nname: test\ndescription: " + longDesc + "\n---\nbody")
		skill, err := ParseSkillMarkdown(content)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(skill.Description) != 1024 {
			t.Errorf("expected description truncated to 1024, got %d", len(skill.Description))
		}
	})
}

// ---- skillMatchesPlatform ----

func TestSkillMatchesPlatform(t *testing.T) {
	t.Parallel()

	current := runtime.GOOS

	t.Run("empty platforms matches all", func(t *testing.T) {
		t.Parallel()
		skill := &Skill{Name: "test", Platforms: nil}
		if !skillMatchesPlatform(skill) {
			t.Error("empty platforms should match all")
		}
	})

	t.Run("exact GOOS match", func(t *testing.T) {
		t.Parallel()
		skill := &Skill{Name: "test", Platforms: []string{current}}
		if !skillMatchesPlatform(skill) {
			t.Errorf("expected match for platform %s", current)
		}
	})

	t.Run("macos maps to darwin", func(t *testing.T) {
		t.Parallel()
		skill := &Skill{Name: "test", Platforms: []string{"macos"}}
		matches := skillMatchesPlatform(skill)
		if current == "darwin" && !matches {
			t.Error("macos should match darwin")
		}
		if current != "darwin" && matches {
			t.Error("macos should not match non-darwin")
		}
	})

	t.Run("non-matching platform", func(t *testing.T) {
		t.Parallel()
		other := "nonexistent-platform"
		if current == other {
			return
		}
		skill := &Skill{Name: "test", Platforms: []string{other}}
		if skillMatchesPlatform(skill) {
			t.Error("should not match nonexistent platform")
		}
	})
}

// ---- SkillLoader ----

func TestSkillLoader_DiscoverAll(t *testing.T) {
	t.Parallel()

	t.Run("discovers skills in directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		createSkillFile(t, dir, "test-skill", "A test skill", "1.0.0")

		loader := NewSkillLoader(dir, nil)
		skills, err := loader.DiscoverAll()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(skills) != 1 {
			t.Fatalf("expected 1 skill, got %d", len(skills))
		}
		if skills[0].Name != "test-skill" {
			t.Errorf("expected 'test-skill', got %q", skills[0].Name)
		}
	})

	t.Run("skips excluded directories", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Create skill in .git (should be skipped)
		createSkillFile(t, filepath.Join(dir, ".git"), "hidden-skill", "Hidden", "1.0.0")
		createSkillFile(t, dir, "visible-skill", "Visible", "1.0.0")

		loader := NewSkillLoader(dir, nil)
		skills, err := loader.DiscoverAll()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(skills) != 1 || skills[0].Name != "visible-skill" {
			t.Errorf("expected only visible-skill, got %v", skillNames(skills))
		}
	})

	t.Run("deduplicates by name (local first)", func(t *testing.T) {
		t.Parallel()
		localDir := t.TempDir()
		extDir := t.TempDir()

		createSkillFile(t, localDir, "dup-skill", "Local version", "1.0.0")
		createSkillFile(t, extDir, "dup-skill", "External version", "2.0.0")

		loader := NewSkillLoader(localDir, []string{extDir})
		skills, err := loader.DiscoverAll()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(skills) != 1 {
			t.Fatalf("expected 1 skill, got %d", len(skills))
		}
		if skills[0].Description != "Local version" {
			t.Errorf("expected local version, got %q", skills[0].Description)
		}
	})

	t.Run("empty directory", func(t *testing.T) {
		t.Parallel()
		loader := NewSkillLoader(t.TempDir(), nil)
		skills, err := loader.DiscoverAll()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(skills) != 0 {
			t.Errorf("expected 0 skills, got %d", len(skills))
		}
	})

	t.Run("nonexistent directory", func(t *testing.T) {
		t.Parallel()
		loader := NewSkillLoader("/nonexistent/path/xyz", nil)
		skills, err := loader.DiscoverAll()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(skills) != 0 {
			t.Errorf("expected 0 skills for nonexistent dir, got %d", len(skills))
		}
	})
}

func TestSkillLoader_Load(t *testing.T) {
	t.Parallel()

	t.Run("loads existing skill", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		createSkillFile(t, dir, "load-test", "Loadable skill", "1.0.0")

		loader := NewSkillLoader(dir, nil)
		skill, err := loader.Load("load-test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if skill.Name != "load-test" {
			t.Errorf("expected 'load-test', got %q", skill.Name)
		}
	})

	t.Run("fails for invalid name", func(t *testing.T) {
		t.Parallel()
		loader := NewSkillLoader(t.TempDir(), nil)
		_, err := loader.Load("../etc/passwd")
		if err == nil {
			t.Fatal("expected error for path traversal")
		}
	})

	t.Run("fails for missing skill", func(t *testing.T) {
		t.Parallel()
		loader := NewSkillLoader(t.TempDir(), nil)
		_, err := loader.Load("nonexistent")
		if err == nil {
			t.Fatal("expected error for missing skill")
		}
	})
}

// ---- helpers ----

func createSkillFile(t *testing.T, baseDir, name, description, version string) {
	t.Helper()

	skillDir := filepath.Join(baseDir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", skillDir, err)
	}

	fm := map[string]any{
		"name":        name,
		"description": description,
	}
	if version != "" {
		fm["version"] = version
	}

	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		t.Fatalf("yaml marshal: %v", err)
	}

	content := "---\n" + string(yamlBytes) + "---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func skillNames(skills []*Skill) []string {
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = s.Name
	}
	return names
}

// Verify unused import is used
var _ = yaml.Unmarshal
