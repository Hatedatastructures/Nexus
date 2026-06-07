package environments

import (
	"context"
	"testing"
)

// ─── WebResearchEnvironment construction ───

func TestNewWebResearchEnvironment(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	if env == nil {
		t.Fatal("NewWebResearchEnvironment() returned nil")
	}
	if env.Name != "web_research" {
		t.Errorf("Name = %q, want %q", env.Name, "web_research")
	}
	if env.QualityScore() != 0 {
		t.Errorf("QualityScore() = %d, want 0", env.QualityScore())
	}
}

func TestWebResearchEnvironment_ImplementsInterface(t *testing.T) {
	t.Parallel()

	var _ Environment = (*WebResearchEnvironment)(nil)
}

func TestWebResearchEnvironment_SetQuery(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("What is quantum computing?")

	rendered := env.Render()
	if rendered == "" {
		t.Error("Render() returned empty string")
	}
}

// ─── Search action ───

func TestWebResearchEnvironment_Search(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test query")

	obs, err := env.Execute(t.Context(), Action{
		Type:       "search",
		Parameters: map[string]any{"keywords": "golang testing"},
	})
	if err != nil {
		t.Fatalf("Execute(search) error = %v", err)
	}
	if obs.Done {
		t.Error("Done = true after search, want false")
	}
	if obs.Reward <= 0 {
		t.Errorf("Reward = %f, want positive", obs.Reward)
	}

	sources := env.Sources()
	if len(sources) != 1 {
		t.Fatalf("Sources() len = %d, want 1", len(sources))
	}
}

func TestWebResearchEnvironment_SearchEmptyKeywords(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")

	obs, err := env.Execute(t.Context(), Action{
		Type:       "search",
		Parameters: map[string]any{"keywords": ""},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if obs.Reward >= 0 {
		t.Errorf("Reward = %f, want negative for empty keywords", obs.Reward)
	}
}

func TestWebResearchEnvironment_SearchMaxSources(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")

	for i := 0; i < 20; i++ {
		_, _ = env.Execute(t.Context(), Action{
			Type:       "search",
			Parameters: map[string]any{"keywords": "keyword"},
		})
	}

	obs, _ := env.Execute(t.Context(), Action{
		Type:       "search",
		Parameters: map[string]any{"keywords": "overflow"},
	})
	if obs.Reward >= 0 {
		t.Errorf("Reward = %f, want negative for exceeding max sources", obs.Reward)
	}
}

// ─── Read action ───

func TestWebResearchEnvironment_Read(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "search",
		Parameters: map[string]any{"keywords": "topic"},
	})

	obs, err := env.Execute(t.Context(), Action{
		Type:       "read",
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute(read) error = %v", err)
	}
	if obs.Reward <= 0 {
		t.Errorf("Reward = %f, want positive", obs.Reward)
	}
}

func TestWebResearchEnvironment_ReadNoSources(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")

	obs, err := env.Execute(t.Context(), Action{
		Type:       "read",
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if obs.Reward >= 0 {
		t.Errorf("Reward = %f, want negative for no sources", obs.Reward)
	}
}

// ─── Validate action ───

func TestWebResearchEnvironment_Validate(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	env.SetQuery("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "search",
		Parameters: map[string]any{"keywords": "topic"},
	})

	obs, err := env.Execute(t.Context(), Action{
		Type:       "validate",
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute(validate) error = %v", err)
	}
	if obs.Reward <= 0 {
		t.Errorf("Reward = %f, want positive", obs.Reward)
	}
}

// ─── Unknown action ───

func TestWebResearchEnvironment_ExecuteUnknownAction(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	_, err := env.Execute(t.Context(), Action{
		Type:       "fly_to_moon",
		Parameters: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for unknown action type")
	}
}

func TestWebResearchEnvironment_ExecuteCancelled(t *testing.T) {
	t.Parallel()

	env := NewWebResearchEnvironment()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := env.Execute(ctx, Action{Type: "search"})
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}
