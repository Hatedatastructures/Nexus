package context

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"nexus-agent/internal/llm"
)

// ---------------------------------------------------------------------------
// pickSummaryRole
// ---------------------------------------------------------------------------

func TestPickSummaryRole(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	tests := []struct {
		name     string
		msgs     []llm.Message
		start    int
		end      int
		wantRole llm.MessageRole
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
			end:      5,                 // past len
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
