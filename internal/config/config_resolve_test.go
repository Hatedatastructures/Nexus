package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
