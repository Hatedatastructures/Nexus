package agent

import (
	"testing"

	"nexus-agent/internal/config"
)

// ───────────────────────── Set* callback setters ─────────────────────────

func TestSetStreamCallback(t *testing.T) {
	a := NewAgent()
	called := false
	a.SetStreamCallback(func(delta string) { called = true })
	if a.streamCallback == nil {
		t.Fatal("streamCallback is nil after SetStreamCallback")
	}
	a.streamCallback("x")
	if !called {
		t.Error("callback not invoked")
	}
}

func TestSetToolCallback(t *testing.T) {
	a := NewAgent()
	called := false
	a.SetToolCallback(func(name string, args map[string]any) { called = true })
	if a.toolCallback == nil {
		t.Fatal("toolCallback is nil after SetToolCallback")
	}
	a.toolCallback("x", nil)
	if !called {
		t.Error("callback not invoked")
	}
}

func TestSetStatusCallback(t *testing.T) {
	a := NewAgent()
	called := false
	a.SetStatusCallback(func(msg string) { called = true })
	if a.statusCallback == nil {
		t.Fatal("statusCallback is nil after SetStatusCallback")
	}
	a.statusCallback("x")
	if !called {
		t.Error("callback not invoked")
	}
}

func TestSetReasoningCallback(t *testing.T) {
	a := NewAgent()
	called := false
	a.SetReasoningCallback(func(reasoning string) { called = true })
	if a.reasoningCallback == nil {
		t.Fatal("reasoningCallback is nil after SetReasoningCallback")
	}
	a.reasoningCallback("think")
	if !called {
		t.Error("callback not invoked")
	}
}

func TestSetClarifyCallback(t *testing.T) {
	a := NewAgent()
	a.SetClarifyCallback(func(question string, choices []string) string { return "" })
}

// ───────────────────────── WithConfig ─────────────────────────

func TestWithConfig(t *testing.T) {
	cfg := &config.AgentConfig{
		Model:         "gpt-4o",
		MaxTokens:     4096,
		MaxIterations: 30,
		FallbackModel: "gpt-3.5",
		FallbackChain: []config.FallbackEntryConfig{
			{Provider: "backup", Model: "m1", Priority: 1},
		},
	}
	a := NewAgent(WithConfig(cfg))
	if a.model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", a.model)
	}
	if a.maxTokens != 4096 {
		t.Errorf("maxTokens = %d, want 4096", a.maxTokens)
	}
	if a.fallbackModel != "gpt-3.5" {
		t.Errorf("fallbackModel = %q, want gpt-3.5", a.fallbackModel)
	}
	if len(a.pendingFallbackChain) != 1 {
		t.Errorf("pendingFallbackChain = %d entries, want 1", len(a.pendingFallbackChain))
	}
}

func TestWithConfig_PartialFields(t *testing.T) {
	cfg := &config.AgentConfig{
		Model: "gpt-4",
	}
	a := NewAgent(WithConfig(cfg))
	if a.model != "gpt-4" {
		t.Errorf("model = %q, want gpt-4", a.model)
	}
	if a.maxTokens != 4096 {
		t.Errorf("maxTokens should remain default 4096, got %d", a.maxTokens)
	}
}

// ───────────────────────── WithConfigProvider ─────────────────────────

func TestWithConfigProvider_OpenAI(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			Model:    "gpt-4",
			Provider: "openai",
		},
		Providers: map[string]config.ProviderConfig{
			"openai": {
				APIKey:  "test-key",
				APIMode: "openai",
				BaseURL: "https://api.openai.com/v1",
			},
		},
	}

	a := NewAgent(WithConfigProvider(cfg))
	if a.provider == nil {
		t.Fatal("provider should not be nil")
	}
}

func TestWithConfigProvider_Anthropic(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			Model:    "claude-3",
			Provider: "anthropic",
		},
		Providers: map[string]config.ProviderConfig{
			"anthropic": {
				APIKey:  "test-key",
				APIMode: "anthropic",
				BaseURL: "https://api.anthropic.com",
			},
		},
	}

	a := NewAgent(WithConfigProvider(cfg))
	if a.provider == nil {
		t.Fatal("provider should not be nil")
	}
}

func TestWithConfigProvider_Gemini(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			Model:    "gemini-pro",
			Provider: "google",
		},
		Providers: map[string]config.ProviderConfig{
			"google": {
				APIKey:  "test-key",
				APIMode: "gemini",
				BaseURL: "https://generativelanguage.googleapis.com",
			},
		},
	}

	a := NewAgent(WithConfigProvider(cfg))
	if a.provider == nil {
		t.Fatal("provider should not be nil")
	}
}

func TestWithConfigProvider_Bedrock(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			Provider: "bedrock",
		},
		Providers: map[string]config.ProviderConfig{
			"bedrock": {
				APIKey:  "test-key",
				APIMode: "bedrock",
			},
		},
	}

	a := NewAgent(WithConfigProvider(cfg))
	if a.provider == nil {
		t.Fatal("provider should not be nil")
	}
}

func TestWithConfigProvider_DefaultOpenAI(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			Provider: "custom",
		},
		Providers: map[string]config.ProviderConfig{
			"custom": {
				APIKey:  "test-key",
				APIMode: "unknown_mode",
				BaseURL: "https://custom.api.com/v1",
			},
		},
	}

	a := NewAgent(WithConfigProvider(cfg))
	if a.provider == nil {
		t.Fatal("unknown api_mode should default to openai provider")
	}
}

func TestWithConfigProvider_NoProviderConfig(t *testing.T) {
	cfg := &config.Config{
		Agent:     config.AgentConfig{Model: "test"},
		Providers: map[string]config.ProviderConfig{},
	}

	a := NewAgent(WithConfigProvider(cfg))
	if a.provider != nil {
		t.Error("provider should be nil when no providers configured")
	}
}

func TestWithConfigProvider_ResolveByName(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			Model: "gpt-4",
		},
		Models: map[string]config.ModelConfig{
			"gpt-4": {Provider: "openai"},
		},
		Providers: map[string]config.ProviderConfig{
			"openai": {APIKey: "test-key", APIMode: "openai"},
		},
	}

	a := NewAgent(WithConfigProvider(cfg))
	if a.provider == nil {
		t.Fatal("provider should be resolved from model config")
	}
}

func TestWithConfigProvider_ModelOverride(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			Model:    "claude-3",
			Provider: "anthropic",
		},
		Providers: map[string]config.ProviderConfig{
			"anthropic": {APIKey: "key", APIMode: "anthropic"},
		},
	}

	a := NewAgent(WithModel("original"), WithConfigProvider(cfg))
	if a.model != "claude-3" {
		t.Errorf("model = %q, want claude-3 (overridden by config)", a.model)
	}
}

// ───────────────────────── buildProviderFromConfig ─────────────────────────

func TestBuildProviderFromConfig_NoAPIKey(t *testing.T) {
	_, err := buildProviderFromConfig("test", config.ProviderConfig{})
	if err == nil {
		t.Error("expected error when APIKey is empty")
	}
}
