package commands

import (
	"testing"
)

func TestModelCommandName(t *testing.T) {
	t.Parallel()
	c := &ModelCommand{}
	if c.Name() != "model" {
		t.Errorf("ModelCommand.Name() = %q, want %q", c.Name(), "model")
	}
}

func TestModelCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &ModelCommand{}
	if c.Synopsis() == "" {
		t.Error("ModelCommand.Synopsis() returned empty string")
	}
}

func TestModelCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("model")
	if !ok {
		t.Fatal("model command not registered")
	}
	if _, isModel := cmd.(*ModelCommand); !isModel {
		t.Errorf("expected *ModelCommand, got %T", cmd)
	}
}
