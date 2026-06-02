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
	if chErr := os.Chdir(dir); chErr != nil {
		t.Fatal(chErr)
	}
	defer os.Chdir(oldWd)

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
	if chErr := os.Chdir(dir); chErr != nil {
		t.Fatal(chErr)
	}
	defer os.Chdir(oldWd)

	content := `
agent:
  model: claude-3.5
  max_tokens: 2048
`
	if wErr := os.WriteFile("config.yaml", []byte(content), 0644); wErr != nil {
		t.Fatal(wErr)
	}
	defer os.Remove("config.yaml")

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
	if chErr := os.Chdir(workDir); chErr != nil {
		t.Fatal(chErr)
	}
	defer os.Chdir(oldWd)

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

func TestExpandEnv_NoExpansionNeeded(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderConfig{
			"test": {
				APIKey:  "plain-key",
				BaseURL: "https://api.example.com",
			},
		},
	}

	expandEnv(cfg)

	if cfg.Providers["test"].APIKey != "plain-key" {
		t.Errorf("APIKey = %q, want plain-key", cfg.Providers["test"].APIKey)
	}
}

func TestExpandEnvString_Plain(t *testing.T) {
	result := expandEnvString("hello world")
	if result != "hello world" {
		t.Errorf("expandEnvString('hello world') = %q, want 'hello world'", result)
	}
}

func TestExpandEnvString_WithVar(t *testing.T) {
	t.Setenv("MY_VAR", "value123")
	result := expandEnvString("prefix-${MY_VAR}-suffix")
	if result != "prefix-value123-suffix" {
		t.Errorf("expandEnvString = %q, want prefix-value123-suffix", result)
	}
}

// ───────────────────────────── ResolveProvider ─────────────────────────────

func TestResolveProvider(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderConfig{
			"openai": {BaseURL: "https://api.openai.com/v1"},
		},
	}

	p, err := cfg.ResolveProvider("openai")
	if err != nil {
		t.Fatal(err)
	}
	if p.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q", p.BaseURL)
	}

	_, err = cfg.ResolveProvider("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent provider")
	}
}

// ───────────────────────────── ResolveModel ─────────────────────────────

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

// ───────────────────────────── 完整 YAML 覆盖测试 ─────────────────────────────

func TestLoad_FullConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
agent:
  model: claude-3.5-sonnet
  provider: anthropic
  max_tokens: 16000
  max_iterations: 100
  fallback_model: claude-haiku
  tool_delay: 0.5
  save_trajectory: true
  proxy: "http://proxy.example.com:8080"
  fallback_model_cfg:
    provider: anthropic
    model: claude-haiku
    base_url: "https://fallback.example.com"
    api_key: "fallback-key"
  fallback_chain:
    - provider: openai
      model: gpt-4o
      priority: 1
      base_url: "https://api.openai.com/v1"
      api_key: "chain-key"
    - provider: google
      model: gemini-pro
      priority: 2
providers:
  anthropic:
    base_url: "https://api.anthropic.com"
    api_key: "sk-ant-xxx"
    api_mode: anthropic_messages
    oauth_url: "https://oauth.anthropic.com"
  openai:
    base_url: "https://api.openai.com/v1"
    api_key: "sk-openai-xxx"
models:
  claude-3.5-sonnet:
    context_limit: 200000
    max_output: 8192
    vision: true
    reasoning: true
    provider: anthropic
gateway:
  enabled: true
  platforms:
    - platform: telegram
      enabled: true
      token: "tg-bot-token"
      settings:
        chat_id: "12345"
    - platform: discord
      enabled: false
      token: "discord-token"
  cache:
    max_size: 256
    idle_ttl: 30m
  stream:
    enabled: true
    buffer_size: 200
    edit_interval: 2s
tools:
  enabled_toolsets: ["fs", "web"]
  disabled_toolsets: ["dangerous"]
  result_max_chars: 80000
  browser_path: /usr/bin/chromium
  web_search_backend: exa
memory:
  memory_max_chars: 3000
  user_max_chars: 2000
  external_provider: memory-plugin
skills:
  disabled: ["skill-a"]
  external_dirs: ["/opt/skills"]
cron:
  enabled: true
  max_parallel_jobs: 5
  tick_interval_secs: 30
logging:
  level: debug
  format: json
  dir: /var/log/nexus
approval:
  mode: always
  cron_mode: off
  allowlist: ["echo *"]
  blocklist: ["rm -rf *"]
sandbox:
  backend: docker
  default_shell: /bin/zsh
  docker_image: nexus:latest
  ssh_host: remote.example.com
  ssh_user: deploy
  timeout_secs: 60
mcp:
  enabled: true
  servers:
    filesystem:
      command: mcp-fs
      args: ["--root", "/data"]
      env: ["FS_ROOT=/data"]
credentials:
  selection: round_robin
  fallback_key: BACKUP_API_KEY
plugins:
  enabled: true
  dirs: ["/opt/nexus/plugins"]
insights:
  enabled: true
trajectory:
  enabled: true
  dir: /data/trajectories
  format: openai
redact:
  enabled: false
  patterns: ["secret_\\w+"]
batch:
  max_workers: 8
  checkpoint_interval: 600
url_safety:
  allow_private_urls: false
  blocked_ips: ["10.0.0.1"]
tool_output:
  max_bytes: 100000
  max_lines: 5000
  max_line_length: 4000
shell_hooks:
  enabled: true
  accept_hooks: true
`
	if wErr := os.WriteFile(path, []byte(content), 0644); wErr != nil {
		t.Fatal(wErr)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load full config: %v", err)
	}

	if cfg.Agent.Model != "claude-3.5-sonnet" {
		t.Errorf("Agent.Model = %q", cfg.Agent.Model)
	}
	if cfg.Agent.FallbackModel != "claude-haiku" {
		t.Errorf("Agent.FallbackModel = %q", cfg.Agent.FallbackModel)
	}
	if cfg.Agent.ToolDelay != 0.5 {
		t.Errorf("Agent.ToolDelay = %f", cfg.Agent.ToolDelay)
	}
	if !cfg.Agent.SaveTrajectory {
		t.Error("Agent.SaveTrajectory should be true")
	}
	if cfg.Agent.Proxy != "http://proxy.example.com:8080" {
		t.Errorf("Agent.Proxy = %q", cfg.Agent.Proxy)
	}
	if cfg.Agent.FallbackModelCfg == nil {
		t.Fatal("Agent.FallbackModelCfg should not be nil")
	}
	if cfg.Agent.FallbackModelCfg.Provider != "anthropic" {
		t.Errorf("FallbackModelCfg.Provider = %q", cfg.Agent.FallbackModelCfg.Provider)
	}
	if cfg.Agent.FallbackModelCfg.APIKey != "fallback-key" {
		t.Errorf("FallbackModelCfg.APIKey = %q", cfg.Agent.FallbackModelCfg.APIKey)
	}
	if len(cfg.Agent.FallbackChain) != 2 {
		t.Fatalf("FallbackChain len = %d, want 2", len(cfg.Agent.FallbackChain))
	}
	if cfg.Agent.FallbackChain[0].Provider != "openai" {
		t.Errorf("FallbackChain[0].Provider = %q", cfg.Agent.FallbackChain[0].Provider)
	}
	if cfg.Agent.FallbackChain[0].Priority != 1 {
		t.Errorf("FallbackChain[0].Priority = %d", cfg.Agent.FallbackChain[0].Priority)
	}
	if cfg.Agent.FallbackChain[0].APIKey != "chain-key" {
		t.Errorf("FallbackChain[0].APIKey = %q", cfg.Agent.FallbackChain[0].APIKey)
	}

	if cfg.Providers["anthropic"].APIMode != "anthropic_messages" {
		t.Errorf("Providers[anthropic].APIMode = %q", cfg.Providers["anthropic"].APIMode)
	}
	if cfg.Providers["anthropic"].OAuthURL != "https://oauth.anthropic.com" {
		t.Errorf("Providers[anthropic].OAuthURL = %q", cfg.Providers["anthropic"].OAuthURL)
	}

	if !cfg.Models["claude-3.5-sonnet"].Reasoning {
		t.Error("Models[claude-3.5-sonnet].Reasoning should be true")
	}
	if cfg.Models["claude-3.5-sonnet"].Provider != "anthropic" {
		t.Errorf("Models[claude-3.5-sonnet].Provider = %q", cfg.Models["claude-3.5-sonnet"].Provider)
	}

	if !cfg.Gateway.Enabled {
		t.Error("Gateway.Enabled should be true")
	}
	if len(cfg.Gateway.Platforms) != 2 {
		t.Fatalf("Gateway.Platforms len = %d, want 2", len(cfg.Gateway.Platforms))
	}
	if cfg.Gateway.Platforms[0].Token != "tg-bot-token" {
		t.Errorf("Gateway.Platforms[0].Token = %q", cfg.Gateway.Platforms[0].Token)
	}
	if cfg.Gateway.Cache.MaxSize != 256 {
		t.Errorf("Gateway.Cache.MaxSize = %d", cfg.Gateway.Cache.MaxSize)
	}
	if cfg.Gateway.Stream.BufferSize != 200 {
		t.Errorf("Gateway.Stream.BufferSize = %d", cfg.Gateway.Stream.BufferSize)
	}

	if len(cfg.Tools.EnabledToolsets) != 2 {
		t.Errorf("Tools.EnabledToolsets len = %d", len(cfg.Tools.EnabledToolsets))
	}
	if cfg.Tools.BrowserPath != "/usr/bin/chromium" {
		t.Errorf("Tools.BrowserPath = %q", cfg.Tools.BrowserPath)
	}
	if cfg.Tools.WebSearchBackend != "exa" {
		t.Errorf("Tools.WebSearchBackend = %q", cfg.Tools.WebSearchBackend)
	}

	if cfg.Memory.ExternalProvider != "memory-plugin" {
		t.Errorf("Memory.ExternalProvider = %q", cfg.Memory.ExternalProvider)
	}

	if !cfg.Cron.Enabled {
		t.Error("Cron.Enabled should be true")
	}
	if cfg.Cron.TickIntervalSecs != 30 {
		t.Errorf("Cron.TickIntervalSecs = %d", cfg.Cron.TickIntervalSecs)
	}

	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q", cfg.Logging.Level)
	}
	if cfg.Logging.Dir != "/var/log/nexus" {
		t.Errorf("Logging.Dir = %q", cfg.Logging.Dir)
	}

	if cfg.Approval.CronMode != "off" {
		t.Errorf("Approval.CronMode = %q", cfg.Approval.CronMode)
	}
	if len(cfg.Approval.Allowlist) != 1 {
		t.Errorf("Approval.Allowlist len = %d", len(cfg.Approval.Allowlist))
	}
	if len(cfg.Approval.Blocklist) != 1 {
		t.Errorf("Approval.Blocklist len = %d", len(cfg.Approval.Blocklist))
	}

	if cfg.Sandbox.DefaultShell != "/bin/zsh" {
		t.Errorf("Sandbox.DefaultShell = %q", cfg.Sandbox.DefaultShell)
	}
	if cfg.Sandbox.DockerImage != "nexus:latest" {
		t.Errorf("Sandbox.DockerImage = %q", cfg.Sandbox.DockerImage)
	}
	if cfg.Sandbox.SSHHost != "remote.example.com" {
		t.Errorf("Sandbox.SSHHost = %q", cfg.Sandbox.SSHHost)
	}
	if cfg.Sandbox.SSHUser != "deploy" {
		t.Errorf("Sandbox.SSHUser = %q", cfg.Sandbox.SSHUser)
	}

	if !cfg.MCP.Enabled {
		t.Error("MCP.Enabled should be true")
	}
	if cfg.MCP.Servers["filesystem"].Command != "mcp-fs" {
		t.Errorf("MCP.Servers[filesystem].Command = %q", cfg.MCP.Servers["filesystem"].Command)
	}
	if len(cfg.MCP.Servers["filesystem"].Args) != 2 {
		t.Errorf("MCP.Servers[filesystem].Args len = %d", len(cfg.MCP.Servers["filesystem"].Args))
	}

	if cfg.Credentials.Selection != "round_robin" {
		t.Errorf("Credentials.Selection = %q", cfg.Credentials.Selection)
	}
	if cfg.Credentials.FallbackKey != "BACKUP_API_KEY" {
		t.Errorf("Credentials.FallbackKey = %q", cfg.Credentials.FallbackKey)
	}

	if !cfg.Plugins.Enabled {
		t.Error("Plugins.Enabled should be true")
	}
	if !cfg.Insights.Enabled {
		t.Error("Insights.Enabled should be true")
	}
	if !cfg.Trajectory.Enabled {
		t.Error("Trajectory.Enabled should be true")
	}
	if cfg.Trajectory.Format != "openai" {
		t.Errorf("Trajectory.Format = %q", cfg.Trajectory.Format)
	}
	if cfg.Redact.Enabled {
		t.Error("Redact.Enabled should be false")
	}
	if len(cfg.Redact.Patterns) != 1 {
		t.Errorf("Redact.Patterns len = %d", len(cfg.Redact.Patterns))
	}
	if cfg.Batch.MaxWorkers != 8 {
		t.Errorf("Batch.MaxWorkers = %d", cfg.Batch.MaxWorkers)
	}
	if cfg.URLSafety.AllowPrivateURLs {
		t.Error("URLSafety.AllowPrivateURLs should be false")
	}
	if cfg.ToolOutput.MaxBytes != 100000 {
		t.Errorf("ToolOutput.MaxBytes = %d", cfg.ToolOutput.MaxBytes)
	}
	if !cfg.ShellHooks.Enabled {
		t.Error("ShellHooks.Enabled should be true")
	}
}

func TestLoad_WithEnvExpansion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	t.Setenv("EXPAND_TEST_KEY", "expanded-key-999")
	t.Setenv("EXPAND_TEST_URL", "https://expanded.example.com")

	content := `
agent:
  proxy: "${EXPAND_TEST_URL}"
providers:
  test:
    api_key: "${EXPAND_TEST_KEY}"
    base_url: "${EXPAND_TEST_URL}"
    oauth_url: "${EXPAND_TEST_URL}"
gateway:
  platforms:
    - platform: telegram
      token: "${EXPAND_TEST_KEY}"
`
	if wErr := os.WriteFile(path, []byte(content), 0644); wErr != nil {
		t.Fatal(wErr)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load with env expansion: %v", err)
	}

	if cfg.Providers["test"].APIKey != "expanded-key-999" {
		t.Errorf("Provider APIKey = %q, want expanded", cfg.Providers["test"].APIKey)
	}
	if cfg.Providers["test"].BaseURL != "https://expanded.example.com" {
		t.Errorf("Provider BaseURL = %q", cfg.Providers["test"].BaseURL)
	}
	if cfg.Providers["test"].OAuthURL != "https://expanded.example.com" {
		t.Errorf("Provider OAuthURL = %q", cfg.Providers["test"].OAuthURL)
	}
	if cfg.Agent.Proxy != "https://expanded.example.com" {
		t.Errorf("Agent.Proxy = %q", cfg.Agent.Proxy)
	}
	if len(cfg.Gateway.Platforms) > 0 && cfg.Gateway.Platforms[0].Token != "expanded-key-999" {
		t.Errorf("Platform Token = %q, want expanded", cfg.Gateway.Platforms[0].Token)
	}
}

func TestExpandEnv_FallbackChainWithEnvExpansion(t *testing.T) {
	t.Setenv("FB_CHAIN_KEY", "chain-env-key")
	t.Setenv("FB_CHAIN_URL", "https://chain.example.com")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
agent:
  fallback_chain:
    - provider: fb
      model: m1
      api_key: "${FB_CHAIN_KEY}"
      base_url: "${FB_CHAIN_URL}"
  fallback_model_cfg:
    provider: legacy
    model: old
    api_key: "${FB_CHAIN_KEY}"
    base_url: "${FB_CHAIN_URL}"
`
	if wErr := os.WriteFile(path, []byte(content), 0644); wErr != nil {
		t.Fatal(wErr)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Agent.FallbackChain[0].APIKey != "chain-env-key" {
		t.Errorf("FallbackChain APIKey = %q, want chain-env-key", cfg.Agent.FallbackChain[0].APIKey)
	}
	if cfg.Agent.FallbackChain[0].BaseURL != "https://chain.example.com" {
		t.Errorf("FallbackChain BaseURL = %q", cfg.Agent.FallbackChain[0].BaseURL)
	}
	if cfg.Agent.FallbackModelCfg.APIKey != "chain-env-key" {
		t.Errorf("FallbackModelCfg APIKey = %q", cfg.Agent.FallbackModelCfg.APIKey)
	}
	if cfg.Agent.FallbackModelCfg.BaseURL != "https://chain.example.com" {
		t.Errorf("FallbackModelCfg BaseURL = %q", cfg.Agent.FallbackModelCfg.BaseURL)
	}
}
