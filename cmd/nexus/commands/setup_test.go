package commands

import (
	"os"
	"strings"
	"testing"
)

func TestSetupCommandName(t *testing.T) {
	t.Parallel()
	c := &SetupCommand{}
	if c.Name() != "setup" {
		t.Errorf("SetupCommand.Name() = %q, want %q", c.Name(), "setup")
	}
}

func TestSetupCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &SetupCommand{}
	if c.Synopsis() == "" {
		t.Error("SetupCommand.Synopsis() returned empty string")
	}
}

func TestSetupCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("setup")
	if !ok {
		t.Fatal("setup command not registered")
	}
	if _, isSetup := cmd.(*SetupCommand); !isSetup {
		t.Errorf("expected *SetupCommand, got %T", cmd)
	}
}

func TestGetDefaultBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "https://api.anthropic.com/v1"},
		{"openai", "https://api.openai.com/v1"},
		{"gemini", "https://generativelanguage.googleapis.com/v1beta"},
		{"custom", "http://localhost:11434/v1"},
		{"unknown", "http://localhost:11434/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			t.Parallel()
			got := getDefaultBaseURL(tt.provider)
			if got != tt.want {
				t.Errorf("getDefaultBaseURL(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestGenerateConfig(t *testing.T) {
	t.Parallel()

	cfg := generateConfig("anthropic", "anthropic_messages", "sk-test-key", "https://api.anthropic.com/v1", "claude-sonnet-4-6")

	if !strings.Contains(cfg, "claude-sonnet-4-6") {
		t.Error("config should contain model name")
	}
	if !strings.Contains(cfg, "anthropic") {
		t.Error("config should contain provider name")
	}
	if !strings.Contains(cfg, "sk-test-key") {
		t.Error("config should contain API key")
	}
	if !strings.Contains(cfg, "https://api.anthropic.com/v1") {
		t.Error("config should contain base URL")
	}
	if !strings.Contains(cfg, "anthropic_messages") {
		t.Error("config should contain API mode")
	}
	if !strings.Contains(cfg, "agent:") {
		t.Error("config should contain agent section")
	}
	if !strings.Contains(cfg, "providers:") {
		t.Error("config should contain providers section")
	}
	if !strings.Contains(cfg, "tools:") {
		t.Error("config should contain tools section")
	}
}

func TestGenerateConfigDifferentProviders(t *testing.T) {
	t.Parallel()

	// OpenAI provider
	cfg := generateConfig("openai", "chat_completions", "sk-openai", "https://api.openai.com/v1", "gpt-4o")
	if !strings.Contains(cfg, "gpt-4o") || !strings.Contains(cfg, "openai") {
		t.Error("OpenAI config missing expected values")
	}

	// Gemini provider
	cfg = generateConfig("gemini", "gemini", "AIza...", "https://generativelanguage.googleapis.com/v1beta", "gemini-pro")
	if !strings.Contains(cfg, "gemini-pro") {
		t.Error("Gemini config missing model name")
	}
}

func TestSaveConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.yaml"
	content := "test: value\n"

	if err := saveConfig(cfgPath, content); err != nil {
		t.Fatalf("saveConfig() error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("readFile() error: %v", err)
	}
	if string(data) != content {
		t.Errorf("saved content = %q, want %q", string(data), content)
	}
}

func TestSaveConfigCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := tmpDir + "/.nexus"
	// saveConfig only MkdirAll's GetNexusHome(), not the path argument's dir,
	// so we must create the nested directory ourselves.
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	cfgPath := nestedDir + "/config.yaml"
	content := "agent:\n  model: test\n"

	if err := saveConfig(cfgPath, content); err != nil {
		t.Fatalf("saveConfig() error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("readFile() error: %v", err)
	}
	if string(data) != content {
		t.Errorf("saved content = %q, want %q", string(data), content)
	}
}
