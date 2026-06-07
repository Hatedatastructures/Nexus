package agent

import (
	"testing"

	"nexus-agent/internal/config"
	"nexus-agent/internal/llm"
)

// ───────────────────────── agent.go ─────────────────────────

func TestDefaultAgentFromConfig(t *testing.T) {
	cfg := &config.AgentConfig{
		Model:         "gpt-4",
		MaxTokens:     8192,
		MaxIterations: 50,
	}
	a := DefaultAgentFromConfig(cfg)
	if a == nil {
		t.Fatal("agent is nil")
	}
	if a.model != "gpt-4" {
		t.Errorf("model = %q, want gpt-4", a.model)
	}
	if a.maxTokens != 8192 {
		t.Errorf("maxTokens = %d, want 8192", a.maxTokens)
	}
}

func TestDefaultAgentFromConfig_Empty(t *testing.T) {
	cfg := &config.AgentConfig{}
	a := DefaultAgentFromConfig(cfg)
	if a == nil {
		t.Fatal("agent is nil")
	}
	if a.model != "claude-sonnet-4-20250514" {
		t.Errorf("model = %q, want default", a.model)
	}
}

func TestAgent_Provider(t *testing.T) {
	p := &mockRouterProvider{name: "test-provider"}
	a := NewAgent(WithProvider(p))
	got := a.Provider()
	if got == nil {
		t.Fatal("Provider() returned nil")
	}
	if got.Name() != "test-provider" {
		t.Errorf("Provider().Name() = %q, want test-provider", got.Name())
	}
}

func TestAgent_Provider_Nil(t *testing.T) {
	a := NewAgent()
	if a.Provider() != nil {
		t.Error("expected nil provider for default agent")
	}
}

func TestAgent_InitRouter(t *testing.T) {
	p := &mockRouterProvider{name: "r1"}
	entries := []*ProviderEntry{{Provider: p, Model: "m1", Priority: 1}}

	a := NewAgent()
	a.InitRouter(entries)

	r := a.Router()
	if r == nil {
		t.Fatal("Router() should not be nil after InitRouter")
	}
}

func TestAgent_InitRouter_Empty(t *testing.T) {
	a := NewAgent()
	a.InitRouter(nil)
	if a.Router() != nil {
		t.Error("Router() should be nil with empty entries")
	}
}

func TestAgent_InitFallbackChain(t *testing.T) {
	p := &mockRouterProvider{name: "fb"}
	providerMap := map[string]llm.Provider{"fb": p}
	pending := []config.FallbackEntryConfig{
		{Provider: "fb", Model: "m1", Priority: 1},
	}

	a := NewAgent(WithConfig(&config.AgentConfig{FallbackChain: pending}))
	a.InitFallbackChain(providerMap)

	fc := a.FallbackChain()
	if fc == nil {
		t.Fatal("FallbackChain() should not be nil after InitFallbackChain")
	}
	if len(fc.entries) != 1 {
		t.Errorf("entries = %d, want 1", len(fc.entries))
	}
}

func TestAgent_InitFallbackChain_Empty(t *testing.T) {
	a := NewAgent()
	a.InitFallbackChain(nil)
	if a.FallbackChain() != nil {
		t.Error("FallbackChain() should be nil with no pending config")
	}
}

func TestAgent_InitFallbackChain_ClearsPending(t *testing.T) {
	p := &mockRouterProvider{name: "fb"}
	providerMap := map[string]llm.Provider{"fb": p}
	pending := []config.FallbackEntryConfig{
		{Provider: "fb", Model: "m1", Priority: 1},
	}

	a := NewAgent(WithConfig(&config.AgentConfig{FallbackChain: pending}))
	a.InitFallbackChain(providerMap)

	// 再次调用应无操作 (pending 已清空)
	a.InitFallbackChain(providerMap)
	if len(a.FallbackChain().entries) != 1 {
		t.Error("second InitFallbackChain should not duplicate entries")
	}
}

func TestAgent_Router_Nil(t *testing.T) {
	a := NewAgent()
	if a.Router() != nil {
		t.Error("Router() should be nil by default")
	}
}

func TestAgent_FallbackChain_Nil(t *testing.T) {
	a := NewAgent()
	if a.FallbackChain() != nil {
		t.Error("FallbackChain() should be nil by default")
	}
}

func TestAgent_Shutdown(t *testing.T) {
	p := &mockRouterProvider{name: "test"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)

	a := NewAgent(WithRouter(r))
	a.messages = []llm.Message{{Role: llm.RoleUser, Content: "test"}}
	a.cachedSystemPrompt = "cached"

	a.Shutdown()

	if a.messages != nil {
		t.Error("messages should be nil after Shutdown")
	}
	if a.cachedSystemPrompt != "" {
		t.Error("cachedSystemPrompt should be empty after Shutdown")
	}
}

func TestAgent_Shutdown_NoRouter(t *testing.T) {
	a := NewAgent()
	a.Shutdown()
}

func TestAgent_Shutdown_Idempotent(t *testing.T) {
	p := &mockRouterProvider{name: "test"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)

	a := NewAgent(WithRouter(r))
	a.Shutdown()
	a.Shutdown()
}
