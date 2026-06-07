package commands

import (
	"testing"
)

func TestMemoryCommandName(t *testing.T) {
	t.Parallel()
	c := &MemoryCommand{}
	if c.Name() != "memory" {
		t.Errorf("MemoryCommand.Name() = %q, want %q", c.Name(), "memory")
	}
}

func TestMemoryCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &MemoryCommand{}
	if c.Synopsis() == "" {
		t.Error("MemoryCommand.Synopsis() returned empty string")
	}
}

func TestMemoryCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("memory")
	if !ok {
		t.Fatal("memory command not registered")
	}
	if _, isMemory := cmd.(*MemoryCommand); !isMemory {
		t.Errorf("expected *MemoryCommand, got %T", cmd)
	}
}
