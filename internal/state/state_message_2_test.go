package state

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestInsertMessage_WithToolCall 测试工具调用消息
func TestInsertMessage_WithToolCall(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "tool-sess", Source: "test"}
	store.CreateSession(ctx, sess)

	msg := &MessageRecord{
		SessionID: "tool-sess",
		Role:      "assistant",
		Content:   "calling tool",
		ToolCalls: `[{"name":"read"}]`,
		ToolName:  "read",
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if msg.ID == 0 {
		t.Error("expected non-zero message ID after insert")
	}

	session, _ := store.GetSession(ctx, "tool-sess")
	if session.ToolCallCount != 1 {
		t.Errorf("expected tool_call_count=1, got %d", session.ToolCallCount)
	}
}

// TestGetMessages_EmptySession 测试空会话返回空列表


// TestGetMessages_EmptySession 测试空会话返回空列表
func TestGetMessages_EmptySession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "empty-sess", Source: "test"}
	store.CreateSession(ctx, sess)

	msgs, err := store.GetMessages(ctx, "empty-sess", 0, 0)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty, got %d", len(msgs))
	}
}

// ── RunMigrations 高级路径覆盖 ─────────────────────────────

// TestRunMigrations_ReconcileError 测试 reconcileColumns 在已损坏的表上仍能完成


// TestGetMessageCount_SpecificSession 测试指定会话的消息计数
func TestGetMessageCount_SpecificSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateSession(ctx, &Session{ID: "count-sess", Source: "test"})
	store.CreateSession(ctx, &Session{ID: "other-sess", Source: "test"})

	for i := range 5 {
		store.InsertMessage(ctx, &MessageRecord{
			SessionID: "count-sess",
			Role:      "user",
			Content:   fmt.Sprintf("msg %d", i),
			Timestamp: float64(i),
		})
	}
	store.InsertMessage(ctx, &MessageRecord{
		SessionID: "other-sess",
		Role:      "user",
		Content:   "other",
		Timestamp: 100,
	})

	count, err := store.GetMessageCount(ctx, "count-sess")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 5 {
		t.Errorf("GetMessageCount = %d, want 5", count)
	}
}

// ── migrateV11 回填失败路径覆盖 ───────────────────────────

// TestMigrateV11_BackfillWarn 测试 v11 回填时无消息数据的 warn 路径


func TestGetMessageCount_EmptyDB(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	count, err := store.GetMessageCount(ctx, "nonexistent-session")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 messages for nonexistent session, got %d", count)
	}
}



// -- Coverage boost round 2: targeted tests for low-coverage paths --



func TestGetMessages_WithOffsetAndLimit(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "pag-sess", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		msg := &MessageRecord{
			SessionID: "pag-sess",
			Role:      "user",
			Content:   fmt.Sprintf("page-msg-%d", i),
			Timestamp: float64(time.Now().Unix()) + float64(i),
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := store.GetMessages(ctx, "pag-sess", 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 5 {
		t.Errorf("expected 5 messages, got %d", len(msgs))
	}
	// First message should be offset by 3
	if !strings.Contains(msgs[0].Content, "page-msg-3") {
		t.Errorf("expected offset 3, got %s", msgs[0].Content)
	}
}



func TestInsertMessage_AutoTimestamp(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "ts-msg-sess"})
	before := float64(time.Now().Unix())
	store.InsertMessage(ctx, &MessageRecord{SessionID: "ts-msg-sess", Role: "user", Content: "test"})
	msgs, _ := store.GetMessages(ctx, "ts-msg-sess", 0, 0)
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	if msgs[0].Timestamp < before-2 {
		t.Fatalf("expected auto timestamp, got %f", msgs[0].Timestamp)
	}
}



func TestInsertMessage_IncrementCounts(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "cnt-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "cnt-sess", Role: "user", Content: "m1"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "cnt-sess", Role: "user", Content: "m2", ToolCalls: "[{\"id\":\"t1\"}]"})
	got, _ := store.GetSession(ctx, "cnt-sess")
	if got.MessageCount != 2 {
		t.Fatalf("expected MessageCount=2, got %d", got.MessageCount)
	}
	if got.ToolCallCount != 1 {
		t.Fatalf("expected ToolCallCount=1, got %d", got.ToolCallCount)
	}
}




func TestGetMessages_WithOffset(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "off-sess"})
	for i := 0; i < 5; i++ {
		store.InsertMessage(ctx, &MessageRecord{SessionID: "off-sess", Role: "user", Content: fmt.Sprintf("msg-%d", i)})
	}
	msgs, err := store.GetMessages(ctx, "off-sess", 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages with limit=3 offset=2, got %d", len(msgs))
	}
}



func TestGetMessageCount_Empty(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "empty-cnt-sess"})
	cnt, err := store.GetMessageCount(ctx, "empty-cnt-sess")
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("expected 0 messages, got %d", cnt)
	}
}



func TestGetMessageCount_WithMessages(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	store.CreateSession(ctx, &Session{ID: "count-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "count-sess", Role: "user", Content: "msg1"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "count-sess", Role: "assistant", Content: "msg2"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "count-sess", Role: "user", Content: "msg3"})

	count, err := store.GetMessageCount(ctx, "count-sess")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("expected 3 messages, got %d", count)
	}

	// Empty session
	count, err = store.GetMessageCount(ctx, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 messages for nonexistent session, got %d", count)
	}
}



func TestInsertMessagesBatch_MultipleMessages(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	store.CreateSession(ctx, &Session{ID: "batch-sess"})

	msgs := []*MessageRecord{
		{SessionID: "batch-sess", Role: "user", Content: "batch1"},
		{SessionID: "batch-sess", Role: "assistant", Content: "batch2"},
		{SessionID: "batch-sess", Role: "user", Content: "batch3"},
	}
	if err := store.InsertMessagesBatch(ctx, msgs); err != nil {
		t.Fatal(err)
	}

	count, err := store.GetMessageCount(ctx, "batch-sess")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("expected 3 messages after batch insert, got %d", count)
	}
}



func TestGetMessages_WithLimit(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	store.CreateSession(ctx, &Session{ID: "msg-limit-sess"})
	for i := 0; i < 5; i++ {
		store.InsertMessage(ctx, &MessageRecord{
			SessionID: "msg-limit-sess",
			Role:      "user",
			Content:   fmt.Sprintf("msg %d", i),
		})
	}

	// Get with limit 3
	msgs, err := store.GetMessages(ctx, "msg-limit-sess", 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages with limit, got %d", len(msgs))
	}

	// Get all (limit 0 defaults to 100)
	msgs, err = store.GetMessages(ctx, "msg-limit-sess", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages with default limit, got %d", len(msgs))
	}
}
