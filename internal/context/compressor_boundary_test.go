package context

import (
	"fmt"
	"testing"

	"nexus-agent/internal/llm"
)

// ---------------------------------------------------------------------------
// alignBoundaryForward
// ---------------------------------------------------------------------------

func TestAlignBoundaryForward(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	t.Run("no tool messages at boundary", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
			{Role: llm.RoleAssistant, Content: "hi"},
		}
		got := c.alignBoundaryForward(msgs, 1)
		if got != 1 {
			t.Errorf("got %d, want 1", got)
		}
	})

	t.Run("skips tool messages at boundary", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
			{Role: llm.RoleTool, Content: "result1"},
			{Role: llm.RoleTool, Content: "result2"},
			{Role: llm.RoleAssistant, Content: "hi"},
		}
		got := c.alignBoundaryForward(msgs, 1)
		if got != 3 {
			t.Errorf("got %d, want 3 (skipped 2 tool msgs)", got)
		}
	})

	t.Run("all tool messages to end returns len", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
			{Role: llm.RoleTool, Content: "result1"},
			{Role: llm.RoleTool, Content: "result2"},
		}
		got := c.alignBoundaryForward(msgs, 1)
		if got != 3 {
			t.Errorf("got %d, want 3", got)
		}
	})
}

// ---------------------------------------------------------------------------
// alignBoundaryBackward
// ---------------------------------------------------------------------------

func TestAlignBoundaryBackward(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	t.Run("idx zero returns zero", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}
		got := c.alignBoundaryBackward(msgs, 0)
		if got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})

	t.Run("idx past end returns unchanged", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}
		got := c.alignBoundaryBackward(msgs, 5)
		if got != 5 {
			t.Errorf("got %d, want 5", got)
		}
	})

	t.Run("boundary after tool result pulls back", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "terminal", Arguments: "{}"},
			}},
			{Role: llm.RoleTool, Content: "result", ToolCallID: "c1"},
			{Role: llm.RoleUser, Content: "next"},
		}
		// idx=3: check=2 is tool, check=1 is assistant with toolcalls -> idx=1
		got := c.alignBoundaryBackward(msgs, 3)
		if got != 1 {
			t.Errorf("got %d, want 1", got)
		}
	})

	t.Run("boundary in middle of tool group pulls back to assistant", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "terminal", Arguments: "{}"},
			}},
			{Role: llm.RoleTool, Content: "result", ToolCallID: "c1"},
		}
		// idx=2 falls on tool result, check=1 is assistant with toolcalls -> idx=1
		got := c.alignBoundaryBackward(msgs, 2)
		if got != 1 {
			t.Errorf("got %d, want 1 (pulled back to assistant)", got)
		}
	})
}

// ---------------------------------------------------------------------------
// searchSubstring
// ---------------------------------------------------------------------------

func TestSearchSubstring(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s    string
		sub  string
		want bool
	}{
		{"hello world", "world", true},
		{"hello world", "xyz", false},
		{"", "", true},
		{"abc", "", true},
		{"short", "longer than", false},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q_in_%q", tc.sub, tc.s), func(t *testing.T) {
			t.Parallel()
			got := searchSubstring(tc.s, tc.sub)
			if got != tc.want {
				t.Errorf("searchSubstring(%q, %q) = %v, want %v", tc.s, tc.sub, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// containsText
// ---------------------------------------------------------------------------

func TestContainsText(t *testing.T) {
	t.Parallel()

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		if !containsText("hello world", "world") {
			t.Error("expected true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		if containsText("hello", "world") {
			t.Error("expected false")
		}
	})

	t.Run("substr longer than s", func(t *testing.T) {
		t.Parallel()
		if containsText("hi", "hello world") {
			t.Error("expected false when substr longer than s")
		}
	})
}
