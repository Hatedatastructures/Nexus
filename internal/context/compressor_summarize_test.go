package context

import (
	"strings"
	"testing"

	"nexus-agent/internal/llm"
)

// ---------------------------------------------------------------------------
// computeSummaryBudget
// ---------------------------------------------------------------------------

func TestComputeSummaryBudget(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	t.Run("empty messages returns minimum budget", func(t *testing.T) {
		t.Parallel()
		budget := c.computeSummaryBudget(nil)
		if budget != 2000 {
			t.Errorf("budget = %d, want 2000", budget)
		}
	})

	t.Run("small messages clamped to minimum", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "short"},
		}
		budget := c.computeSummaryBudget(msgs)
		if budget != 2000 {
			t.Errorf("budget = %d, want 2000", budget)
		}
	})

	t.Run("large messages capped at maximum", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: strings.Repeat("x", 300000)},
		}
		budget := c.computeSummaryBudget(msgs)
		if budget != 12000 {
			t.Errorf("budget = %d, want 12000", budget)
		}
	})

	t.Run("medium messages within range", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: strings.Repeat("a", 20000)},
			{Role: llm.RoleAssistant, Content: strings.Repeat("b", 20000)},
		}
		budget := c.computeSummaryBudget(msgs)
		if budget < 2000 || budget > 12000 {
			t.Errorf("budget = %d, want in [2000, 12000]", budget)
		}
	})

	t.Run("tool call arguments included in budget", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "terminal", Arguments: strings.Repeat("a", 40000)},
				},
			},
		}
		budget := c.computeSummaryBudget(msgs)
		// 40000/4 = 10000 tokens, 10000*0.20 = 2000 budget
		if budget < 2000 {
			t.Errorf("budget = %d, want >= 2000", budget)
		}
	})
}

// ---------------------------------------------------------------------------
// serializeForSummary
// ---------------------------------------------------------------------------

func TestSerializeForSummary(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	t.Run("empty messages returns empty string", func(t *testing.T) {
		t.Parallel()
		got := c.serializeForSummary(nil)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("user message serialized", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "hello world"},
		}
		got := c.serializeForSummary(msgs)
		if !strings.Contains(got, "[USER]: hello world") {
			t.Errorf("expected [USER] prefix, got %q", got)
		}
	})

	t.Run("assistant message with tool calls", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "I will run a command",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "terminal", Arguments: `{"command":"ls"}`},
				},
			},
		}
		got := c.serializeForSummary(msgs)
		if !strings.Contains(got, "[ASSISTANT]:") {
			t.Errorf("expected [ASSISTANT] prefix, got %q", got)
		}
		if !strings.Contains(got, "terminal") {
			t.Errorf("expected tool call name, got %q", got)
		}
		if !strings.Contains(got, "Tool calls:") {
			t.Errorf("expected tool calls section, got %q", got)
		}
	})

	t.Run("tool result message serialized", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleTool, Content: "output", ToolCallID: "call_1"},
		}
		got := c.serializeForSummary(msgs)
		if !strings.Contains(got, "[TOOL RESULT call_1]: output") {
			t.Errorf("expected tool result format, got %q", got)
		}
	})

	t.Run("long content truncated", func(t *testing.T) {
		t.Parallel()
		longContent := strings.Repeat("x", 7000)
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: longContent},
		}
		got := c.serializeForSummary(msgs)
		if !strings.Contains(got, "[truncated]") {
			t.Error("long content should be truncated")
		}
	})

	t.Run("system message serialized with uppercase role", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleSystem, Content: "system prompt"},
		}
		got := c.serializeForSummary(msgs)
		if !strings.Contains(got, "[SYSTEM]: system prompt") {
			t.Errorf("expected [SYSTEM] prefix, got %q", got)
		}
	})

	t.Run("sensitive data redacted in tool call args", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "terminal", Arguments: `{"command":"curl -H 'api_key: sk-abc123def456ghi789jkl012' http://example.com"}`},
				},
			},
		}
		got := c.serializeForSummary(msgs)
		if strings.Contains(got, "sk-abc123def456ghi789jkl012") {
			t.Error("sensitive API key should be redacted")
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Error("expected [REDACTED] in output")
		}
	})
}

// ---------------------------------------------------------------------------
// FormatSummary
// ---------------------------------------------------------------------------

func TestFormatSummary(t *testing.T) {
	t.Parallel()

	t.Run("empty messages returns structured empty summary", func(t *testing.T) {
		t.Parallel()
		got := FormatSummary(nil)
		if !strings.Contains(got, "## 消息统计") {
			t.Error("missing section header")
		}
		if !strings.Contains(got, "用户消息: 0 条") {
			t.Error("expected zero user messages")
		}
	})

	t.Run("counts messages by role", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "do something important here"},
			{Role: llm.RoleAssistant, Content: "sure"},
			{Role: llm.RoleTool, Content: "result"},
			{Role: llm.RoleUser, Content: "another request from user side"},
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "用户消息: 2 条") {
			t.Error("expected 2 user messages")
		}
		if !strings.Contains(got, "助手消息: 1 条") {
			t.Error("expected 1 assistant message")
		}
		if !strings.Contains(got, "工具结果: 1 条") {
			t.Error("expected 1 tool result")
		}
	})

	t.Run("extracts tool calls", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "terminal", Arguments: `{"command":"go test"}`},
				},
			},
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "## 工具调用摘要") {
			t.Error("missing tool calls section")
		}
		if !strings.Contains(got, "[terminal]") {
			t.Error("missing tool call entry")
		}
	})

	t.Run("extracts pending tasks from user messages", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "Please implement the authentication module for the project"},
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "## 待处理任务") {
			t.Error("missing pending tasks section")
		}
		if !strings.Contains(got, "authentication module") {
			t.Error("missing task content")
		}
	})

	t.Run("deduplicates short user messages", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "ok"},
			{Role: llm.RoleUser, Content: "yes"},
		}
		got := FormatSummary(msgs)
		if strings.Contains(got, "## 待处理任务\n- ok") {
			t.Error("short messages should not appear as pending tasks")
		}
	})

	t.Run("extracts key files from tool calls", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "write_file", Arguments: `{"path":"main.go"}`},
				},
			},
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "## 关键文件") {
			t.Error("missing key files section")
		}
		if !strings.Contains(got, "main.go") {
			t.Error("missing file path")
		}
	})

	t.Run("extracts decisions from assistant content", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "I decided to use the repository pattern for this module",
			},
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "## 关键决策") {
			t.Error("missing decisions section")
		}
		if !strings.Contains(got, "repository pattern") {
			t.Error("missing decision content")
		}
	})

	t.Run("includes tool results in context", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleTool, Content: "Tests passed successfully", ToolCallID: "c1"},
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "## 当前上下文") {
			t.Error("missing context section")
		}
		if !strings.Contains(got, "Tests passed") {
			t.Error("missing tool result in context")
		}
	})

	t.Run("long user request truncated in pending tasks", func(t *testing.T) {
		t.Parallel()
		longReq := "Please implement " + strings.Repeat("a", 300)
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: longReq},
		}
		got := FormatSummary(msgs)
		// The request should appear truncated (not full 300+ chars)
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(line, "- Please implement") && len(line) > 210 {
				t.Errorf("pending task line too long: %d chars", len(line))
			}
		}
	})

	t.Run("limits tool calls to 20 in output", func(t *testing.T) {
		t.Parallel()
		msgs := make([]llm.Message, 30)
		for i := range msgs {
			msgs[i] = llm.Message{
				Role:    llm.RoleAssistant,
				Content: "",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "terminal", Arguments: `{"command":"ls"}`},
				},
			}
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "仅显示最近 20 次") {
			t.Error("expected truncation notice for 30 tool calls")
		}
	})
}
