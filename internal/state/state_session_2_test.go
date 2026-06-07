package state

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestListRecentSessions_OrderByActivity(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create two sessions with different timestamps
	s1 := &Session{ID: "recent-s1", Source: "test", StartedAt: float64(time.Now().Add(-2 * time.Hour).Unix())}
	s2 := &Session{ID: "recent-s2", Source: "test", StartedAt: float64(time.Now().Add(-1 * time.Hour).Unix())}
	if err := store.CreateSession(ctx, s1); err != nil {
		t.Fatalf("CreateSession s1: %v", err)
	}
	if err := store.CreateSession(ctx, s2); err != nil {
		t.Fatalf("CreateSession s2: %v", err)
	}

	// Add a message to s1 to make it more recently active
	msg := &MessageRecord{
		SessionID: "recent-s1",
		Role:      "user",
		Content:   "hello",
		Timestamp: float64(time.Now().Unix()),
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	// s1 should be first because it has a recent message
	if sessions[0].ID != "recent-s1" {
		t.Errorf("expected recent-s1 first, got %s", sessions[0].ID)
	}
}



func TestListSessions_WithFilter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	s1 := &Session{ID: "f-src1", Source: "api", StartedAt: float64(time.Now().Unix())}
	s2 := &Session{ID: "f-src2", Source: "cli", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, s1); err != nil {
		t.Fatalf("CreateSession s1: %v", err)
	}
	if err := store.CreateSession(ctx, s2); err != nil {
		t.Fatalf("CreateSession s2: %v", err)
	}

	filtered, err := store.ListSessions(ctx, &SessionFilter{Source: "api"})
	if err != nil {
		t.Fatalf("ListSessions filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 session, got %d", len(filtered))
	}
	if filtered[0].ID != "f-src1" {
		t.Errorf("expected f-src1, got %s", filtered[0].ID)
	}
}



func TestListSessions_Empty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sessions, err := store.ListSessions(ctx, nil)
	if err != nil {
		t.Fatalf("ListSessions empty: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}



func TestListSessions_FilterEnded(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	s1 := &Session{ID: "active-f", Source: "test", StartedAt: float64(time.Now().Unix())}
	s2 := &Session{ID: "ended-f", Source: "test", StartedAt: float64(time.Now().Add(-1*time.Hour).Unix())}
	if err := store.CreateSession(ctx, s1); err != nil {
		t.Fatalf("CreateSession s1: %v", err)
	}
	if err := store.CreateSession(ctx, s2); err != nil {
		t.Fatalf("CreateSession s2: %v", err)
	}
	if err := store.EndSession(ctx, "ended-f", "done"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	ended := true
	filtered, err := store.ListSessions(ctx, &SessionFilter{Ended: &ended})
	if err != nil {
		t.Fatalf("ListSessions ended: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 ended session, got %d", len(filtered))
	}
	if filtered[0].ID != "ended-f" {
		t.Errorf("expected ended-f, got %s", filtered[0].ID)
	}

	active := false
	activeSess, err := store.ListSessions(ctx, &SessionFilter{Ended: &active})
	if err != nil {
		t.Fatalf("ListSessions active: %v", err)
	}
	if len(activeSess) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(activeSess))
	}
	if activeSess[0].ID != "active-f" {
		t.Errorf("expected active-f, got %s", activeSess[0].ID)
	}
}



func TestListSessions_Pagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := range 5 {
		sess := &Session{
			ID:        fmt.Sprintf("page-%d", i),
			Source:    "test",
			StartedAt: float64(time.Now().Add(-time.Duration(i) * time.Hour).Unix()),
		}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession %d: %v", i, err)
		}
	}

	// Limit 2, offset 0
	page1, err := store.ListSessions(ctx, &SessionFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("ListSessions page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2, got %d", len(page1))
	}

	// Limit 2, offset 2
	page2, err := store.ListSessions(ctx, &SessionFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("ListSessions page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2, got %d", len(page2))
	}

	// Pages should have different IDs
	if page1[0].ID == page2[0].ID {
		t.Error("pages should have different sessions")
	}
}



// TestListRecentSessions_SessionWithoutMessages 测试没有消息的会话排序
func TestListRecentSessions_SessionWithoutMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create session with no messages
	store.CreateSession(ctx, &Session{
		ID:        "no-msg-session",
		Source:    "test",
		StartedAt: float64(time.Now().Unix()),
	})

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "no-msg-session" {
		t.Errorf("expected no-msg-session, got %s", sessions[0].ID)
	}
}

// TestSearchCJKLike_ToolCallsField 测试 CJK LIKE 搜索 tool_calls 字段


func TestListRecentSessions_WithMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create sessions with different message timestamps
	for _, id := range []string{"s1", "s2", "s3"} {
		sess := &Session{ID: id, Source: "test"}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession %s: %v", id, err)
		}
	}

	// Insert messages in different order
	now := float64(time.Now().Unix())
	msgs := []*MessageRecord{
		{SessionID: "s1", Role: "user", Content: "first", Timestamp: now - 100},
		{SessionID: "s2", Role: "user", Content: "second", Timestamp: now},
		{SessionID: "s3", Role: "user", Content: "third", Timestamp: now - 50},
	}
	for _, m := range msgs {
		if err := store.InsertMessage(ctx, m); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	results, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(results))
	}
	// Most recently active should be first (s2 has the latest message)
	if results[0].ID != "s2" {
		t.Errorf("expected first session to be s2, got %s", results[0].ID)
	}
}

// TestGetCompressionTip_NoChild 测试没有压缩子会话时返回自身


// TestListRecentSessions_SingleSessionNoMessages 测试只有一个会话且无消息
func TestListRecentSessions_SingleSessionNoMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateSession(ctx, &Session{ID: "solo", Source: "test"})

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "solo" {
		t.Errorf("session ID = %q, want %q", sessions[0].ID, "solo")
	}
}

// ── NewStore 错误路径覆盖 ─────────────────────────────────

// ── GetCompressionTip 边界路径覆盖 ───────────────────────

// TestGetCompressionTip_MaxDepth 测试超过最大遍历深度


func TestEndSession_Nonexistent(t *testing.T) {
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

	if err := store.EndSession(ctx, "no-such-session", "test"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}



func TestListRecentSessions_WithActiveAndEnded(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Create active session with message
	sess1 := &Session{ID: "recent-active", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess1)
	msg1 := &MessageRecord{SessionID: "recent-active", Role: "user", Content: "hello", Timestamp: float64(time.Now().Unix())}
	store.InsertMessage(ctx, msg1)
	// Create ended session with message
	sess2 := &Session{ID: "recent-ended", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess2)
	msg2 := &MessageRecord{SessionID: "recent-ended", Role: "user", Content: "world", Timestamp: float64(time.Now().Unix())}
	store.InsertMessage(ctx, msg2)
	store.EndSession(ctx, "recent-ended", "done")
	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
}



func TestCreateSession_Duplicate(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "dupe-sess"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal("INSERT OR IGNORE should not fail on duplicate")
	}
}



func TestCreateSession_SetsStartedAt(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	before := float64(time.Now().Unix())
	sess := &Session{ID: "ts-sess"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(ctx, "ts-sess")
	if err != nil {
		t.Fatal(err)
	}
	if got.StartedAt < before-2 || got.StartedAt == 0 {
		t.Fatalf("expected StartedAt to be set, got %f", got.StartedAt)
	}
}
