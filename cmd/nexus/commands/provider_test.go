package commands

import (
	"testing"
)

func TestProviderCommandName(t *testing.T) {
	t.Parallel()
	c := &ProviderCommand{}
	if c.Name() != "provider" {
		t.Errorf("ProviderCommand.Name() = %q, want %q", c.Name(), "provider")
	}
}

func TestProviderCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &ProviderCommand{}
	if c.Synopsis() == "" {
		t.Error("ProviderCommand.Synopsis() returned empty string")
	}
}

func TestProviderCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("provider")
	if !ok {
		t.Fatal("provider command not registered")
	}
	if _, isProvider := cmd.(*ProviderCommand); !isProvider {
		t.Errorf("expected *ProviderCommand, got %T", cmd)
	}
}
