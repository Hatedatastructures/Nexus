package commands

import (
	"testing"
)

func TestToolCommandName(t *testing.T) {
	t.Parallel()
	c := &ToolCommand{}
	if c.Name() != "tool" {
		t.Errorf("ToolCommand.Name() = %q, want %q", c.Name(), "tool")
	}
}

func TestToolCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &ToolCommand{}
	if c.Synopsis() == "" {
		t.Error("ToolCommand.Synopsis() returned empty string")
	}
}

func TestToolCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("tool")
	if !ok {
		t.Fatal("tool command not registered")
	}
	if _, isTool := cmd.(*ToolCommand); !isTool {
		t.Errorf("expected *ToolCommand, got %T", cmd)
	}
}
