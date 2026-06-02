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

// ───────────────────────── options.go ─────────────────────────

func TestWithMaxTokens(t *testing.T) {
	a := NewAgent(WithMaxTokens(2048))
	if a.maxTokens != 2048 {
		t.Errorf("maxTokens = %d, want 2048", a.maxTokens)
	}
}

func TestWithReasoningConfig(t *testing.T) {
	cfg := &ReasoningConfig{Effort: "high", BudgetTokens: 8000, Enabled: true}
	a := NewAgent(WithReasoningConfig(cfg))
	if a.reasoningCfg == nil || a.reasoningCfg.Effort != "high" {
		t.Error("reasoningCfg not set correctly")
	}
}

func TestWithReasoningConfig_Nil(t *testing.T) {
	a := NewAgent(WithReasoningConfig(nil))
	if a.reasoningCfg != nil {
		t.Error("nil ReasoningConfig should remain nil")
	}
}

func TestWithStreamCallback(t *testing.T) {
	called := false
	fn := func(delta string) { called = true }
	a := NewAgent(WithStreamCallback(fn))
	if a.streamCallback == nil {
		t.Fatal("streamCallback is nil")
	}
	a.streamCallback("test")
	if !called {
		t.Error("streamCallback was not called")
	}
}

func TestWithToolCallback(t *testing.T) {
	called := false
	fn := func(name string, args map[string]any) { called = true }
	a := NewAgent(WithToolCallback(fn))
	if a.toolCallback == nil {
		t.Fatal("toolCallback is nil")
	}
	a.toolCallback("test", nil)
	if !called {
		t.Error("toolCallback was not called")
	}
}

func TestWithStatusCallback(t *testing.T) {
	called := false
	fn := func(msg string) { called = true }
	a := NewAgent(WithStatusCallback(fn))
	if a.statusCallback == nil {
		t.Fatal("statusCallback is nil")
	}
	a.statusCallback("test")
	if !called {
		t.Error("statusCallback was not called")
	}
}

func TestWithReasoningCallback(t *testing.T) {
	called := false
	fn := func(reasoning string) { called = true }
	a := NewAgent(WithReasoningCallback(fn))
	if a.reasoningCallback == nil {
		t.Fatal("reasoningCallback is nil")
	}
	a.reasoningCallback("thinking")
	if !called {
		t.Error("reasoningCallback was not called")
	}
}

func TestWithClarifyCallback(t *testing.T) {
	fn := func(question string, choices []string) string { return "yes" }
	NewAgent(WithClarifyCallback(fn))
}

func TestWithMemoryManager(t *testing.T) {
	a := NewAgent(WithMemoryManager(nil))
	if a.memoryManager != nil {
		t.Error("nil memoryManager should remain nil")
	}
}

func TestWithSkillManager(t *testing.T) {
	a := NewAgent(WithSkillManager(nil))
	if a.skillManager != nil {
		t.Error("nil skillManager should remain nil")
	}
}

func TestWithContextBuilder(t *testing.T) {
	a := NewAgent(WithContextBuilder(nil))
	if a.contextBuilder != nil {
		t.Error("nil contextBuilder should remain nil")
	}
}

func TestWithStateStore(t *testing.T) {
	a := NewAgent(WithStateStore(nil))
	if a.state != nil {
		t.Error("nil state should remain nil")
	}
}

func TestWithSessionPersister(t *testing.T) {
	a := NewAgent(WithSessionPersister(nil))
	if a.persister != nil {
		t.Error("nil persister should remain nil")
	}
}

func TestWithCredentialPool(t *testing.T) {
	a := NewAgent(WithCredentialPool(nil))
	if a.credentialPool != nil {
		t.Error("nil credentialPool should remain nil")
	}
}

func TestWithResumeSession(t *testing.T) {
	a := NewAgent(WithResumeSession("sess-123"))
	if a.sessionID != "sess-123" {
		t.Errorf("sessionID = %q, want sess-123", a.sessionID)
	}
	if !a.resumeMode {
		t.Error("resumeMode should be true")
	}
}

func TestWithSandboxEnv(t *testing.T) {
	a := NewAgent(WithSandboxEnv(nil))
	if a.sandboxEnv != nil {
		t.Error("nil sandboxEnv should remain nil")
	}
}

func TestWithAllowedRoot(t *testing.T) {
	a := NewAgent(WithAllowedRoot("/tmp/test"))
	if a.fileSafety == nil {
		t.Fatal("fileSafety should be created")
	}
}

func TestWithAllowedRoot_Empty(t *testing.T) {
	a := NewAgent(WithAllowedRoot(""))
	if a.fileSafety != nil {
		t.Error("empty root should not create fileSafety")
	}
}

func TestWithPlatform(t *testing.T) {
	a := NewAgent(WithPlatform("telegram"))
	if a.platform != "telegram" {
		t.Errorf("platform = %q, want telegram", a.platform)
	}
}

func TestWithUserID(t *testing.T) {
	a := NewAgent(WithUserID("user-1"))
	if a.userID != "user-1" {
		t.Errorf("userID = %q, want user-1", a.userID)
	}
}

func TestWithChatID(t *testing.T) {
	a := NewAgent(WithChatID("chat-1"))
	if a.chatID != "chat-1" {
		t.Errorf("chatID = %q, want chat-1", a.chatID)
	}
}

// ───────────────────────── Set* 回调设置器 ─────────────────────────

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
		Agent:    config.AgentConfig{Model: "test"},
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
