package environments

import (
	"testing"
)

// ─── SWEEnvironment Execute ───

func TestSWEEnvironment_ExecuteWriteFile(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test task")

	obs, err := env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "main.go", "content": "package main"},
	})
	if err != nil {
		t.Fatalf("Execute(write_file) error = %v", err)
	}
	if obs.Done {
		t.Error("Done = true, want false")
	}
	if obs.Reward <= 0 {
		t.Errorf("Reward = %f, want positive", obs.Reward)
	}

	files := env.Files()
	if files["main.go"] != "package main" {
		t.Errorf("file content = %q", files["main.go"])
	}
}

func TestSWEEnvironment_ExecuteWriteFileEmptyPath(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()

	obs, err := env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "", "content": "data"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if obs.Reward >= 0 {
		t.Errorf("Reward = %f, want negative for empty path", obs.Reward)
	}
}

func TestSWEEnvironment_ExecuteReadFile(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "test.txt", "content": "hello"},
	})

	obs, err := env.Execute(t.Context(), Action{
		Type:       "read_file",
		Parameters: map[string]any{"path": "test.txt"},
	})
	if err != nil {
		t.Fatalf("Execute(read_file) error = %v", err)
	}
	if obs.Reward <= 0 {
		t.Errorf("Reward = %f, want positive for reading existing file", obs.Reward)
	}
}

func TestSWEEnvironment_ExecuteReadFileNotFound(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()

	obs, err := env.Execute(t.Context(), Action{
		Type:       "read_file",
		Parameters: map[string]any{"path": "nonexistent.txt"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if obs.Reward != 0.0 {
		t.Errorf("Reward = %f, want 0.0 for not found", obs.Reward)
	}
}

func TestSWEEnvironment_ExecuteReadFileEmptyPath(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()

	obs, err := env.Execute(t.Context(), Action{
		Type:       "read_file",
		Parameters: map[string]any{"path": ""},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if obs.Reward >= 0 {
		t.Errorf("Reward = %f, want negative for empty path", obs.Reward)
	}
}

func TestSWEEnvironment_ExecuteRunTest(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "main.go", "content": "package main"},
	})

	obs, err := env.Execute(t.Context(), Action{
		Type:       "run_test",
		Parameters: map[string]any{"name": "unit_test"},
	})
	if err != nil {
		t.Fatalf("Execute(run_test) error = %v", err)
	}
	if obs.Info["passed"] != true {
		t.Error("test should pass when files exist")
	}
}

func TestSWEEnvironment_ExecuteRunTestNoFiles(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")

	obs, err := env.Execute(t.Context(), Action{
		Type:       "run_test",
		Parameters: map[string]any{"name": "empty_test"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if obs.Info["passed"] == true {
		t.Error("test should fail when no files exist")
	}
}

func TestSWEEnvironment_ExecuteRunTestDefaultName(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "a.go", "content": "x"},
	})

	obs, err := env.Execute(t.Context(), Action{
		Type:       "run_test",
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if obs.Info["test_name"] != "default_test" {
		t.Errorf("test_name = %v, want default_test", obs.Info["test_name"])
	}
}

func TestSWEEnvironment_ExecuteCommit(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "a.go", "content": "code"},
	})

	obs, err := env.Execute(t.Context(), Action{
		Type:       "commit",
		Parameters: map[string]any{"message": "initial commit"},
	})
	if err != nil {
		t.Fatalf("Execute(commit) error = %v", err)
	}
	if obs.Reward <= 0 {
		t.Errorf("Reward = %f, want positive for commit", obs.Reward)
	}

	commits := env.Commits()
	if len(commits) != 1 {
		t.Fatalf("Commits() len = %d, want 1", len(commits))
	}
	if commits[0].Message != "initial commit" {
		t.Errorf("commit message = %q", commits[0].Message)
	}
}

func TestSWEEnvironment_ExecuteCommitDefaultMessage(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "a.go", "content": "code"},
	})

	obs, _ := env.Execute(t.Context(), Action{
		Type:       "commit",
		Parameters: map[string]any{},
	})

	commits := env.Commits()
	if commits[0].Message != "auto commit" {
		t.Errorf("message = %q, want %q", commits[0].Message, "auto commit")
	}
	_ = obs
}

func TestSWEEnvironment_ExecuteSubmit(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")
	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "a.go", "content": "code"},
	})
	_, _ = env.Execute(t.Context(), Action{
		Type:       "run_test",
		Parameters: map[string]any{"name": "t1"},
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

func TestSWEEnvironment_ExecuteSubmitNoFiles(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")

	obs, err := env.Execute(t.Context(), Action{
		Type:       "submit",
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if obs.Done {
		t.Error("Done = true with no files, want false")
	}
	if obs.Reward >= 0 {
		t.Errorf("Reward = %f, want negative", obs.Reward)
	}
}

func TestSWEEnvironment_ExecuteSubmitFailingTests(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	env.SetTask("test")

	// Run test without files -> fails
	_, _ = env.Execute(t.Context(), Action{
		Type:       "run_test",
		Parameters: map[string]any{"name": "failing"},
	})
	// Add file but don't run another test
	_, _ = env.Execute(t.Context(), Action{
		Type:       "write_file",
		Parameters: map[string]any{"path": "a.go", "content": "x"},
	})

	obs, _ := env.Execute(t.Context(), Action{
		Type:       "submit",
		Parameters: map[string]any{},
	})
	if obs.Done {
		t.Error("should not be done with failing tests and no passing tests")
	}
}

func TestSWEEnvironment_ExecuteUnknownAction(t *testing.T) {
	t.Parallel()

	env := NewSWEEnvironment()
	_, err := env.Execute(t.Context(), Action{
		Type:       "unknown_action",
		Parameters: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for unknown action type")
	}
}
