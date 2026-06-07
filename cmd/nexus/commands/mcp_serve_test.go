package commands

import (
	"testing"
)

func TestMCPServeCommandName(t *testing.T) {
	t.Parallel()
	c := &MCPServeCommand{}
	if c.Name() != "mcp-serve" {
		t.Errorf("MCPServeCommand.Name() = %q, want %q", c.Name(), "mcp-serve")
	}
}

func TestMCPServeCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &MCPServeCommand{}
	if c.Synopsis() == "" {
		t.Error("MCPServeCommand.Synopsis() returned empty string")
	}
}

func TestMCPServeCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("mcp-serve")
	if !ok {
		t.Fatal("mcp-serve command not registered")
	}
	if _, isMCP := cmd.(*MCPServeCommand); !isMCP {
		t.Errorf("expected *MCPServeCommand, got %T", cmd)
	}
}
