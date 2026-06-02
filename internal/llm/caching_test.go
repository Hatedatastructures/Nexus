package llm

import (
	"strings"
	"testing"
)

func TestSupportsAnthropicCaching(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-3-5-sonnet-20241022", true},
		{"claude-3-7-sonnet", true},
		{"claude-4-opus", true},
		{"claude-opus-4-7", true},
		{"claude-sonnet-4-6", true},
		{"claude-haiku-4-5", true},
		{"gpt-4o", false},
		{"deepseek-chat", false},
		{"", false},
	}
	for _, tt := range tests {
		got := supportsAnthropicCaching(tt.model)
		if got != tt.want {
			t.Errorf("supportsAnthropicCaching(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestApplyCacheControl_NonCacheModel(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "You are helpful."},
		{Role: RoleUser, Content: "Hello"},
	}
	result := ApplyCacheControl(msgs, "gpt-4o")
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].Content != "You are helpful." {
		t.Error("non-cache model should not modify messages")
	}
}

func TestApplyCacheControl_ClaudeModel(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "You are helpful."},
		{Role: RoleUser, Content: "Hello"},
	}
	result := ApplyCacheControl(msgs, "claude-sonnet-4-6")
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].Content == "You are helpful." {
		t.Error("system message should have cache control added")
	}
	if !strings.Contains(result[0].Content, "cache_control") {
		t.Error("system message should contain cache_control marker")
	}
}

func TestApplyCacheControl_MaxCachePoints(t *testing.T) {
	msgs := make([]Message, 10)
	for i := range msgs {
		msgs[i] = Message{Role: RoleSystem, Content: "system prompt"}
	}
	result := ApplyCacheControl(msgs, "claude-sonnet-4-6")
	count := 0
	for _, m := range result {
		if strings.Contains(m.Content, "cache_control") {
			count++
		}
	}
	if count > 4 {
		t.Errorf("cache points = %d, want at most 4", count)
	}
}

func TestApplyCacheControl_DuplicatePrevention(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "prompt\n<!-- cache_control: ephemeral -->"},
	}
	result := ApplyCacheControl(msgs, "claude-sonnet-4-6")
	occurrences := strings.Count(result[0].Content, "cache_control")
	if occurrences > 1 {
		t.Errorf("cache_control occurrences = %d, want 1", occurrences)
	}
}

func TestNeedsCacheControl(t *testing.T) {
	if !NeedsCacheControl("claude-sonnet-4-6") {
		t.Error("claude model should need cache control")
	}
	if NeedsCacheControl("gpt-4o") {
		t.Error("non-claude model should not need cache control")
	}
}

func TestIsAnthropicCacheModel(t *testing.T) {
	if !IsAnthropicCacheModel("claude-3-5-sonnet") {
		t.Error("claude-3-5-sonnet should be cache model")
	}
	if IsAnthropicCacheModel("gpt-4") {
		t.Error("gpt-4 should not be cache model")
	}
}
