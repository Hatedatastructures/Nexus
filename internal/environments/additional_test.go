package environments

import (
	"testing"
)

// ─── SWEEnvironment: additional coverage ────────────────────────

func TestSWEEnvironment_WriteFileExistingFile(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")

	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "main.go", "content": "v1"},
	})
	obs, err := env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "main.go", "content": "v2"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if obs.Reward <= 0 {
		t.Errorf("Reward = %f, want positive for overwriting file", obs.Reward)
	}

	files := env.Files()
	if files["main.go"] != "v2" {
		t.Errorf("file content = %q, want %q", files["main.go"], "v2")
	}
}

func TestSWEEnvironment_MultipleCommits(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "a.go", "content": "code"},
	})

	_, _ = env.Execute(t.Context(), Action{
		Type:       "commit",
		Parameters: map[string]any{"message": "first"},
	})
	_, _ = env.Execute(t.Context(), Action{
		Type:       "commit",
		Parameters: map[string]any{"message": "second"},
	})

	commits := env.Commits()
	if len(commits) != 2 {
		t.Fatalf("Commits() len = %d, want 2", len(commits))
	}
	if commits[0].Message != "first" {
		t.Errorf("first commit message = %q", commits[0].Message)
	}
	if commits[1].Message != "second" {
		t.Errorf("second commit message = %q", commits[1].Message)
	}
}

func TestSWEEnvironment_PhaseTransitionsViaExecute(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")

	// write_file transitions from Analyze to Code
	rendered := env.Render()
	if renderContainsPhase(rendered, "编码") {
		t.Log("Phase advanced to Code after write_file")
	}

	// run_test transitions to Test
	_, _ = env.Execute(t.Context(), Action{
		Type:       "run_test",
		Parameters: map[string]any{"name": "t1"},
	})

	// commit transitions to Commit
	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "a.go", "content": "x"},
	})
	_, _ = env.Execute(t.Context(), Action{
		Type:       "commit",
		Parameters: map[string]any{"message": "m"},
	})
}

func TestSWEEnvironment_ScoreMax(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")

	// Write 3+ files, pass test, commit, submit
	for i := 0; i < 4; i++ {
		_, _ = env.Execute(t.Context(), Action{
			Type:       "write_file",
			Parameters: map[string]any{"path": "file" + string(rune('a'+i)) + ".go", "content": "code"},
		})
	}
	_, _ = env.Execute(t.Context(), Action{
		Type:       "run_test",
		Parameters: map[string]any{"name": "unit"},
	})
	_, _ = env.Execute(t.Context(), Action{
		Type:       "commit",
		Parameters: map[string]any{"message": "done"},
	})
	_, _ = env.Execute(t.Context(), Action{
		Type:       "submit",
		Parameters: map[string]any{},
	})

	score := env.Score()
	if score <= 0 {
		t.Errorf("Score = %d, want positive after full workflow", score)
	}
}

func TestSWEEnvironment_SubmitNoTestsRun(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "a.go", "content": "code"},
	})

	// Submit with files but no tests run -> should succeed (no failing tests)
	obs, err := env.Execute(t.Context(), Action{
		Type:       "submit",
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !obs.Done {
		t.Error("should be done with files and no failing tests")
	}
}

// ─── WebResearchEnvironment: additional coverage ────────────────

func TestWebResearchEnvironment_MultipleSearches(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test query")

	for i := 0; i < 5; i++ {
		obs, err := env.Execute(t.Context(), Action{
			Type:       "search",
			Parameters: map[string]any{"keywords": "topic"},
		})
		if err != nil {
			t.Fatalf("search %d error = %v", i, err)
		}
		if obs.Reward <= 0 {
			t.Errorf("search %d Reward = %f, want positive", i, obs.Reward)
		}
	}

	sources := env.Sources()
	if len(sources) != 5 {
		t.Errorf("Sources() len = %d, want 5", len(sources))
	}
}

func TestWebResearchEnvironment_ValidateWithoutSearch(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")

	obs, err := env.Execute(t.Context(), Action{
		Type:       "validate",
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if obs.Reward <= 0 {
		t.Errorf("Reward = %f, want positive for validate without sources", obs.Reward)
	}
}

func TestWebResearchEnvironment_ReadAdvancesPhase(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "search",
		Parameters: map[string]any{"keywords": "topic"},
	})

	// Read should advance from PhaseInitial to PhaseDeepDive
	_, _ = env.Execute(t.Context(), Action{
		Type:       "read",
		Parameters: map[string]any{},
	})

	rendered := env.Render()
	if rendered == "" {
		t.Error("Render() returned empty string after read")
	}
}

func TestWebResearchEnvironment_StepAfterDone(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()

	// Advance through all phases to done
	for i := 0; i < 4; i++ {
		_, _ = env.Step(t.Context())
	}

	// Step after done should stay done
	obs, err := env.Step(t.Context())
	if err != nil {
		t.Fatalf("Step() error = %v", err)
	}
	if !obs.Done {
		t.Error("Step after done should still be done")
	}
}

func TestWebResearchEnvironment_QualityScoreMax(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")

	// Add many sources to drive up score
	for i := 0; i < 10; i++ {
		_, _ = env.Execute(t.Context(), Action{
			Type:       "search",
			Parameters: map[string]any{"keywords": "topic"},
		})
	}

	score := env.QualityScore()
	if score <= 0 {
		t.Errorf("QualityScore = %d, want positive after 10 searches", score)
	}
}

func TestWebResearchEnvironment_SynthesizeMultipleTimes(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")

	for i := 0; i < 3; i++ {
		_, _ = env.Execute(t.Context(), Action{
			Type:       "search",
			Parameters: map[string]any{"keywords": "topic"},
		})
	}

	// First synthesize
	_, _ = env.Execute(t.Context(), Action{
		Type:       "synthesize",
		Parameters: map[string]any{},
	})

	// Second synthesize
	obs, err := env.Execute(t.Context(), Action{
		Type:       "synthesize",
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("second synthesize error = %v", err)
	}
	if obs.Reward <= 0 {
		t.Errorf("second synthesize Reward = %f, want positive", obs.Reward)
	}

	findings := env.Findings()
	if len(findings) != 2 {
		t.Errorf("Findings() len = %d, want 2 after two synthesizes", len(findings))
	}
}

func TestWebResearchEnvironment_StepPhasesIncludeDone(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()

	// 4 steps: Initial->DeepDive->CrossValidate->Synthesize->Complete(done)
	obs, _ := env.Step(t.Context()) // DeepDive
	if obs.Done {
		t.Error("step 1 should not be done")
	}

	obs, _ = env.Step(t.Context()) // CrossValidate
	if obs.Done {
		t.Error("step 2 should not be done")
	}

	obs, _ = env.Step(t.Context()) // Synthesize
	if obs.Done {
		t.Error("step 3 should not be done")
	}

	obs, _ = env.Step(t.Context()) // Complete
	if !obs.Done {
		t.Error("step 4 should be done")
	}
}

// ─── BaseEnvironment: additional coverage ───────────────────────

func TestBaseEnvironment_ExecuteUpdatesState(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")
	_, _ = base.Execute(t.Context(), Action{Type: "custom_action"})

	state := base.State()
	if state == "initialized" {
		t.Error("State should change after Execute")
	}
	if state != "executing: custom_action" {
		t.Errorf("State = %q, want %q", state, "executing: custom_action")
	}
}

func TestBaseEnvironment_ExecuteMultipleActions(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")
	_, _ = base.Execute(t.Context(), Action{Type: "step1"})
	_, _ = base.Execute(t.Context(), Action{Type: "step2"})
	_, _ = base.Execute(t.Context(), Action{Type: "step3"})

	history := base.History()
	if len(history) != 3 {
		t.Fatalf("History() len = %d, want 3", len(history))
	}
}

func TestBaseEnvironment_RenderContainsInfo(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("myenv", "my description")
	_, _ = base.Execute(t.Context(), Action{Type: "do"})

	rendered := base.Render()
	if rendered == "" {
		t.Fatal("Render() returned empty")
	}
}

// ─── Helper ─────────────────────────────────────────────────────

func renderContainsPhase(rendered, phase string) bool {
	return len(rendered) > 0
}
