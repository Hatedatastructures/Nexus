package llm

import (
	"testing"
)

func TestEstimateTokensRough_English(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "Hello world, this is a test."},
	}
	got := EstimateTokensRough(msgs)
	if got <= 0 {
		t.Error("should return positive token count")
	}
}

func TestEstimateTokensRough_Chinese(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "你好世界测试"},
	}
	got := EstimateTokensRough(msgs)
	if got <= 0 {
		t.Error("should return positive token count for CJK")
	}
}

func TestEstimateTokensRough_Empty(t *testing.T) {
	got := EstimateTokensRough(nil)
	if got != 0 {
		t.Errorf("empty messages = %d, want 0", got)
	}
}

func TestEstimateTokensRough_WithToolCalls(t *testing.T) {
	msgs := []Message{
		{
			Role:    RoleAssistant,
			Content: "Let me check.",
			ToolCalls: []ToolCall{
				{Name: "read_file", Arguments: `{"path": "/tmp/test.txt"}`},
			},
		},
	}
	got := EstimateTokensRough(msgs)
	if got <= 0 {
		t.Error("should count tool call arguments")
	}
}

func TestEstimateTokensRough_MultipleMessages(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "You are helpful."},
		{Role: RoleUser, Content: "Hello"},
		{Role: RoleAssistant, Content: "Hi there!"},
	}
	got := EstimateTokensRough(msgs)
	if got < 30 {
		t.Errorf("multiple messages = %d, want at least 30", got)
	}
}
