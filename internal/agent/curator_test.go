package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewCurator(t *testing.T) {
	c := NewCurator("/tmp/skills")
	if c == nil {
		t.Fatal("curator is nil")
	}
	if c.skillsDir != "/tmp/skills" {
		t.Errorf("skillsDir = %q, want /tmp/skills", c.skillsDir)
	}
}

func TestCurator_LoadState_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	c := NewCurator("")
	c.stateFile = filepath.Join(tmpDir, "nonexistent.json")

	if err := c.LoadState(); err != nil {
		t.Errorf("LoadState with no file: %v", err)
	}
}

func TestCurator_SaveAndLoadState(t *testing.T) {
	tmpDir := t.TempDir()
	c := NewCurator("")
	c.stateFile = filepath.Join(tmpDir, "curator.json")

	c.state.LastRun = time.Now()
	c.state.RunCount = 5
	c.state.Skills = []CuratorSkill{
		{Name: "test-skill", State: SkillStateActive, UsageCount: 10},
	}

	if err := c.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	c2 := NewCurator("")
	c2.stateFile = filepath.Join(tmpDir, "curator.json")
	if err := c2.LoadState(); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if c2.state.RunCount != 5 {
		t.Errorf("RunCount = %d, want 5", c2.state.RunCount)
	}
	if len(c2.state.Skills) != 1 {
		t.Errorf("Skills len = %d, want 1", len(c2.state.Skills))
	}
}

func TestCurator_ShouldRunNow_FirstRun(t *testing.T) {
	c := NewCurator("")
	if !c.ShouldRunNow() {
		t.Error("first run should return true")
	}
}

func TestCurator_ShouldRunNow_RecentRun(t *testing.T) {
	c := NewCurator("")
	c.state.LastRun = time.Now()
	if c.ShouldRunNow() {
		t.Error("recent run should return false")
	}
}

func TestCurator_ApplyAutomaticTransitions_Stale(t *testing.T) {
	c := NewCurator("")
	skills := []CuratorSkill{
		{
			Name:     "old-skill",
			State:    SkillStateActive,
			LastUsed: time.Now().Add(-35 * 24 * time.Hour),
		},
	}

	result := c.ApplyAutomaticTransitions(skills)
	if result[0].State != SkillStateStale {
		t.Errorf("expected stale, got %s", result[0].State)
	}
}

func TestCurator_ApplyAutomaticTransitions_Archived(t *testing.T) {
	c := NewCurator("")
	skills := []CuratorSkill{
		{
			Name:     "very-old-skill",
			State:    SkillStateActive,
			LastUsed: time.Now().Add(-100 * 24 * time.Hour),
		},
	}

	result := c.ApplyAutomaticTransitions(skills)
	if result[0].State != SkillStateArchived {
		t.Errorf("expected archived, got %s", result[0].State)
	}
}

func TestCurator_ApplyAutomaticTransitions_StaleToArchived(t *testing.T) {
	c := NewCurator("")
	skills := []CuratorSkill{
		{
			Name:     "stale-skill",
			State:    SkillStateStale,
			LastUsed: time.Now().Add(-100 * 24 * time.Hour),
		},
	}

	result := c.ApplyAutomaticTransitions(skills)
	if result[0].State != SkillStateArchived {
		t.Errorf("expected archived, got %s", result[0].State)
	}
}

func TestCurator_ApplyAutomaticTransitions_Reactivated(t *testing.T) {
	c := NewCurator("")
	skills := []CuratorSkill{
		{Name: "reactivated-skill", State: SkillStateReactivated},
	}

	result := c.ApplyAutomaticTransitions(skills)
	if result[0].State != SkillStateActive {
		t.Errorf("reactivated should become active, got %s", result[0].State)
	}
}

func TestCurator_ApplyAutomaticTransitions_ArchivedStaysArchived(t *testing.T) {
	c := NewCurator("")
	skills := []CuratorSkill{
		{
			Name:     "archived-skill",
			State:    SkillStateArchived,
			LastUsed: time.Now(),
		},
	}

	result := c.ApplyAutomaticTransitions(skills)
	if result[0].State != SkillStateArchived {
		t.Errorf("archived should stay archived, got %s", result[0].State)
	}
}

func TestCurator_ApplyAutomaticTransitions_ActiveFresh(t *testing.T) {
	c := NewCurator("")
	skills := []CuratorSkill{
		{
			Name:     "fresh-skill",
			State:    SkillStateActive,
			LastUsed: time.Now(),
		},
	}

	result := c.ApplyAutomaticTransitions(skills)
	if result[0].State != SkillStateActive {
		t.Errorf("fresh active should stay active, got %s", result[0].State)
	}
}

func TestCurator_Run_NoSkillsDir(t *testing.T) {
	tmpDir := t.TempDir()
	c := NewCurator("")
	c.stateFile = filepath.Join(tmpDir, "curator.json")

	result, err := c.Run(t.Context())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil on first run")
	}
}

func TestCurator_Run_AlreadyRan(t *testing.T) {
	tmpDir := t.TempDir()
	c := NewCurator("")
	c.stateFile = filepath.Join(tmpDir, "curator.json")
	c.state.LastRun = time.Now()

	result, err := c.Run(t.Context())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != nil {
		t.Error("already ran should return nil")
	}
}

func TestCurator_scanSkills(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	skillDir := filepath.Join(skillsDir, "my-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# My Skill"), 0644); err != nil {
		t.Fatal(err)
	}

	c := NewCurator(skillsDir)
	c.stateFile = filepath.Join(tmpDir, "curator.json")

	if err := c.scanSkills(); err != nil {
		t.Fatalf("scanSkills: %v", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.state.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(c.state.Skills))
	}
	if c.state.Skills[0].Name != "my-skill" {
		t.Errorf("skill name = %q, want my-skill", c.state.Skills[0].Name)
	}
}

func TestCurator_scanSkills_EmptyDir(t *testing.T) {
	c := NewCurator("")
	if err := c.scanSkills(); err != nil {
		t.Fatalf("scanSkills empty dir: %v", err)
	}
}

func TestCurator_WriteRunReport(t *testing.T) {
	tmpDir := t.TempDir()
	c := NewCurator("")
	c.stateFile = filepath.Join(tmpDir, "curator.json")
	c.state.Skills = []CuratorSkill{
		{Name: "skill1", State: SkillStateActive, LastUsed: time.Now()},
		{Name: "skill2", State: SkillStateArchived, LastUsed: time.Now().Add(-100 * 24 * time.Hour)},
	}

	result := &ReviewResult{Summary: "test summary"}
	if err := c.WriteRunReport(tmpDir, result); err != nil {
		t.Fatalf("WriteRunReport: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "curator_report.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("report is empty")
	}
}

func TestCurator_WriteRunReport_NilResult(t *testing.T) {
	tmpDir := t.TempDir()
	c := NewCurator("")
	c.stateFile = filepath.Join(tmpDir, "curator.json")
	c.state.Skills = []CuratorSkill{
		{Name: "skill1", State: SkillStateActive, LastUsed: time.Now()},
	}

	if err := c.WriteRunReport(tmpDir, nil); err != nil {
		t.Fatalf("WriteRunReport nil result: %v", err)
	}
}

func TestCurator_GetSkillsByState(t *testing.T) {
	c := NewCurator("")
	c.state.Skills = []CuratorSkill{
		{Name: "a", State: SkillStateActive},
		{Name: "b", State: SkillStateArchived},
		{Name: "c", State: SkillStateActive},
	}

	active := c.GetSkillsByState(SkillStateActive)
	if len(active) != 2 {
		t.Errorf("active skills = %d, want 2", len(active))
	}
	archived := c.GetSkillsByState(SkillStateArchived)
	if len(archived) != 1 {
		t.Errorf("archived skills = %d, want 1", len(archived))
	}
}

func TestCurator_UpdateSkillUsage(t *testing.T) {
	c := NewCurator("")
	c.state.Skills = []CuratorSkill{
		{Name: "skill1", State: SkillStateStale, UsageCount: 5},
	}

	c.UpdateSkillUsage("skill1")

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Skills[0].UsageCount != 6 {
		t.Errorf("UsageCount = %d, want 6", c.state.Skills[0].UsageCount)
	}
	if c.state.Skills[0].State != SkillStateReactivated {
		t.Errorf("State = %s, want reactivated", c.state.Skills[0].State)
	}
}

func TestCurator_UpdateSkillUsage_UnknownSkill(t *testing.T) {
	c := NewCurator("")
	c.UpdateSkillUsage("nonexistent")
}

func TestCurator_UpdateSkillUsage_ArchivedSkill(t *testing.T) {
	c := NewCurator("")
	c.state.Skills = []CuratorSkill{
		{Name: "old", State: SkillStateArchived, UsageCount: 1},
	}

	c.UpdateSkillUsage("old")

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Skills[0].State != SkillStateReactivated {
		t.Errorf("State = %s, want reactivated", c.state.Skills[0].State)
	}
}
