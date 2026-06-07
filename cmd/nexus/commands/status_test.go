package commands

import (
	"testing"
)

func TestStatusCommandName(t *testing.T) {
	t.Parallel()
	c := &StatusCommand{}
	if c.Name() != "status" {
		t.Errorf("StatusCommand.Name() = %q, want %q", c.Name(), "status")
	}
}

func TestStatusCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &StatusCommand{}
	if c.Synopsis() == "" {
		t.Error("StatusCommand.Synopsis() returned empty string")
	}
}

func TestStatusCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("status")
	if !ok {
		t.Fatal("status command not registered")
	}
	if _, isStatus := cmd.(*StatusCommand); !isStatus {
		t.Errorf("expected *StatusCommand, got %T", cmd)
	}
}
