package environments

import (
	"context"
	"testing"
)

// ─── Synthesize action ───

func TestWebResearchEnvironment_Synthesize(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test query")

	for i := 0; i < 3; i++ {
		_, _ = env.Execute(t.Context(), Action{
			Type:       "search",
			Parameters: map[string]any{"keywords": "topic"},
		})
	}

	obs, err := env.Execute(t.Context(), Action{
		Type:       "synthesize",
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute(synthesize) error = %v", err)
	}
	if obs.Reward <= 0 {
		t.Errorf("Reward = %f, want positive", obs.Reward)
	}

	findings := env.Findings()
	if len(findings) != 1 {
		t.Fatalf("Findings() len = %d, want 1", len(findings))
	}
}

func TestWebResearchEnvironment_SynthesizeInsufficientSources(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")

	_, _ = env.Execute(t.Context(), Action{
		Type:       "search",
		Parameters: map[string]any{"keywords": "a"},
	})
	_, _ = env.Execute(t.Context(), Action{
		Type:       "search",
		Parameters: map[string]any{"keywords": "b"},
	})

	obs, _ := env.Execute(t.Context(), Action{
		Type:       "synthesize",
		Parameters: map[string]any{},
	})
	if obs.Reward >= 0 {
		t.Errorf("Reward = %f, want negative for insufficient sources", obs.Reward)
	}
}

// ─── Submit action ───

func TestWebResearchEnvironment_Submit(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test query")

	for i := 0; i < 3; i++ {
		_, _ = env.Execute(t.Context(), Action{
			Type:       "search",
			Parameters: map[string]any{"keywords": "topic"},
		})
	}
	_, _ = env.Execute(t.Context(), Action{
		Type:       "synthesize",
		Parameters: map[string]any{},
	})

	obs, err := env.Execute(t.Context(), Action{
		Type:       "submit",
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute(submit) error = %v", err)
	}
	if !obs.Done {
		t.Error("Done = false after submit, want true")
	}
	if obs.Reward != 1.0 {
		t.Errorf("Reward = %f, want 1.0", obs.Reward)
	}
}

func TestWebResearchEnvironment_SubmitNoFindings(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")

	obs, err := env.Execute(t.Context(), Action{
		Type:       "submit",
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if obs.Done {
		t.Error("Done = true with no findings, want false")
	}
	if obs.Reward >= 0 {
		t.Errorf("Reward = %f, want negative", obs.Reward)
	}
}

// ─── Step ───

func TestWebResearchEnvironment_Step(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()

	obs, err := env.Step(t.Context())
	if err != nil {
		t.Fatalf("Step() error = %v", err)
	}
	if obs.Done {
		t.Error("Done = true on first step, want false")
	}
}

func TestWebResearchEnvironment_StepAdvancesPhases(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()

	for i := 0; i < 4; i++ {
		obs, err := env.Step(t.Context())
		if err != nil {
			t.Fatalf("Step(%d) error = %v", i, err)
		}
		if i == 3 && !obs.Done {
			t.Error("expected Done = true after 4 steps")
		}
		if i < 3 && obs.Done {
			t.Errorf("Step(%d): Done = true too early", i)
		}
	}
}

func TestWebResearchEnvironment_StepCancelled(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := env.Step(ctx)
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

// ─── QualityScore ───

func TestWebResearchEnvironment_QualityScoreProgression(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")

	initial := env.QualityScore()
	if initial != 0 {
		t.Errorf("initial QualityScore = %d, want 0", initial)
	}

	_, _ = env.Execute(t.Context(), Action{
		Type:       "search",
		Parameters: map[string]any{"keywords": "topic"},
	})
	afterSearch := env.QualityScore()
	if afterSearch <= initial {
		t.Errorf("QualityScore after search = %d, should increase from %d", afterSearch, initial)
	}
}

// ─── Sources ───

func TestWebResearchEnvironment_SourcesIsCopy(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "search",
		Parameters: map[string]any{"keywords": "topic"},
	})

	s1 := env.Sources()
	s1[0].URL = "tampered"

	s2 := env.Sources()
	if s2[0].URL == "tampered" {
		t.Error("Sources() should return a copy, not a reference")
	}
}

// ─── Findings ───

func TestWebResearchEnvironment_FindingsIsCopy(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")

	for i := 0; i < 3; i++ {
		_, _ = env.Execute(t.Context(), Action{
			Type:       "search",
			Parameters: map[string]any{"keywords": "topic"},
		})
	}
	_, _ = env.Execute(t.Context(), Action{
		Type:       "synthesize",
		Parameters: map[string]any{},
	})

	f1 := env.Findings()
	f1[0].Topic = "tampered"

	f2 := env.Findings()
	if f2[0].Topic == "tampered" {
		t.Error("Findings() should return a copy, not a reference")
	}
}

// ─── Render ───

func TestWebResearchEnvironment_Render(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test query")

	rendered := env.Render()
	if rendered == "" {
		t.Fatal("Render() returned empty string")
	}
}
