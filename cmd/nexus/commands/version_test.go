package commands

import (
	"testing"
)

func TestVersionCommandName(t *testing.T) {
	t.Parallel()
	c := &VersionCommand{}
	if c.Name() != "version" {
		t.Errorf("VersionCommand.Name() = %q, want %q", c.Name(), "version")
	}
}

func TestVersionCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &VersionCommand{}
	if c.Synopsis() == "" {
		t.Error("VersionCommand.Synopsis() returned empty string")
	}
}

func TestGetVersion(t *testing.T) {
	t.Parallel()
	v := getVersion()
	if v == "" {
		t.Error("getVersion() returned empty string")
	}
}

func TestVersionCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("version")
	if !ok {
		t.Fatal("version command not registered")
	}
	if _, isVersion := cmd.(*VersionCommand); !isVersion {
		t.Errorf("expected *VersionCommand, got %T", cmd)
	}
}
