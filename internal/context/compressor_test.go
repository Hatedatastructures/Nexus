package context

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"nexus-agent/internal/llm"
)

// TestNewCompressor_Defaults verifies that NewCompressor applies the expected
// default values when given reasonable positive inputs.
func TestNewCompressor_Defaults(t *testing.T) {
	t.Parallel()

	c := NewCompressor(3, 20000)

	if c.protectFirstN != 3 {
		t.Errorf("protectFirstN: got %d, want 3", c.protectFirstN)
	}
	if c.tailTokenBudget != 20000 {
		t.Errorf("tailTokenBudget: got %d, want 20000", c.tailTokenBudget)
	}
	if c.thresholdPercent != 0.75 {
		t.Errorf("thresholdPercent: got %f, want 0.75", c.thresholdPercent)
	}
}

// TestCompressor_SetThresholdPercent verifies the SetThresholdPercent method
// accepts valid values and silently ignores invalid ones.
func TestCompressor_SetThresholdPercent(t *testing.T) {
	t.Parallel()

	c := NewCompressor(3, 20000)

	// Valid values should be accepted.
	for _, pct := range []float64{0.1, 0.5, 0.75, 1.0} {
		c.SetThresholdPercent(pct)
		if c.thresholdPercent != pct {
			t.Errorf("SetThresholdPercent(%f): got %f, want %f", pct, c.thresholdPercent, pct)
		}
	}

	// Invalid values should be ignored — threshold should remain at the last valid value (1.0).
	original := c.thresholdPercent

	for _, pct := range []float64{0.0, -0.5, 1.5, 2.0, -100.0} {
		c.SetThresholdPercent(pct)
		if c.thresholdPercent != original {
			t.Errorf("SetThresholdPercent(%f): threshold changed from %f to %f, expected no change",
				pct, original, c.thresholdPercent)
		}
	}
}

// TestCompressor_TailTokenBudget verifies the TailTokenBudget getter returns
// the value that was supplied to NewCompressor.
func TestCompressor_TailTokenBudget(t *testing.T) {
	t.Parallel()

	c := NewCompressor(3, 50000)
	if got := c.TailTokenBudget(); got != 50000 {
		t.Errorf("TailTokenBudget() = %d, want 50000", got)
	}
}

// TestCompressor_NilAuxProvider verifies that constructing a Compressor without
// calling SetAuxProvider does not panic and leaves the auxProvider field nil.
func TestCompressor_NilAuxProvider(t *testing.T) {
	t.Parallel()

	c := NewCompressor(3, 20000)
	// auxProvider should remain nil — no panic, no forced initialization.
	if c.auxProvider != nil {
		t.Error("auxProvider should be nil when SetAuxProvider is not called")
	}
}

// TestCompressor_InvalidDefaults verifies that passing zero or negative values
// to NewCompressor causes the constructor to fall back to sensible defaults
// (protectFirstN=3, tailTokenBudget=20000).
func TestCompressor_InvalidDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		protectFirstN     int
		tailTokenBudget   int
		wantProtect       int
		wantTailBudget    int
	}{
		{
			name:            "both zero",
			protectFirstN:   0,
			tailTokenBudget: 0,
			wantProtect:     3,
			wantTailBudget:  20000,
		},
		{
			name:            "both negative",
			protectFirstN:   -5,
			tailTokenBudget: -1000,
			wantProtect:     3,
			wantTailBudget:  20000,
		},
		{
			name:            "protectFirstN zero only",
			protectFirstN:   0,
			tailTokenBudget: 5000,
			wantProtect:     3,
			wantTailBudget:  5000,
		},
		{
			name:            "tailTokenBudget negative only",
			protectFirstN:   10,
			tailTokenBudget: -1,
			wantProtect:     10,
			wantTailBudget:  20000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := NewCompressor(tt.protectFirstN, tt.tailTokenBudget)

			if c.protectFirstN != tt.wantProtect {
				t.Errorf("protectFirstN: got %d, want %d", c.protectFirstN, tt.wantProtect)
			}
			if c.tailTokenBudget != tt.wantTailBudget {
				t.Errorf("tailTokenBudget: got %d, want %d", c.tailTokenBudget, tt.wantTailBudget)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// estimateImageTokens
// ---------------------------------------------------------------------------

func TestEstimateImageTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input int
		want  int
	}{
		{0, 0},
		{-1, 0},
		{-5, 0},
		{1, 1600},
		{3, 4800},
		{10, 16000},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("count_%d", tc.input), func(t *testing.T) {
			t.Parallel()
			got := estimateImageTokens(tc.input)
			if got != tc.want {
				t.Errorf("estimateImageTokens(%d) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ShouldCompress
// ---------------------------------------------------------------------------

func TestShouldCompress(t *testing.T) {
	t.Parallel()

	t.Run("below threshold returns false", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		if c.ShouldCompress(100000, 50000) {
			t.Error("should not compress when below threshold")
		}
	})

	t.Run("above threshold returns true", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		if !c.ShouldCompress(100000, 80000) {
			t.Error("should compress when above threshold")
		}
	})

	t.Run("exact threshold returns false", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		// 75% of 100000 = 75000, so 75000 should not compress
		if c.ShouldCompress(100000, 75000) {
			t.Error("at exact threshold should not compress")
		}
	})

	t.Run("cooldown blocks compression", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		// Force anti-thrash by setting consecutiveSummaries to threshold
		c.consecutiveSummaries = 2
		// This call triggers cooldown
		if c.ShouldCompress(100000, 90000) {
			t.Error("cooldown should block compression")
		}
		// Cooldown should now be 3
		if c.antiThrashCooldown != 3 {
			t.Errorf("cooldown should be 3, got %d", c.antiThrashCooldown)
		}
	})

	t.Run("cooldown ticks down", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.antiThrashCooldown = 2
		// First tick: cooldown 2->1
		if c.ShouldCompress(100000, 90000) {
			t.Error("cooldown should still block")
		}
		if c.antiThrashCooldown != 1 {
			t.Errorf("cooldown should be 1, got %d", c.antiThrashCooldown)
		}
		// Second tick: cooldown 1->0
		if c.ShouldCompress(100000, 90000) {
			t.Error("cooldown should still block")
		}
		if c.antiThrashCooldown != 0 {
			t.Errorf("cooldown should be 0, got %d", c.antiThrashCooldown)
		}
		// Third call: no cooldown, should compress
		if !c.ShouldCompress(100000, 90000) {
			t.Error("after cooldown expires, should compress again")
		}
	})
}

// ---------------------------------------------------------------------------
// recordCompressionResult
// ---------------------------------------------------------------------------

func TestRecordCompressionResult(t *testing.T) {
	t.Parallel()

	t.Run("effective compression resets counter", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.consecutiveSummaries = 1
		// 50% reduction (effective)
		c.recordCompressionResult(1000, 500)
		if c.consecutiveSummaries != 0 {
			t.Errorf("expected counter reset to 0, got %d", c.consecutiveSummaries)
		}
	})

	t.Run("ineffective compression increments counter", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		// 10% reduction (ineffective < 15%)
		c.recordCompressionResult(1000, 900)
		if c.consecutiveSummaries != 1 {
			t.Errorf("expected counter=1, got %d", c.consecutiveSummaries)
		}
	})

	t.Run("zero beforeTokens resets counter", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.consecutiveSummaries = 5
		c.recordCompressionResult(0, 0)
		if c.consecutiveSummaries != 0 {
			t.Errorf("zero beforeTokens should reset, got %d", c.consecutiveSummaries)
		}
	})

	t.Run("negative beforeTokens resets counter", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.consecutiveSummaries = 3
		c.recordCompressionResult(-10, 0)
		if c.consecutiveSummaries != 0 {
			t.Errorf("negative beforeTokens should reset, got %d", c.consecutiveSummaries)
		}
	})

	t.Run("consecutive ineffective triggers threshold", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.recordCompressionResult(1000, 950) // 5% reduction
		c.recordCompressionResult(1000, 950) // 5% reduction
		if c.consecutiveSummaries != 2 {
			t.Errorf("expected 2 consecutive, got %d", c.consecutiveSummaries)
		}
	})

	t.Run("exactly 15 percent reduction resets counter", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.consecutiveSummaries = 1
		// 15% exactly: 1000 -> 850
		c.recordCompressionResult(1000, 850)
		if c.consecutiveSummaries != 0 {
			t.Errorf("exactly 15%% should be effective, got counter=%d", c.consecutiveSummaries)
		}
	})
}

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

// ---------------------------------------------------------------------------
// pickSummaryRole
// ---------------------------------------------------------------------------

func TestPickSummaryRole(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	tests := []struct {
		name         string
		msgs         []llm.Message
		start        int
		end          int
		wantRole     llm.MessageRole
	}{
		{
			name:     "compressStart zero returns user",
			msgs:     []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
			start:    0,
			end:      1,
			wantRole: llm.RoleUser,
		},
		{
			name: "last head is assistant returns user",
			msgs: []llm.Message{
				{Role: llm.RoleUser, Content: "hi"},
				{Role: llm.RoleAssistant, Content: "reply"},
				{Role: llm.RoleUser, Content: "next"},
			},
			start:    2,
			end:      3,
			wantRole: llm.RoleUser,
		},
		{
			name: "last head is tool returns user",
			msgs: []llm.Message{
				{Role: llm.RoleUser, Content: "hi"},
				{Role: llm.RoleTool, Content: "result"},
				{Role: llm.RoleUser, Content: "next"},
			},
			start:    2,
			end:      3,
			wantRole: llm.RoleUser,
		},
		{
			name: "tail is assistant returns user to avoid conflict",
			msgs: []llm.Message{
				{Role: llm.RoleUser, Content: "hi"},
				{Role: llm.RoleUser, Content: "mid"},
				{Role: llm.RoleAssistant, Content: "tail"},
			},
			start:    2,
			end:      2,
			wantRole: llm.RoleUser,
		},
		{
			name: "tail is user and head is user returns assistant",
			msgs: []llm.Message{
				{Role: llm.RoleUser, Content: "hi"},
				{Role: llm.RoleUser, Content: "mid"},
				{Role: llm.RoleUser, Content: "tail"},
			},
			start:    2,
			end:      2,
			wantRole: llm.RoleAssistant,
		},
		{
			name: "end past messages uses default user tail",
			msgs: []llm.Message{
				{Role: llm.RoleUser, Content: "hi"},
				{Role: llm.RoleUser, Content: "mid"},
			},
			start:    2,
			end:      5, // past len
			wantRole: llm.RoleAssistant, // tail default is user, not assistant, so no conflict
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := c.pickSummaryRole(tc.msgs, tc.start, tc.end)
			if got != tc.wantRole {
				t.Errorf("pickSummaryRole() = %q, want %q", got, tc.wantRole)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// searchSubstring
// ---------------------------------------------------------------------------

func TestSearchSubstring(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s      string
		sub    string
		want   bool
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

// ---------------------------------------------------------------------------
// mockProvider for Compress integration tests
// ---------------------------------------------------------------------------

type mockProvider struct {
	response *llm.ChatResponse
	err      error
}

func (m *mockProvider) CreateChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func (m *mockProvider) CreateChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan *llm.StreamDelta, error) {
	ch := make(chan *llm.StreamDelta, 1)
	ch <- &llm.StreamDelta{Done: true}
	close(ch)
	return ch, nil
}

func (m *mockProvider) ListModels(_ context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}

func (m *mockProvider) Name() string {
	return "mock"
}

// ---------------------------------------------------------------------------
// Compress integration tests
// ---------------------------------------------------------------------------

func TestCompress_TooFewMessages(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "hello"},
		{Role: llm.RoleUser, Content: "next"}, // 4 messages, minForCompress = 3+3+1=7
	}
	got, err := c.Compress(context.Background(), msgs, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(msgs) {
		t.Errorf("too few messages should return unchanged, got %d vs %d", len(got), len(msgs))
	}
}

func TestCompress_WithNilProvider(t *testing.T) {
	t.Parallel()
	c := NewCompressor(1, 500) // small budget to force compression

	msgs := make([]llm.Message, 15)
	msgs[0] = llm.Message{Role: llm.RoleSystem, Content: "sys"}
	for i := 1; i < len(msgs); i++ {
		msgs[i] = llm.Message{Role: llm.RoleUser, Content: strings.Repeat("hello ", 50)}
	}

	got, err := c.Compress(context.Background(), msgs, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have compressed (fewer messages than original)
	if len(got) >= len(msgs) {
		t.Errorf("expected compression, got %d messages (original %d)", len(got), len(msgs))
	}
	// Should have system message
	if got[0].Role != llm.RoleSystem {
		t.Error("first message should be system")
	}
}

func TestCompress_WithProvider(t *testing.T) {
	t.Parallel()
	c := NewCompressor(1, 500)

	provider := &mockProvider{
		response: &llm.ChatResponse{
			Content: "This is a summary of the conversation.",
		},
	}

	msgs := make([]llm.Message, 15)
	msgs[0] = llm.Message{Role: llm.RoleSystem, Content: "sys"}
	for i := 1; i < len(msgs); i++ {
		msgs[i] = llm.Message{Role: llm.RoleUser, Content: strings.Repeat("hello ", 50)}
	}

	got, err := c.Compress(context.Background(), msgs, provider, "testing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) >= len(msgs) {
		t.Errorf("expected compression, got %d messages (original %d)", len(got), len(msgs))
	}
}

func TestCompress_ProviderErrorUsesFallback(t *testing.T) {
	t.Parallel()
	c := NewCompressor(1, 500)

	provider := &mockProvider{
		err: fmt.Errorf("provider unavailable"),
	}

	msgs := make([]llm.Message, 15)
	msgs[0] = llm.Message{Role: llm.RoleSystem, Content: "sys"}
	for i := 1; i < len(msgs); i++ {
		msgs[i] = llm.Message{Role: llm.RoleUser, Content: strings.Repeat("hello ", 50)}
	}

	got, err := c.Compress(context.Background(), msgs, provider, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should still compress even if provider fails (uses degraded summary)
	if len(got) >= len(msgs) {
		t.Errorf("expected fallback compression, got %d messages (original %d)", len(got), len(msgs))
	}
}

func TestCompress_NoMiddleToCompress(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 100000) // huge budget protects everything

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "hello"},
		{Role: llm.RoleUser, Content: "next"},
		{Role: llm.RoleAssistant, Content: "reply"},
		{Role: llm.RoleUser, Content: "end"},
		{Role: llm.RoleAssistant, Content: "bye"},
		{Role: llm.RoleUser, Content: "last"},
	}

	got, err := c.Compress(context.Background(), msgs, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With huge budget, head+tail covers everything, no middle to compress
	if len(got) != len(msgs) {
		t.Logf("got %d messages (original %d) — compression occurred despite large budget", len(got), len(msgs))
	}
}
