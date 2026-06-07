package commands

import (
	"testing"
)

func TestLogsCommandName(t *testing.T) {
	t.Parallel()
	c := &LogsCommand{}
	if c.Name() != "logs" {
		t.Errorf("LogsCommand.Name() = %q, want %q", c.Name(), "logs")
	}
}

func TestLogsCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &LogsCommand{}
	if c.Synopsis() == "" {
		t.Error("LogsCommand.Synopsis() returned empty string")
	}
}

func TestLogsCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("logs")
	if !ok {
		t.Fatal("logs command not registered")
	}
	if _, isLogs := cmd.(*LogsCommand); !isLogs {
		t.Errorf("expected *LogsCommand, got %T", cmd)
	}
}
