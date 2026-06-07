package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"nexus-agent/internal/llm"
)

// ---------------------------------------------------------------------------
// loadMessageHistory
// ---------------------------------------------------------------------------

func TestGatewayRunner_loadMessageHistory(t *testing.T) {
	t.Parallel()

	t.Run("returns nil when state is nil", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		r := NewGatewayRunner(cfg, fullCfg, nil, nil)

		msgs, err := r.loadMessageHistory(context.Background(), "sess1", 10)
		if err != nil {
			t.Fatal(err)
		}
		if msgs != nil {
			t.Error("expected nil messages")
		}
	})

	t.Run("returns nil when sessionID is empty", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		msgs, err := r.loadMessageHistory(context.Background(), "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if msgs != nil {
			t.Error("expected nil messages")
		}
	})

	t.Run("returns empty for non-existent session", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		msgs, err := r.loadMessageHistory(context.Background(), "nonexistent", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 0 {
			t.Errorf("expected 0 messages, got %d", len(msgs))
		}
	})

	t.Run("loads messages with basic fields", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx := context.Background()
		sessionID := "hist-basic"

		// 先创建 session
		_, err := st.DB().ExecContext(ctx,
			`INSERT INTO sessions (id, source, user_id, model, started_at)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "tg:dm:123", "u1", "model", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		// 插入消息
		_, err = st.DB().ExecContext(ctx,
			`INSERT INTO messages (session_id, role, content, timestamp)
			 VALUES (?, ?, ?, ?)`,
			sessionID, "user", "hello", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = st.DB().ExecContext(ctx,
			`INSERT INTO messages (session_id, role, content, timestamp)
			 VALUES (?, ?, ?, ?)`,
			sessionID, "assistant", "world", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		msgs, err := r.loadMessageHistory(ctx, sessionID, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		if msgs[0].Role != llm.MessageRole("user") {
			t.Errorf("msgs[0].Role = %q, want %q", msgs[0].Role, "user")
		}
		if msgs[0].Content != "hello" {
			t.Errorf("msgs[0].Content = %q, want %q", msgs[0].Content, "hello")
		}
		if msgs[1].Role != llm.MessageRole("assistant") {
			t.Errorf("msgs[1].Role = %q, want %q", msgs[1].Role, "assistant")
		}
	})

	t.Run("deserializes tool_calls from JSON", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx := context.Background()
		sessionID := "hist-tools"

		_, err := st.DB().ExecContext(ctx,
			`INSERT INTO sessions (id, source, user_id, model, started_at)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "tg:dm:456", "u2", "model", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		toolCalls := []llm.ToolCall{
			{ID: "tc1", Name: "bash", Arguments: `{"cmd":"ls"}`},
		}
		tcJSON, _ := json.Marshal(toolCalls)

		_, err = st.DB().ExecContext(ctx,
			`INSERT INTO messages (session_id, role, content, tool_calls, timestamp)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "assistant", "", string(tcJSON), float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		msgs, err := r.loadMessageHistory(ctx, sessionID, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		if len(msgs[0].ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(msgs[0].ToolCalls))
		}
		if msgs[0].ToolCalls[0].ID != "tc1" {
			t.Errorf("ToolCalls[0].ID = %q, want %q", msgs[0].ToolCalls[0].ID, "tc1")
		}
		if msgs[0].ToolCalls[0].Name != "bash" {
			t.Errorf("ToolCalls[0].Name = %q, want %q", msgs[0].ToolCalls[0].Name, "bash")
		}
	})

	t.Run("handles invalid tool_calls JSON gracefully", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx := context.Background()
		sessionID := "hist-badjson"

		_, err := st.DB().ExecContext(ctx,
			`INSERT INTO sessions (id, source, user_id, model, started_at)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "tg:dm:789", "u3", "model", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = st.DB().ExecContext(ctx,
			`INSERT INTO messages (session_id, role, content, tool_calls, timestamp)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "assistant", "some content", "not-valid-json", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		msgs, err := r.loadMessageHistory(ctx, sessionID, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		// invalid JSON -> ToolCalls should be nil (graceful degradation)
		if len(msgs[0].ToolCalls) != 0 {
			t.Errorf("expected 0 tool calls on invalid JSON, got %d", len(msgs[0].ToolCalls))
		}
	})

	t.Run("preserves tool_call_id field", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx := context.Background()
		sessionID := "hist-tcid"

		_, err := st.DB().ExecContext(ctx,
			`INSERT INTO sessions (id, source, user_id, model, started_at)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "tg:dm:000", "u4", "model", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = st.DB().ExecContext(ctx,
			`INSERT INTO messages (session_id, role, content, tool_call_id, timestamp)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "tool", "result", "call_123", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		msgs, err := r.loadMessageHistory(ctx, sessionID, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		if msgs[0].ToolCallID != "call_123" {
			t.Errorf("ToolCallID = %q, want %q", msgs[0].ToolCallID, "call_123")
		}
	})
}
