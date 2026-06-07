package commands

import (
	"testing"
)

func TestRLCommandName(t *testing.T) {
	t.Parallel()
	c := &RLCommand{}
	if c.Name() != "rl" {
		t.Errorf("RLCommand.Name() = %q, want %q", c.Name(), "rl")
	}
}

func TestRLCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &RLCommand{}
	if c.Synopsis() == "" {
		t.Error("RLCommand.Synopsis() returned empty string")
	}
}

func TestRLCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("rl")
	if !ok {
		t.Fatal("rl command not registered")
	}
	if _, isRL := cmd.(*RLCommand); !isRL {
		t.Errorf("expected *RLCommand, got %T", cmd)
	}
}

func TestListEnvironments(t *testing.T) {
	t.Parallel()
	envs := ListEnvironments()
	if len(envs) == 0 {
		t.Fatal("ListEnvironments() returned empty list")
	}

	// Check for known environments
	envMap := make(map[string]bool, len(envs))
	for _, e := range envs {
		envMap[e.Name] = true
		if e.Description == "" {
			t.Errorf("environment %q has empty Description", e.Name)
		}
		if e.Framework == "" {
			t.Errorf("environment %q has empty Framework", e.Name)
		}
	}

	for _, name := range []string{"cartpole", "mountaincar", "atari", "mujoco", "custom"} {
		if !envMap[name] {
			t.Errorf("expected environment %q to be listed", name)
		}
	}
}

func TestRLEnvironmentJSONFields(t *testing.T) {
	t.Parallel()
	envs := ListEnvironments()
	for _, env := range envs {
		if env.Name == "" {
			t.Error("RLEnvironment.Name should not be empty")
		}
	}
}

func TestRLSystemPromptNotEmpty(t *testing.T) {
	t.Parallel()
	if rlSystemPrompt == "" {
		t.Error("rlSystemPrompt should not be empty")
	}
}
