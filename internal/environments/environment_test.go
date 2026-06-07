package environments

import (
	"context"
	"testing"
)

// ─── Observation and Action types ───

func TestObservation_Fields(t *testing.T) {
	t.Parallel()

	obs := &Observation{
		State:  "running",
		Reward: 0.5,
		Done:   false,
		Info:   map[string]any{"step": 1},
	}

	if obs.State != "running" {
		t.Errorf("State = %q, want %q", obs.State, "running")
	}
	if obs.Reward != 0.5 {
		t.Errorf("Reward = %f, want 0.5", obs.Reward)
	}
	if obs.Done {
		t.Error("Done = true, want false")
	}
	if obs.Info["step"] != 1 {
		t.Errorf("Info[step] = %v, want 1", obs.Info["step"])
	}
}

func TestAction_Fields(t *testing.T) {
	t.Parallel()

	action := Action{
		Type:       "search",
		Parameters: map[string]any{"query": "test"},
	}

	if action.Type != "search" {
		t.Errorf("Type = %q, want %q", action.Type, "search")
	}
	if action.Parameters["query"] != "test" {
		t.Errorf("Parameters[query] = %v, want test", action.Parameters["query"])
	}
}

// ─── BaseEnvironment tests ───

func TestNewBaseEnvironment(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "A test environment")
	if base.Name != "test" {
		t.Errorf("Name = %q, want %q", base.Name, "test")
	}
	if base.Description != "A test environment" {
		t.Errorf("Description = %q", base.Description)
	}
	if base.State() != "initialized" {
		t.Errorf("State() = %q, want %q", base.State(), "initialized")
	}
}

func TestBaseEnvironment_Execute(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")
	action := Action{Type: "do_something", Parameters: map[string]any{}}

	obs, err := base.Execute(t.Context(), action)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if obs.Done {
		t.Error("Done = true, want false")
	}
	if obs.Reward != 0.0 {
		t.Errorf("Reward = %f, want 0.0", obs.Reward)
	}
}

func TestBaseEnvironment_ExecuteCancelled(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := base.Execute(ctx, Action{Type: "x"})
	if err == nil {
		t.Fatal("Execute() with cancelled context should return error")
	}
}

func TestBaseEnvironment_Reset(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")

	// Add some history
	_, _ = base.Execute(t.Context(), Action{Type: "step1"})

	err := base.Reset(t.Context())
	if err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if base.State() != "reset" {
		t.Errorf("State() = %q, want %q", base.State(), "reset")
	}
	if len(base.History()) != 0 {
		t.Errorf("History() len = %d, want 0 after reset", len(base.History()))
	}
}

func TestBaseEnvironment_ResetCancelled(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := base.Reset(ctx)
	if err == nil {
		t.Fatal("Reset() with cancelled context should return error")
	}
}

func TestBaseEnvironment_Step(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")
	_, _ = base.Execute(t.Context(), Action{Type: "step1"})

	obs, err := base.Step(t.Context())
	if err != nil {
		t.Fatalf("Step() error = %v", err)
	}
	if obs.State == "" {
		t.Error("Step() returned empty state")
	}
}

func TestBaseEnvironment_StepCancelled(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := base.Step(ctx)
	if err == nil {
		t.Fatal("Step() with cancelled context should return error")
	}
}

func TestBaseEnvironment_Render(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")
	rendered := base.Render()
	if rendered == "" {
		t.Fatal("Render() returned empty string")
	}
	if len(rendered) < 10 {
		t.Errorf("Render() too short: %q", rendered)
	}
}

func TestBaseEnvironment_MarkDone(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")
	base.MarkDone()

	obs, _ := base.Step(t.Context())
	if !obs.Done {
		t.Error("Done = false after MarkDone(), want true")
	}
}

func TestBaseEnvironment_SetState(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")
	base.SetState("custom-state")
	if base.State() != "custom-state" {
		t.Errorf("State() = %q, want %q", base.State(), "custom-state")
	}
}

func TestBaseEnvironment_History(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")
	_, _ = base.Execute(t.Context(), Action{Type: "a"})
	_, _ = base.Execute(t.Context(), Action{Type: "b"})

	history := base.History()
	if len(history) != 2 {
		t.Fatalf("History() len = %d, want 2", len(history))
	}
	if history[0].Type != "a" || history[1].Type != "b" {
		t.Errorf("History() = %v", history)
	}
}

func TestBaseEnvironment_HistoryIsCopy(t *testing.T) {
	t.Parallel()

	base := NewBaseEnvironment("test", "test env")
	_, _ = base.Execute(t.Context(), Action{Type: "a"})

	h1 := base.History()
	h1[0].Type = "modified"

	h2 := base.History()
	if h2[0].Type == "modified" {
		t.Error("History() should return a copy, not a reference")
	}
}

// ─── Environment interface conformance ───

func TestBaseEnvironment_ImplementsInterface(t *testing.T) {
	t.Parallel()

	var _ Environment = (*BaseEnvironment)(nil)
}
