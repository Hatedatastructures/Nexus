package commands

import (
	"testing"
)

func TestCronCommandName(t *testing.T) {
	t.Parallel()
	c := &CronCommand{}
	if c.Name() != "cron" {
		t.Errorf("CronCommand.Name() = %q, want %q", c.Name(), "cron")
	}
}

func TestCronCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &CronCommand{}
	if c.Synopsis() == "" {
		t.Error("CronCommand.Synopsis() returned empty string")
	}
}

func TestCronCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("cron")
	if !ok {
		t.Fatal("cron command not registered")
	}
	if _, isCron := cmd.(*CronCommand); !isCron {
		t.Errorf("expected *CronCommand, got %T", cmd)
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short enough", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 8, "hello..."},
		{"empty string", "", 5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
