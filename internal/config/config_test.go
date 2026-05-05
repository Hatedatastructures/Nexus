package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()

	if cfg.Agent.MaxIterations != 90 {
		t.Errorf("MaxIterations = %d, want 90", cfg.Agent.MaxIterations)
	}
	if cfg.Agent.MaxTokens != 8000 {
		t.Errorf("MaxTokens = %d, want 8000", cfg.Agent.MaxTokens)
	}
	if cfg.Tools.ResultMaxChars != 50000 {
		t.Errorf("ResultMaxChars = %d, want 50000", cfg.Tools.ResultMaxChars)
	}
	if cfg.Memory.MemoryMaxChars != 2200 {
		t.Errorf("MemoryMaxChars = %d, want 2200", cfg.Memory.MemoryMaxChars)
	}
	if cfg.Cron.MaxParallelJobs != 3 {
		t.Errorf("MaxParallelJobs = %d, want 3", cfg.Cron.MaxParallelJobs)
	}
	if cfg.Approval.Mode != "smart" {
		t.Errorf("Approval.Mode = %q, want smart", cfg.Approval.Mode)
	}
	if cfg.Sandbox.Backend != "local" {
		t.Errorf("Sandbox.Backend = %q, want local", cfg.Sandbox.Backend)
	}
	if cfg.Redact.Enabled != true {
		t.Errorf("Redact.Enabled = %v, want true", cfg.Redact.Enabled)
	}
	if cfg.Trajectory.Format != "sharegpt" {
		t.Errorf("Trajectory.Format = %q, want sharegpt", cfg.Trajectory.Format)
	}
	if cfg.Batch.MaxWorkers != 4 {
		t.Errorf("Batch.MaxWorkers = %d, want 4", cfg.Batch.MaxWorkers)
	}
	if cfg.ToolOutput.MaxBytes != 50000 {
		t.Errorf("ToolOutput.MaxBytes = %d, want 50000", cfg.ToolOutput.MaxBytes)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
agent:
  model: gpt-4o
  provider: openai
  max_tokens: 4096
  max_iterations: 50
providers:
  openai:
    base_url: https://api.openai.com/v1
    api_key: test-key
tools:
  result_max_chars: 10000
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Agent.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", cfg.Agent.Model)
	}
	if cfg.Agent.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", cfg.Agent.Provider)
	}
	if cfg.Agent.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", cfg.Agent.MaxTokens)
	}
	if cfg.Agent.MaxIterations != 50 {
		t.Errorf("MaxIterations = %d, want 50", cfg.Agent.MaxIterations)
	}
	if cfg.Providers["openai"].BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q", cfg.Providers["openai"].BaseURL)
	}
	if cfg.Tools.ResultMaxChars != 10000 {
		t.Errorf("ResultMaxChars = %d, want 10000", cfg.Tools.ResultMaxChars)
	}
	// 默认值应该保留
	if cfg.Cron.MaxParallelJobs != 3 {
		t.Errorf("MaxParallelJobs = %d, want 3 (default)", cfg.Cron.MaxParallelJobs)
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("TEST_API_KEY", "sk-test-12345")
	t.Setenv("TEST_BASE_URL", "https://test.api.com/v1")

	cfg := &Config{
		Providers: map[string]ProviderConfig{
			"test": {
				APIKey:  "${TEST_API_KEY}",
				BaseURL: "${TEST_BASE_URL}",
			},
		},
	}

	expandEnv(cfg)

	if cfg.Providers["test"].APIKey != "sk-test-12345" {
		t.Errorf("APIKey = %q, want sk-test-12345", cfg.Providers["test"].APIKey)
	}
	if cfg.Providers["test"].BaseURL != "https://test.api.com/v1" {
		t.Errorf("BaseURL = %q", cfg.Providers["test"].BaseURL)
	}
}

func TestResolveProvider(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderConfig{
			"openai": {BaseURL: "https://api.openai.com/v1"},
		},
	}

	// 存在的提供者
	p, err := cfg.ResolveProvider("openai")
	if err != nil {
		t.Fatal(err)
	}
	if p.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q", p.BaseURL)
	}

	// 不存在的提供者
	_, err = cfg.ResolveProvider("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent provider")
	}
}

func TestResolveModel(t *testing.T) {
	cfg := &Config{
		Models: map[string]ModelConfig{
			"gpt-4o": {ContextLimit: 128000, MaxOutput: 4096, Vision: true},
		},
	}

	m, ok := cfg.ResolveModel("gpt-4o")
	if !ok {
		t.Fatal("expected to find gpt-4o")
	}
	if m.ContextLimit != 128000 {
		t.Errorf("ContextLimit = %d, want 128000", m.ContextLimit)
	}
	if !m.Vision {
		t.Error("expected Vision = true")
	}

	_, ok = cfg.ResolveModel("nonexistent")
	if ok {
		t.Error("expected false for nonexistent model")
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		// 某些实现会返回错误，这是可接受的
		return
	}
	// 如果不返回错误，应该返回默认配置
	if cfg.Agent.MaxIterations != 90 {
		t.Errorf("expected default MaxIterations = 90, got %d", cfg.Agent.MaxIterations)
	}
}
