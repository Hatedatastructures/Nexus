package agent

import (
	"testing"
)

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
