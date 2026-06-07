package state

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestInsertAndGetMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "msg-session", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	msg := &MessageRecord{
		SessionID:    "msg-session",
		Role:         "user",
		Content:      "Hello, world!",
		ToolCalls:    `[{"id":"call_1","function":{"name":"bash"}}]`,
		ToolName:     "bash",
		Timestamp:    float64(time.Now().Unix()),
		TokenCount:   10,
		FinishReason: "stop",
	}

	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if msg.ID == 0 {
		t.Error("msg.ID should be set after insert")
	}

	got, err := store.GetMessages(ctx, "msg-session", 10, 0)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].Content != "Hello, world!" {
		t.Errorf("Content = %q, want %q", got[0].Content, "Hello, world!")
	}
	if got[0].Role != "user" {
		t.Errorf("Role = %q, want %q", got[0].Role, "user")
	}
	if got[0].ToolName != "bash" {
		t.Errorf("ToolName = %q, want %q", got[0].ToolName, "bash")
	}
}



func TestInsertMessage_UpdatesSessionCounters(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "counter-session", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	// Insert message without tool calls
	msg1 := &MessageRecord{
		SessionID: "counter-session",
		Role:      "user",
		Content:   "hello",
	}
	store.InsertMessage(ctx, msg1)

	got, _ := store.GetSession(ctx, "counter-session")
	if got.MessageCount != 1 {
		t.Errorf("MessageCount = %d, want 1", got.MessageCount)
	}
	if got.ToolCallCount != 0 {
		t.Errorf("ToolCallCount = %d, want 0", got.ToolCallCount)
	}

	// Insert message with tool calls
	msg2 := &MessageRecord{
		SessionID: "counter-session",
		Role:      "assistant",
		Content:   "using tool",
		ToolCalls: `[{"id":"c1"}]`,
	}
	store.InsertMessage(ctx, msg2)

	got, _ = store.GetSession(ctx, "counter-session")
	if got.MessageCount != 2 {
		t.Errorf("MessageCount = %d, want 2", got.MessageCount)
	}
	if got.ToolCallCount != 1 {
		t.Errorf("ToolCallCount = %d, want 1", got.ToolCallCount)
	}
}



func TestInsertMessage_ToolCallEdgeCases(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "tool-edge", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	// ToolCalls = "null" → no increment
	msg1 := &MessageRecord{SessionID: "tool-edge", Role: "assistant", ToolCalls: "null"}
	store.InsertMessage(ctx, msg1)

	// ToolCalls = "[]" → no increment
	msg2 := &MessageRecord{SessionID: "tool-edge", Role: "assistant", ToolCalls: "[]"}
	store.InsertMessage(ctx, msg2)

	// ToolCalls = "" → no increment
	msg3 := &MessageRecord{SessionID: "tool-edge", Role: "assistant", ToolCalls: ""}
	store.InsertMessage(ctx, msg3)

	got, _ := store.GetSession(ctx, "tool-edge")
	if got.MessageCount != 3 {
		t.Errorf("MessageCount = %d, want 3", got.MessageCount)
	}
	if got.ToolCallCount != 0 {
		t.Errorf("ToolCallCount = %d, want 0 (all edge cases)", got.ToolCallCount)
	}
}



func TestInsertMessagesBatch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "batch-session", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	msgs := []*MessageRecord{
		{SessionID: "batch-session", Role: "user", Content: "first"},
		{SessionID: "batch-session", Role: "assistant", Content: "second", ToolCalls: `[{"id":"c1"}]`},
		{SessionID: "batch-session", Role: "user", Content: "third"},
	}

	if err := store.InsertMessagesBatch(ctx, msgs); err != nil {
		t.Fatalf("InsertMessagesBatch: %v", err)
	}

	got, _ := store.GetMessages(ctx, "batch-session", 10, 0)
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}

	sess, _ := store.GetSession(ctx, "batch-session")
	if sess.MessageCount != 3 {
		t.Errorf("MessageCount = %d, want 3", sess.MessageCount)
	}
	if sess.ToolCallCount != 1 {
		t.Errorf("ToolCallCount = %d, want 1", sess.ToolCallCount)
	}
}



func TestInsertMessagesBatch_Empty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.InsertMessagesBatch(ctx, nil); err != nil {
		t.Errorf("InsertMessagesBatch(nil) should not error: %v", err)
	}
	if err := store.InsertMessagesBatch(ctx, []*MessageRecord{}); err != nil {
		t.Errorf("InsertMessagesBatch(empty) should not error: %v", err)
	}
}



func TestGetMessages_DefaultLimit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "limit-session", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	for i := 0; i < 5; i++ {
		msg := &MessageRecord{
			SessionID: "limit-session",
			Role:      "user",
			Content:   fmt.Sprintf("msg-%d", i),
			Timestamp: float64(time.Now().Unix() + int64(i)),
		}
		store.InsertMessage(ctx, msg)
	}

	// limit=0 should default to 100
	got, err := store.GetMessages(ctx, "limit-session", 0, 0)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("got %d messages with default limit, want 5", len(got))
	}
}



func TestGetMessageCount(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "count-session", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	for i := 0; i < 3; i++ {
		msg := &MessageRecord{SessionID: "count-session", Role: "user", Content: "hi"}
		store.InsertMessage(ctx, msg)
	}

	count, err := store.GetMessageCount(ctx, "count-session")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 3 {
		t.Errorf("GetMessageCount = %d, want 3", count)
	}
}



func TestGetMessageCount_NoMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "empty-count", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	count, err := store.GetMessageCount(ctx, "empty-count")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 0 {
		t.Errorf("GetMessageCount = %d, want 0", count)
	}
}

// ── 搜索测试 ──────────────────────────────────────────────────



func TestInsertMessagesBatch_WithMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateSession(ctx, &Session{ID: "batch-test", Source: "test", StartedAt: float64(time.Now().Unix())})

	msgs := []*MessageRecord{
		{SessionID: "batch-test", Role: "user", Content: "first"},
		{SessionID: "batch-test", Role: "assistant", Content: "second", ToolCalls: `[{"id":"t1"}]`},
	}
	if err := store.InsertMessagesBatch(ctx, msgs); err != nil {
		t.Fatalf("InsertMessagesBatch: %v", err)
	}

	count, err := store.GetMessageCount(ctx, "batch-test")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 2 {
		t.Errorf("message count = %d, want 2", count)
	}

	sess, _ := store.GetSession(ctx, "batch-test")
	if sess.MessageCount != 2 {
		t.Errorf("session message_count = %d, want 2", sess.MessageCount)
	}
	if sess.ToolCallCount != 1 {
		t.Errorf("session tool_call_count = %d, want 1", sess.ToolCallCount)
	}
}

// ── GetMessages 分页测试 ────────────────────────────────────────



func TestGetMessages_Pagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateSession(ctx, &Session{ID: "page-test", Source: "test", StartedAt: float64(time.Now().Unix())})

	for i := 0; i < 5; i++ {
		store.InsertMessage(ctx, &MessageRecord{
			SessionID: "page-test",
			Role:      "user",
			Content:   fmt.Sprintf("msg %d", i),
		})
	}

	// Get first page
	msgs, err := store.GetMessages(ctx, "page-test", 2, 0)
	if err != nil {
		t.Fatalf("GetMessages page 1: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("page 1: expected 2 messages, got %d", len(msgs))
	}

	// Get second page
	msgs, err = store.GetMessages(ctx, "page-test", 2, 2)
	if err != nil {
		t.Fatalf("GetMessages page 2: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("page 2: expected 2 messages, got %d", len(msgs))
	}

	// Default limit
	msgs, err = store.GetMessages(ctx, "page-test", 0, 0)
	if err != nil {
		t.Fatalf("GetMessages default limit: %v", err)
	}
	if len(msgs) != 5 {
		t.Errorf("default limit: expected 5 messages, got %d", len(msgs))
	}
}



// TestInsertMessagesBatch_Multiple 测试批量插入多条消息
func TestInsertMessagesBatch_Multiple(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "batch-sess", Source: "test"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	msgs := []*MessageRecord{
		{SessionID: "batch-sess", Role: "user", Content: "msg1"},
		{SessionID: "batch-sess", Role: "assistant", Content: "msg2"},
		{SessionID: "batch-sess", Role: "user", Content: "msg3", ToolCalls: `[{"id":"call_1"}]`},
	}
	if err := store.InsertMessagesBatch(ctx, msgs); err != nil {
		t.Fatalf("InsertMessagesBatch: %v", err)
	}

	count, err := store.GetMessageCount(ctx, "batch-sess")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 messages, got %d", count)
	}

	session, err := store.GetSession(ctx, "batch-sess")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if session.MessageCount != 3 {
		t.Errorf("expected message_count=3, got %d", session.MessageCount)
	}
	if session.ToolCallCount != 1 {
		t.Errorf("expected tool_call_count=1, got %d", session.ToolCallCount)
	}
}

// TestInsertMessage_WithToolCall 测试工具调用消息
