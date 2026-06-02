package context

import (
	"testing"

	"nexus-agent/internal/llm"
)

// ---------------------------------------------------------------------------
// ApplyAnthropicCacheControl
// ---------------------------------------------------------------------------

func TestApplyAnthropicCacheControl(t *testing.T) {
	t.Parallel()

	t.Run("empty messages returns unchanged", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{}
		got := ApplyAnthropicCacheControl(msgs, 3)
		if len(got) != 0 {
			t.Errorf("expected empty, got %d messages", len(got))
		}
	})

	t.Run("zero cachePoints returns unchanged", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleSystem, Content: "system"},
		}
		got := ApplyAnthropicCacheControl(msgs, 0)
		if got[0].Content != "system" {
			t.Error("zero cachePoints should not modify messages")
		}
	})

	t.Run("caches system message", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleSystem, Content: "system prompt"},
			{Role: llm.RoleUser, Content: "hello"},
		}
		got := ApplyAnthropicCacheControl(msgs, 1)
		if !containsText(got[0].Content, "[cache_control:ephemeral]") {
			t.Error("system message should have cache_control marker")
		}
	})

	t.Run("caches last N non-system messages", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleSystem, Content: "system"},
			{Role: llm.RoleUser, Content: "first"},
			{Role: llm.RoleAssistant, Content: "second"},
			{Role: llm.RoleUser, Content: "third"},
		}
		got := ApplyAnthropicCacheControl(msgs, 2)
		// last 2 non-system: msg[2] (assistant) and msg[3] (user)
		if !containsText(got[3].Content, "[cache_control:ephemeral]") {
			t.Error("last user message should be cached")
		}
		if !containsText(got[2].Content, "[cache_control:ephemeral]") {
			t.Error("assistant message should be cached")
		}
	})

	t.Run("skips system in tail caching", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleSystem, Content: "sys"},
			{Role: llm.RoleUser, Content: "hello"},
		}
		got := ApplyAnthropicCacheControl(msgs, 5)
		// system is cached separately; tail loop skips system
		systemCacheCount := 0
		if containsText(got[0].Content, "[cache_control:ephemeral]") {
			systemCacheCount++
		}
		if systemCacheCount != 1 {
			t.Error("system should be cached exactly once")
		}
	})
}

// ---------------------------------------------------------------------------
// withCacheControl
// ---------------------------------------------------------------------------

func TestWithCacheControl(t *testing.T) {
	t.Parallel()

	t.Run("assistant with tool calls sets Extra on last ToolCall", func(t *testing.T) {
		t.Parallel()
		msg := llm.Message{
			Role:    llm.RoleAssistant,
			Content: "running tools",
			ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "read", Arguments: "{}"},
				{ID: "c2", Name: "write", Arguments: "{}"},
			},
		}
		got := withCacheControl(msg)
		if got.ToolCalls[1].Extra == nil {
			t.Error("last ToolCall should have Extra set")
		}
		if got.ToolCalls[0].Extra != nil {
			t.Error("first ToolCall should not have Extra set")
		}
	})

	t.Run("tool message sets default name", func(t *testing.T) {
		t.Parallel()
		msg := llm.Message{
			Role:       llm.RoleTool,
			Content:    "result",
			ToolCallID: "call_1",
		}
		got := withCacheControl(msg)
		if got.Name != "tool" {
			t.Errorf("Name = %q, want %q", got.Name, "tool")
		}
	})

	t.Run("tool message preserves existing name", func(t *testing.T) {
		t.Parallel()
		msg := llm.Message{
			Role:       llm.RoleTool,
			Content:    "result",
			ToolCallID: "call_1",
			Name:       "terminal",
		}
		got := withCacheControl(msg)
		if got.Name != "terminal" {
			t.Errorf("Name = %q, want %q", got.Name, "terminal")
		}
	})

	t.Run("tool message without ToolCallID falls through", func(t *testing.T) {
		t.Parallel()
		msg := llm.Message{
			Role:    llm.RoleTool,
			Content: "result",
		}
		got := withCacheControl(msg)
		if !containsText(got.Content, "[cache_control:ephemeral]") {
			t.Error("tool without ToolCallID should get text marker")
		}
	})

	t.Run("text message appends cache marker", func(t *testing.T) {
		t.Parallel()
		msg := llm.Message{
			Role:    llm.RoleUser,
			Content: "hello",
		}
		got := withCacheControl(msg)
		if !containsText(got.Content, "[cache_control:ephemeral]") {
			t.Error("text message should have cache marker")
		}
	})

	t.Run("empty content not modified", func(t *testing.T) {
		t.Parallel()
		msg := llm.Message{
			Role:    llm.RoleUser,
			Content: "",
		}
		got := withCacheControl(msg)
		if got.Content != "" {
			t.Errorf("empty content should stay empty, got %q", got.Content)
		}
	})

	t.Run("system message gets text marker", func(t *testing.T) {
		t.Parallel()
		msg := llm.Message{
			Role:    llm.RoleSystem,
			Content: "system prompt",
		}
		got := withCacheControl(msg)
		if !containsText(got.Content, "[cache_control:ephemeral]") {
			t.Error("system message should have cache marker")
		}
	})
}
