package commands

import (
	"testing"
)

func TestSessionCommandName(t *testing.T) {
	t.Parallel()
	c := &SessionCommand{}
	if c.Name() != "session" {
		t.Errorf("SessionCommand.Name() = %q, want %q", c.Name(), "session")
	}
}

func TestSessionCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &SessionCommand{}
	if c.Synopsis() == "" {
		t.Error("SessionCommand.Synopsis() returned empty string")
	}
}

func TestSessionCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("session")
	if !ok {
		t.Fatal("session command not registered")
	}
	if _, isSession := cmd.(*SessionCommand); !isSession {
		t.Errorf("expected *SessionCommand, got %T", cmd)
	}
}

func TestMinHelper(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b, want int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{0, 10, 0},
	}
	for _, tt := range tests {
		got := min(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("min(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
