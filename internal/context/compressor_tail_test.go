package context

import (
	"strings"
	"testing"

	"nexus-agent/internal/llm"
)

// ---------------------------------------------------------------------------
// findTailCutByTokens
// ---------------------------------------------------------------------------

func TestFindTailCutByTokens(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	t.Run("small conversation protects all", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleSystem, Content: "sys"},
			{Role: llm.RoleUser, Content: "hi"},
			{Role: llm.RoleAssistant, Content: "hello"},
		}
		// headEnd=1, very large budget -> cutIdx should be 2 (just one message compressed)
		got := c.findTailCutByTokens(msgs, 1, 100000)
		if got <= 1 {
			t.Errorf("got %d, want > 1", got)
		}
	})

	t.Run("large conversation respects budget", func(t *testing.T) {
		t.Parallel()
		msgs := make([]llm.Message, 20)
		for i := range msgs {
			msgs[i] = llm.Message{Role: llm.RoleUser, Content: strings.Repeat("x", 1000)}
		}
		// headEnd=3, budget=2000 -> soft ceiling 3000
		// each msg ~260 tokens, should cut somewhere in the middle
		got := c.findTailCutByTokens(msgs, 3, 2000)
		if got <= 3 {
			t.Errorf("got %d, want > 3", got)
		}
		if got > 20 {
			t.Errorf("got %d, want <= 20", got)
		}
	})

	t.Run("ensures minimum tail of 3", func(t *testing.T) {
		t.Parallel()
		msgs := make([]llm.Message, 10)
		for i := range msgs {
			msgs[i] = llm.Message{Role: llm.RoleUser, Content: "short"}
		}
		got := c.findTailCutByTokens(msgs, 3, 10)
		// Should ensure at least 3 messages in tail
		if 10-got < 3 && got > 3 {
			t.Errorf("tail should have at least 3 messages, got cutIdx=%d (tail=%d)", got, 10-got)
		}
	})
}

// ---------------------------------------------------------------------------
// ensureLastUserMessageInTail
// ---------------------------------------------------------------------------

func TestEnsureLastUserMessageInTail(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	t.Run("last user already in tail returns unchanged", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "first"},
			{Role: llm.RoleAssistant, Content: "reply"},
			{Role: llm.RoleUser, Content: "second"},
		}
		got := c.ensureLastUserMessageInTail(msgs, 1, 0)
		if got != 1 {
			t.Errorf("got %d, want 1", got)
		}
	})

	t.Run("last user in middle pulls boundary back", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "first"},
			{Role: llm.RoleUser, Content: "middle_user"},
			{Role: llm.RoleAssistant, Content: "reply"},
			{Role: llm.RoleAssistant, Content: "tail"},
		}
		// cutIdx=3, last user at idx=1, headEnd=0
		// lastUserIdx=1 < cutIdx=3, and lastUserIdx > headEnd=0
		got := c.ensureLastUserMessageInTail(msgs, 3, 0)
		if got != 1 {
			t.Errorf("got %d, want 1 (pulled back to last user)", got)
		}
	})

	t.Run("no user messages returns unchanged", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleSystem, Content: "sys"},
			{Role: llm.RoleAssistant, Content: "reply"},
		}
		got := c.ensureLastUserMessageInTail(msgs, 1, 0)
		if got != 1 {
			t.Errorf("got %d, want 1", got)
		}
	})

	t.Run("last user at headEnd returns unchanged", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "first"},
			{Role: llm.RoleAssistant, Content: "reply"},
			{Role: llm.RoleAssistant, Content: "tail"},
		}
		// cutIdx=2, headEnd=0, lastUserIdx=0 — not > headEnd so stays unchanged
		got := c.ensureLastUserMessageInTail(msgs, 2, 0)
		if got != 2 {
			t.Errorf("got %d, want 2 (lastUser at headEnd, no pull)", got)
		}
	})
}

// ---------------------------------------------------------------------------
// sanitizeToolPairs
// ---------------------------------------------------------------------------

func TestSanitizeToolPairs(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	t.Run("no orphan pairs returns unchanged", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "terminal", Arguments: "{}"},
			}},
			{Role: llm.RoleTool, Content: "result", ToolCallID: "c1"},
		}
		got := c.sanitizeToolPairs(msgs)
		if len(got) != 2 {
			t.Errorf("got %d messages, want 2", len(got))
		}
	})

	t.Run("removes orphaned tool results", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
			{Role: llm.RoleTool, Content: "orphan result", ToolCallID: "missing_call"},
		}
		got := c.sanitizeToolPairs(msgs)
		if len(got) != 1 {
			t.Errorf("got %d messages, want 1 (orphan removed)", len(got))
		}
		if got[0].Role != llm.RoleUser {
			t.Error("remaining message should be user")
		}
	})

	t.Run("adds stub results for missing tool results", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "terminal", Arguments: "{}"},
			}},
			{Role: llm.RoleUser, Content: "next"},
		}
		got := c.sanitizeToolPairs(msgs)
		// Should have: assistant, stub_result, user
		if len(got) != 3 {
			t.Fatalf("got %d messages, want 3", len(got))
		}
		if got[1].Role != llm.RoleTool {
			t.Error("second message should be stub tool result")
		}
		if got[1].ToolCallID != "c1" {
			t.Errorf("stub ToolCallID = %q, want c1", got[1].ToolCallID)
		}
	})

	t.Run("handles both orphan removal and stub insertion", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "terminal", Arguments: "{}"},
			}},
			{Role: llm.RoleTool, Content: "orphan", ToolCallID: "ghost"},
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "c2", Name: "read", Arguments: "{}"},
			}},
		}
		got := c.sanitizeToolPairs(msgs)
		// After orphan removal: assistant(c1), assistant(c2)
		// After stub insertion: assistant(c1), stub_c1, assistant(c2), stub_c2
		if len(got) != 4 {
			t.Fatalf("got %d messages, want 4", len(got))
		}
		// Check stubs
		stubIDs := map[string]bool{}
		for _, m := range got {
			if m.Role == llm.RoleTool {
				stubIDs[m.ToolCallID] = true
			}
		}
		if !stubIDs["c1"] || !stubIDs["c2"] {
			t.Errorf("missing stubs for c1 or c2, got stubIDs=%v", stubIDs)
		}
	})

	t.Run("empty messages returns empty", func(t *testing.T) {
		t.Parallel()
		got := c.sanitizeToolPairs(nil)
		if len(got) != 0 {
			t.Errorf("got %d messages, want 0", len(got))
		}
	})
}
