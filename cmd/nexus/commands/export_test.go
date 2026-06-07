package commands

import (
	"testing"
)

func TestExportCommandName(t *testing.T) {
	t.Parallel()
	c := &ExportCommand{}
	if c.Name() != "export" {
		t.Errorf("ExportCommand.Name() = %q, want %q", c.Name(), "export")
	}
}

func TestExportCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &ExportCommand{}
	if c.Synopsis() == "" {
		t.Error("ExportCommand.Synopsis() returned empty string")
	}
}

func TestExportCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("export")
	if !ok {
		t.Fatal("export command not registered")
	}
	if _, isExport := cmd.(*ExportCommand); !isExport {
		t.Errorf("expected *ExportCommand, got %T", cmd)
	}
}
