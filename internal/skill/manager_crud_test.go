package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---- Manager ----

func TestManager_LoadAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createSkillFile(t, dir, "skill-a", "Skill A desc", "1.0.0")
	createSkillFile(t, dir, "skill-b", "Skill B desc", "1.0.0")

	mgr := NewManager(dir, nil)
	if err := mgr.LoadAll(context.Background()); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	active := mgr.GetActiveSkills()
	if len(active) != 2 {
		t.Errorf("expected 2 active skills, got %d", len(active))
	}
}

func TestManager_Get(t *testing.T) {
	t.Parallel()

	t.Run("returns loaded skill", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		createSkillFile(t, dir, "cached-skill", "Cached desc", "1.0.0")

		mgr := NewManager(dir, nil)
		if err := mgr.LoadAll(context.Background()); err != nil {
			t.Fatalf("LoadAll: %v", err)
		}

		skill, err := mgr.Get("cached-skill")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if skill.Name != "cached-skill" {
			t.Errorf("expected 'cached-skill', got %q", skill.Name)
		}
	})

	t.Run("lazy loads uncached skill", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		createSkillFile(t, dir, "lazy-skill", "Lazy desc", "1.0.0")

		mgr := NewManager(dir, nil)
		// Don't call LoadAll -- test lazy loading

		skill, err := mgr.Get("lazy-skill")
		if err != nil {
			t.Fatalf("Get (lazy): %v", err)
		}
		if skill.Name != "lazy-skill" {
			t.Errorf("expected 'lazy-skill', got %q", skill.Name)
		}
	})

	t.Run("returns error for missing skill", func(t *testing.T) {
		t.Parallel()
		mgr := NewManager(t.TempDir(), nil)
		_, err := mgr.Get("no-such-skill")
		if err == nil {
			t.Fatal("expected error for missing skill")
		}
	})
}

func TestManager_Create(t *testing.T) {
	t.Parallel()

	t.Run("creates new skill on disk", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mgr := NewManager(dir, nil)

		skill := &Skill{
			Name:        "created-skill",
			Description: "Created by test",
			Version:     "1.0.0",
			Body:        "# Created\n\nTest body",
		}
		if err := mgr.Create(skill); err != nil {
			t.Fatalf("Create: %v", err)
		}

		// Verify file on disk
		skillPath := filepath.Join(dir, "created-skill", "SKILL.md")
		if _, err := os.Stat(skillPath); os.IsNotExist(err) {
			t.Fatal("expected SKILL.md to exist on disk")
		}

		// Verify can be loaded back
		loaded, err := mgr.Get("created-skill")
		if err != nil {
			t.Fatalf("Get after create: %v", err)
		}
		if loaded.Name != "created-skill" {
			t.Errorf("expected 'created-skill', got %q", loaded.Name)
		}
	})

	t.Run("rejects nil skill", func(t *testing.T) {
		t.Parallel()
		mgr := NewManager(t.TempDir(), nil)
		if err := mgr.Create(nil); err == nil {
			t.Fatal("expected error for nil skill")
		}
	})

	t.Run("rejects empty name", func(t *testing.T) {
		t.Parallel()
		mgr := NewManager(t.TempDir(), nil)
		if err := mgr.Create(&Skill{Name: "", Description: "d"}); err == nil {
			t.Fatal("expected error for empty name")
		}
	})

	t.Run("rejects empty description", func(t *testing.T) {
		t.Parallel()
		mgr := NewManager(t.TempDir(), nil)
		if err := mgr.Create(&Skill{Name: "x", Description: ""}); err == nil {
			t.Fatal("expected error for empty description")
		}
	})

	t.Run("rejects path traversal name", func(t *testing.T) {
		t.Parallel()
		mgr := NewManager(t.TempDir(), nil)
		if err := mgr.Create(&Skill{Name: "../evil", Description: "d"}); err == nil {
			t.Fatal("expected error for path traversal")
		}
	})
}

func TestManager_Update(t *testing.T) {
	t.Parallel()

	t.Run("updates existing skill", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mgr := NewManager(dir, nil)

		original := &Skill{Name: "up-skill", Description: "Old desc", Body: "old body"}
		if err := mgr.Create(original); err != nil {
			t.Fatalf("Create: %v", err)
		}

		updated := &Skill{Name: "up-skill", Description: "New desc", Body: "new body"}
		if err := mgr.Update("up-skill", updated); err != nil {
			t.Fatalf("Update: %v", err)
		}

		skill, _ := mgr.Get("up-skill")
		if skill.Description != "New desc" {
			t.Errorf("expected 'New desc', got %q", skill.Description)
		}
	})

	t.Run("rejects nil skill", func(t *testing.T) {
		t.Parallel()
		mgr := NewManager(t.TempDir(), nil)
		if err := mgr.Update("x", nil); err == nil {
			t.Fatal("expected error for nil skill")
		}
	})

	t.Run("rejects nonexistent skill", func(t *testing.T) {
		t.Parallel()
		mgr := NewManager(t.TempDir(), nil)
		if err := mgr.Update("nope", &Skill{Name: "nope", Description: "d"}); err == nil {
			t.Fatal("expected error for nonexistent skill")
		}
	})
}

func TestManager_Delete(t *testing.T) {
	t.Parallel()

	t.Run("deletes skill from memory and disk", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mgr := NewManager(dir, nil)

		_ = mgr.Create(&Skill{Name: "del-skill", Description: "d", Body: "b"})
		if err := mgr.Delete("del-skill"); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		_, err := mgr.Get("del-skill")
		if err == nil {
			t.Error("expected error after delete")
		}

		// Disk cleanup is best-effort, just check memory
		active := mgr.GetActiveSkills()
		for _, s := range active {
			if s.Name == "del-skill" {
				t.Error("deleted skill should not appear in active list")
			}
		}
	})

	t.Run("rejects nonexistent skill", func(t *testing.T) {
		t.Parallel()
		mgr := NewManager(t.TempDir(), nil)
		if err := mgr.Delete("nope"); err == nil {
			t.Fatal("expected error for nonexistent skill")
		}
	})
}

func TestManager_DisableEnable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createSkillFile(t, dir, "toggle-skill", "Toggle desc", "1.0.0")

	mgr := NewManager(dir, []string{})
	_ = mgr.LoadAll(context.Background())

	// Initially active
	active := mgr.GetActiveSkills()
	if len(active) != 1 {
		t.Fatalf("expected 1 active skill, got %d", len(active))
	}

	// Disable
	mgr.Disable("toggle-skill")
	active = mgr.GetActiveSkills()
	if len(active) != 0 {
		t.Errorf("expected 0 active after disable, got %d", len(active))
	}

	// Enable again
	mgr.Enable("toggle-skill")
	active = mgr.GetActiveSkills()
	if len(active) != 1 {
		t.Errorf("expected 1 active after enable, got %d", len(active))
	}

	// Double-disable is safe
	mgr.Disable("toggle-skill")
	mgr.Disable("toggle-skill")
}

func TestManager_DisabledAtInit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createSkillFile(t, dir, "disabled-skill", "Disabled desc", "1.0.0")
	createSkillFile(t, dir, "enabled-skill", "Enabled desc", "1.0.0")

	mgr := NewManager(dir, []string{"disabled-skill"})
	_ = mgr.LoadAll(context.Background())

	active := mgr.GetActiveSkills()
	if len(active) != 1 || active[0].Name != "enabled-skill" {
		t.Errorf("expected only enabled-skill, got %v", skillNames(active))
	}
}

// ---- writeSkillToDisk ----

func TestManager_WriteSkillToDisk_YAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mgr := NewManager(dir, nil)

	skill := &Skill{
		Name:        "yaml-test",
		Description: "desc",
		Version:     "2.0.0",
		License:     "MIT",
		Platforms:   []string{"linux", "windows"},
		Body:        "# Body here",
		Fields:      map[string]any{"custom": "field"},
	}
	skill.Path = filepath.Join(dir, "yaml-test", "SKILL.md")

	if err := mgr.writeSkillToDisk(skill); err != nil {
		t.Fatalf("writeSkillToDisk: %v", err)
	}

	// Read back and parse
	data, err := os.ReadFile(skill.Path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	// Parse YAML frontmatter
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		t.Fatal("expected YAML frontmatter")
	}

	// Verify the round-trip via ParseSkillMarkdown
	parsed, err := ParseSkillMarkdown(data)
	if err != nil {
		t.Fatalf("ParseSkillMarkdown round-trip: %v", err)
	}
	if parsed.Name != "yaml-test" {
		t.Errorf("expected 'yaml-test', got %q", parsed.Name)
	}
	if parsed.Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %q", parsed.Version)
	}
	if parsed.License != "MIT" {
		t.Errorf("expected license 'MIT', got %q", parsed.License)
	}
	if parsed.Fields["custom"] != "field" {
		t.Errorf("expected custom='field', got %v", parsed.Fields["custom"])
	}
}

// ---- Concurrent access ----

func TestManager_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createSkillFile(t, dir, "conc-skill", "Concurrent desc", "1.0.0")

	mgr := NewManager(dir, nil)
	_ = mgr.LoadAll(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mgr.GetActiveSkills()
			_, _ = mgr.Get("conc-skill")
		}()
	}
	wg.Wait()
}
