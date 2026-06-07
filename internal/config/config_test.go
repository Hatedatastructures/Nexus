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
	if cfg.Logging.Level != "info" {
		t.Errorf("Logging.Level = %q, want info", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "text" {
		t.Errorf("Logging.Format = %q, want text", cfg.Logging.Format)
	}
	if cfg.Approval.CronMode != "always" {
		t.Errorf("Approval.CronMode = %q, want always", cfg.Approval.CronMode)
	}
	if cfg.Sandbox.TimeoutSecs != 120 {
		t.Errorf("Sandbox.TimeoutSecs = %d, want 120", cfg.Sandbox.TimeoutSecs)
	}
	if cfg.Cron.TickIntervalSecs != 60 {
		t.Errorf("Cron.TickIntervalSecs = %d, want 60", cfg.Cron.TickIntervalSecs)
	}
	if cfg.Memory.UserMaxChars != 1375 {
		t.Errorf("Memory.UserMaxChars = %d, want 1375", cfg.Memory.UserMaxChars)
	}
	if cfg.Batch.CheckpointInterval != 300 {
		t.Errorf("Batch.CheckpointInterval = %d, want 300", cfg.Batch.CheckpointInterval)
	}
	if cfg.ToolOutput.MaxLines != 2000 {
		t.Errorf("ToolOutput.MaxLines = %d, want 2000", cfg.ToolOutput.MaxLines)
	}
	if cfg.ToolOutput.MaxLineLength != 2000 {
		t.Errorf("ToolOutput.MaxLineLength = %d, want 2000", cfg.ToolOutput.MaxLineLength)
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
	if cfg.Cron.MaxParallelJobs != 3 {
		t.Errorf("MaxParallelJobs = %d, want 3 (default)", cfg.Cron.MaxParallelJobs)
	}
}

func TestLoadEmptyPath_NoConfigAnywhere(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load('') without config file: %v", err)
	}
	if cfg.Agent.MaxIterations != 90 {
		t.Errorf("MaxIterations = %d, want 90 (default)", cfg.Agent.MaxIterations)
	}
	if cfg.Agent.MaxTokens != 8000 {
		t.Errorf("MaxTokens = %d, want 8000 (default)", cfg.Agent.MaxTokens)
	}
}

func TestLoadEmptyPath_ConfigInCwd(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	content := `
agent:
  model: claude-3.5
  max_tokens: 2048
`
	if wErr := os.WriteFile("config.yaml", []byte(content), 0644); wErr != nil {
		t.Fatal(wErr)
	}
	defer func() { _ = os.Remove("config.yaml") }()

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load('') with cwd config: %v", err)
	}
	if cfg.Agent.Model != "claude-3.5" {
		t.Errorf("Model = %q, want claude-3.5", cfg.Agent.Model)
	}
	if cfg.Agent.MaxTokens != 2048 {
		t.Errorf("MaxTokens = %d, want 2048", cfg.Agent.MaxTokens)
	}
}

func TestLoadEmptyPath_ConfigInHomeDir(t *testing.T) {
	dir := t.TempDir()
	nexusDir := filepath.Join(dir, ".nexus")
	if mkErr := os.MkdirAll(nexusDir, 0755); mkErr != nil {
		t.Fatal(mkErr)
	}

	content := `
agent:
  model: home-model
  max_iterations: 42
`
	configPath := filepath.Join(nexusDir, "config.yaml")
	if wErr := os.WriteFile(configPath, []byte(content), 0644); wErr != nil {
		t.Fatal(wErr)
	}

	workDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load('') with home config: %v", err)
	}
	if cfg.Agent.Model != "home-model" {
		t.Errorf("Model = %q, want home-model", cfg.Agent.Model)
	}
	if cfg.Agent.MaxIterations != 42 {
		t.Errorf("MaxIterations = %d, want 42", cfg.Agent.MaxIterations)
	}
}

func TestLoad_ParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
agent:
  model: [invalid
`
	if wErr := os.WriteFile(path, []byte(content), 0644); wErr != nil {
		t.Fatal(wErr)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoad_NonexistentExplicitPath(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file with explicit path")
	}
}

// ───────────────────────────── expandEnv 覆盖 ─────────────────────────────

func TestExpandEnv_ProviderAPIKeyAndBaseURL(t *testing.T) {
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

func TestExpandEnv_ProviderOAuthURL(t *testing.T) {
	t.Setenv("OAUTH_URL", "https://oauth.example.com/token")

	cfg := &Config{
		Providers: map[string]ProviderConfig{
			"xai": {
				OAuthURL: "${OAUTH_URL}",
			},
		},
	}

	expandEnv(cfg)

	if cfg.Providers["xai"].OAuthURL != "https://oauth.example.com/token" {
		t.Errorf("OAuthURL = %q, want https://oauth.example.com/token", cfg.Providers["xai"].OAuthURL)
	}
}

func TestExpandEnv_PlatformToken(t *testing.T) {
	t.Setenv("TG_TOKEN", "123456:ABC-DEF")

	cfg := &Config{
		Gateway: GatewayConfig{
			Platforms: []PlatformEntry{
				{Platform: "telegram", Token: "${TG_TOKEN}"},
				{Platform: "discord", Token: "static-token"},
			},
		},
	}

	expandEnv(cfg)

	if cfg.Gateway.Platforms[0].Token != "123456:ABC-DEF" {
		t.Errorf("Platform[0].Token = %q, want expanded", cfg.Gateway.Platforms[0].Token)
	}
	if cfg.Gateway.Platforms[1].Token != "static-token" {
		t.Errorf("Platform[1].Token = %q, want static-token", cfg.Gateway.Platforms[1].Token)
	}
}

func TestExpandEnv_AgentProxy(t *testing.T) {
	t.Setenv("PROXY_ADDR", "socks5://proxy.local:1080")

	cfg := &Config{
		Agent: AgentConfig{
			Proxy: "${PROXY_ADDR}",
		},
	}

	expandEnv(cfg)

	if cfg.Agent.Proxy != "socks5://proxy.local:1080" {
		t.Errorf("Proxy = %q, want socks5://proxy.local:1080", cfg.Agent.Proxy)
	}
}

func TestExpandEnv_FallbackChain(t *testing.T) {
	t.Setenv("FB_KEY", "fb-key-123")
	t.Setenv("FB_URL", "https://fb.example.com/v1")

	cfg := &Config{
		Agent: AgentConfig{
			FallbackChain: []FallbackEntryConfig{
				{
					Provider: "fallback-a",
					Model:    "model-a",
					APIKey:   "${FB_KEY}",
					BaseURL:  "${FB_URL}",
				},
			},
		},
	}

	expandEnv(cfg)

	if cfg.Agent.FallbackChain[0].APIKey != "fb-key-123" {
		t.Errorf("FallbackChain[0].APIKey = %q, want fb-key-123", cfg.Agent.FallbackChain[0].APIKey)
	}
	if cfg.Agent.FallbackChain[0].BaseURL != "https://fb.example.com/v1" {
		t.Errorf("FallbackChain[0].BaseURL = %q", cfg.Agent.FallbackChain[0].BaseURL)
	}
}

func TestExpandEnv_FallbackModelCfg(t *testing.T) {
	t.Setenv("LEGACY_KEY", "legacy-key-456")
	t.Setenv("LEGACY_URL", "https://legacy.example.com")

	cfg := &Config{
		Agent: AgentConfig{
			FallbackModelCfg: &FallbackModelConfig{
				Provider: "legacy",
				Model:    "old-model",
				APIKey:   "${LEGACY_KEY}",
				BaseURL:  "${LEGACY_URL}",
			},
		},
	}

	expandEnv(cfg)

	if cfg.Agent.FallbackModelCfg.APIKey != "legacy-key-456" {
		t.Errorf("FallbackModelCfg.APIKey = %q, want legacy-key-456", cfg.Agent.FallbackModelCfg.APIKey)
	}
	if cfg.Agent.FallbackModelCfg.BaseURL != "https://legacy.example.com" {
		t.Errorf("FallbackModelCfg.BaseURL = %q", cfg.Agent.FallbackModelCfg.BaseURL)
	}
}

func TestExpandEnv_UndefinedEnvVar(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderConfig{
			"test": {
				APIKey: "${NONEXISTENT_VAR_XYZ}",
			},
		},
	}

	expandEnv(cfg)

	if cfg.Providers["test"].APIKey != "" {
		t.Errorf("APIKey = %q, want empty for undefined env var", cfg.Providers["test"].APIKey)
	}
}

