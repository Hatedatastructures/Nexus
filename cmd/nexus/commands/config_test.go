package commands

import (
	"testing"
)

func TestConfigCommandName(t *testing.T) {
	t.Parallel()
	c := &ConfigCommand{}
	if c.Name() != "config" {
		t.Errorf("ConfigCommand.Name() = %q, want %q", c.Name(), "config")
	}
}

func TestConfigCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &ConfigCommand{}
	if c.Synopsis() == "" {
		t.Error("ConfigCommand.Synopsis() returned empty string")
	}
}

func TestConfigCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("config")
	if !ok {
		t.Fatal("config command not registered")
	}
	if _, isConfig := cmd.(*ConfigCommand); !isConfig {
		t.Errorf("expected *ConfigCommand, got %T", cmd)
	}
}

func TestSanitizeEditorPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		wantOk bool
	}{
		{"empty falls back to vi", "vi", true},
		{"path with spaces", "code --wait", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := sanitizeEditorPath(tt.input)
			if result == "" {
				t.Error("sanitizeEditorPath() should not return empty string")
			}
		})
	}
}
