package state

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestGetMessages_LimitAndOffset(t *testing.T) {
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

	store.CreateSession(ctx, &Session{ID: "limoff-sess", Source: "test"})
	for i := 0; i < 10; i++ {
		store.InsertMessage(ctx, &MessageRecord{
			SessionID: "limoff-sess",
			Role:      "user",
			Content:   fmt.Sprintf("msg %d", i),
		})
	}

	// Limit only
	msgs, err := store.GetMessages(ctx, "limoff-sess", 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// Limit + offset
	msgs, err = store.GetMessages(ctx, "limoff-sess", 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages with offset, got %d", len(msgs))
	}

	// No limit
	msgs, err = store.GetMessages(ctx, "limoff-sess", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(msgs))
	}
}




func TestGetMessageCount_WithData(t *testing.T) {
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

	store.CreateSession(ctx, &Session{ID: "count-sess", Source: "test"})
	for i := 0; i < 5; i++ {
		store.InsertMessage(ctx, &MessageRecord{
			SessionID: "count-sess",
			Role:      "user",
			Content:   fmt.Sprintf("msg %d", i),
		})
	}

	count, err := store.GetMessageCount(ctx, "count-sess")
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("expected 5 messages, got %d", count)
	}
}




func TestInsertMessage_WithAllFields(t *testing.T) {
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

	store.CreateSession(ctx, &Session{ID: "allfields-sess", Source: "test"})
	msg := &MessageRecord{
		SessionID:   "allfields-sess",
		Role:        "assistant",
		Content:     "I used a tool",
		ToolCallID:  "call_123",
		ToolCalls:   "[{\"name\":\"bash\"}]",
		ToolName:    "bash",
		TokenCount:  42,
		FinishReason: "stop",
		Reasoning:   "thinking about it",
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage with all fields failed: %v", err)
	}

	msgs, err := store.GetMessages(ctx, "allfields-sess", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.ToolCallID != "call_123" {
		t.Errorf("expected tool_call_id=call_123, got %s", m.ToolCallID)
	}
	if m.ToolName != "bash" {
		t.Errorf("expected tool_name=bash, got %s", m.ToolName)
	}
	if m.TokenCount != 42 {
		t.Errorf("expected token_count=42, got %d", m.TokenCount)
	}
	if m.FinishReason != "stop" {
		t.Errorf("expected finish_reason=stop, got %s", m.FinishReason)
	}
	if m.Reasoning != "thinking about it" {
		t.Errorf("expected reasoning, got %s", m.Reasoning)
	}
}







func TestInsertMessage_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, &Session{ID: "s1", Source: "test", StartedAt: float64(time.Now().Unix())}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	msg := &MessageRecord{SessionID: "s1", Role: "user", Content: "hello"}
	err = store.InsertMessage(ctx, msg)
	if err == nil {
		t.Fatal("expected error after close")
	}
}



func TestGetMessages_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	_, err = store.GetMessages(ctx, "s1", 10, 0)
	if err == nil {
		t.Fatal("expected error after close")
	}
}



func TestGetMessageCount_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	_, err = store.GetMessageCount(ctx, "s1")
	if err == nil {
		t.Fatal("expected error after close")
	}
}



func TestInsertMessagesBatch_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	msgs := []*MessageRecord{{SessionID: "s1", Role: "user", Content: "hi"}}
	err = store.InsertMessagesBatch(ctx, msgs)
	if err == nil {
		t.Fatal("expected error after close")
	}
}



func TestGetMessages_EmptySession2(t *testing.T) {
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
	if err := store.CreateSession(ctx, &Session{ID: "empty-sess", Source: "test", StartedAt: float64(time.Now().Unix())}); err != nil {
		t.Fatal(err)
	}
	msgs, err := store.GetMessages(ctx, "empty-sess", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}
