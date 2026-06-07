package environments

import (
	"context"
	"testing"
)

// ─── SWEEnvironment construction ───

func TestNewSWEEnvironment(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	if env == nil {
		t.Fatal("NewSWEEnvironment() returned nil")
	}
	if env.Name != "swe" {
		t.Errorf("Name = %q, want %q", env.Name, "swe")
	}
	if env.Score() != 0 {
		t.Errorf("Score() = %d, want 0", env.Score())
	}
}

func TestSWEEnvironment_ImplementsInterface(t *testing.T) {
	t.Parallel()

	var _ Environment = (*SWEEnvironment)(nil)
}

func TestSWEEnvironment_SetTask(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("Fix bug in authentication module")

	rendered := env.Render()
	if rendered == "" {
		t.Error("Render() returned empty string")
	}
}

// ─── SWEEnvironment Execute (cancelled context) ───

func TestSWEEnvironment_ExecuteCancelled(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := env.Execute(ctx, Action{Type: "read_file"})
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

// ─── SWEEnvironment Step ───

func TestSWEEnvironment_Step(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()

	obs, err := env.Step(t.Context())
	if err != nil {
		t.Fatalf("Step() error = %v", err)
	}
	if obs.Done {
		t.Error("Done = true on first step, want false")
	}
}

func TestSWEEnvironment_StepAdvancesPhases(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()

	// Step through all phases
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

func TestSWEEnvironment_StepCancelled(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := env.Step(ctx)
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

// ─── SWEEnvironment Score ───

func TestSWEEnvironment_ScoreProgression(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")

	initial := env.Score()
	if initial != 0 {
		t.Errorf("initial Score = %d, want 0", initial)
	}

	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "a.go", "content": "code"},
	})
	afterWrite := env.Score()
	if afterWrite <= initial {
		t.Errorf("Score after write = %d, should increase from %d", afterWrite, initial)
	}

	_, _ = env.Execute(t.Context(), Action{
		Type:       "run_test",
		Parameters: map[string]any{"name": "t1"},
	})
	afterTest := env.Score()
	if afterTest <= afterWrite {
		t.Errorf("Score after test = %d, should increase from %d", afterTest, afterWrite)
	}
}

// ─── SWEEnvironment Files ───

func TestSWEEnvironment_FilesIsCopy(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "a.go", "content": "original"},
	})

	f1 := env.Files()
	f1["a.go"] = "modified"

	f2 := env.Files()
	if f2["a.go"] == "modified" {
		t.Error("Files() should return a copy, not a reference")
	}
}

// ─── SWEEnvironment Render ───

func TestSWEEnvironment_Render(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("Fix authentication")

	rendered := env.Render()
	if rendered == "" {
		t.Fatal("Render() returned empty string")
	}
}

// ─── SWEEnvironment Commits ───

func TestSWEEnvironment_CommitsIsCopy(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "a.go", "content": "x"},
	})
	_, _ = env.Execute(t.Context(), Action{
		Type:       "commit",
		Parameters: map[string]any{"message": "c1"},
	})

	c1 := env.Commits()
	c1[0].Message = "tampered"

	c2 := env.Commits()
	if c2[0].Message == "tampered" {
		t.Error("Commits() should return a copy, not a reference")
	}
}
