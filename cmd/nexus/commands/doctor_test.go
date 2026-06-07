package commands

import (
	"testing"
)

func TestDoctorCommandName(t *testing.T) {
	t.Parallel()
	c := &DoctorCommand{}
	if c.Name() != "doctor" {
		t.Errorf("DoctorCommand.Name() = %q, want %q", c.Name(), "doctor")
	}
}

func TestDoctorCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &DoctorCommand{}
	if c.Synopsis() == "" {
		t.Error("DoctorCommand.Synopsis() returned empty string")
	}
}

func TestDoctorCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("doctor")
	if !ok {
		t.Fatal("doctor command not registered")
	}
	if _, isDoctor := cmd.(*DoctorCommand); !isDoctor {
		t.Errorf("expected *DoctorCommand, got %T", cmd)
	}
}
