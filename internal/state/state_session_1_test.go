package state

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestCreateAndGetSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := float64(time.Now().Unix())
	session := &Session{
		ID:        "test-session-1",
		Source:    "cli",
		UserID:    "user-123",
		Model:     "claude-3",
		StartedAt: now,
	}

	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	got, err := store.GetSession(ctx, "test-session-1")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got == nil {
		t.Fatal("GetSession returned nil")
	}
	if got.ID != "test-session-1" {
		t.Errorf("got.ID = %q, want %q", got.ID, "test-session-1")
	}
	if got.Source != "cli" {
		t.Errorf("got.Source = %q, want %q", got.Source, "cli")
	}
	if got.UserID != "user-123" {
		t.Errorf("got.UserID = %q, want %q", got.UserID, "user-123")
	}
	if got.Model != "claude-3" {
		t.Errorf("got.Model = %q, want %q", got.Model, "claude-3")
	}
}



func TestCreateSession_IDExists(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "dup-id", Source: "cli", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("first CreateSession: %v", err)
	}
	// INSERT OR IGNORE — second call should not error
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("duplicate CreateSession should not error: %v", err)
	}
}



func TestCreateSession_DefaultStartedAt(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "auto-ts", Source: "cli"}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, _ := store.GetSession(ctx, "auto-ts")
	if got == nil {
		t.Fatal("session not found")
	}
	if got.StartedAt == 0 {
		t.Error("StartedAt should be auto-populated when left as 0")
	}
}



func TestGetSession_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	got, err := store.GetSession(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetSession unexpected error: %v", err)
	}
	if got != nil {
		t.Error("GetSession should return nil for nonexistent session")
	}
}



func TestUpdateSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{
		ID:        "update-me",
		Source:    "cli",
		Model:     "old-model",
		StartedAt: float64(time.Now().Unix()),
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	session.Model = "new-model"
	session.Title = "updated title"
	session.MessageCount = 5
	session.ToolCallCount = 2
	session.InputTokens = 100
	session.OutputTokens = 200
	session.CacheReadTokens = 50
	session.CacheWriteTokens = 30
	session.EstimatedCostUSD = 0.05
	session.APICallCount = 3

	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	got, _ := store.GetSession(ctx, "update-me")
	if got == nil {
		t.Fatal("session not found after update")
	}
	if got.Model != "new-model" {
		t.Errorf("Model = %q, want %q", got.Model, "new-model")
	}
	if got.Title != "updated title" {
		t.Errorf("Title = %q, want %q", got.Title, "updated title")
	}
	if got.MessageCount != 5 {
		t.Errorf("MessageCount = %d, want 5", got.MessageCount)
	}
	if got.ToolCallCount != 2 {
		t.Errorf("ToolCallCount = %d, want 2", got.ToolCallCount)
	}
	if got.EstimatedCostUSD != 0.05 {
		t.Errorf("EstimatedCostUSD = %v, want 0.05", got.EstimatedCostUSD)
	}
}



func TestEndSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "end-me", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	if err := store.EndSession(ctx, "end-me", "completed"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	got, _ := store.GetSession(ctx, "end-me")
	if got == nil {
		t.Fatal("session not found")
	}
	if got.EndedAt == 0 {
		t.Error("EndedAt should be set after EndSession")
	}
	if got.EndReason != "completed" {
		t.Errorf("EndReason = %q, want %q", got.EndReason, "completed")
	}
}



func TestEndSession_FirstWriterWins(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "first-wins", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	store.EndSession(ctx, "first-wins", "compression")
	store.EndSession(ctx, "first-wins", "user_exit")

	got, _ := store.GetSession(ctx, "first-wins")
	if got.EndReason != "compression" {
		t.Errorf("EndReason = %q, want first writer 'compression'", got.EndReason)
	}
}



func TestEndSession_AlreadyEnded(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "already-ended", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	// First end
	store.EndSession(ctx, "already-ended", "done")
	got, _ := store.GetSession(ctx, "already-ended")
	firstEnd := got.EndedAt

	// Second end — should be no-op
	store.EndSession(ctx, "already-ended", "other")
	got2, _ := store.GetSession(ctx, "already-ended")
	if got2.EndedAt != firstEnd {
		t.Error("second EndSession should not change EndedAt")
	}
}



func TestListSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		s := &Session{
			ID:        fmt.Sprintf("list-%d", i),
			Source:    "cli",
			UserID:    "user-1",
			StartedAt: float64(time.Now().Unix() + int64(i)),
		}
		store.CreateSession(ctx, s)
	}

	// End one session
	store.EndSession(ctx, "list-2", "done")

	tests := []struct {
		name   string
		filter *SessionFilter
		want   int
	}{
		{"all", nil, 5},
		{"source filter", &SessionFilter{Source: "cli"}, 5},
		{"ended true", &SessionFilter{Ended: boolPtr(true)}, 1},
		{"ended false", &SessionFilter{Ended: boolPtr(false)}, 4},
		{"with limit", &SessionFilter{Limit: 2}, 2},
		{"with offset", &SessionFilter{Offset: 3}, 2},
		{"user filter", &SessionFilter{UserID: "user-1"}, 5},
		{"no match source", &SessionFilter{Source: "api"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessions, err := store.ListSessions(ctx, tt.filter)
			if err != nil {
				t.Fatalf("ListSessions error: %v", err)
			}
			if len(sessions) != tt.want {
				t.Errorf("got %d sessions, want %d", len(sessions), tt.want)
			}
		})
	}
}



func TestListSessions_EmptyResult(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sessions, err := store.ListSessions(ctx, nil)
	if err != nil {
		t.Fatalf("ListSessions error: %v", err)
	}
	if sessions == nil {
		t.Error("should return empty slice, not nil")
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}



func TestListRecentSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		s := &Session{
			ID:        fmt.Sprintf("recent-%d", i),
			Source:    "cli",
			StartedAt: float64(time.Now().Unix() - int64(i*100)),
		}
		store.CreateSession(ctx, s)
	}

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("got %d sessions, want 3", len(sessions))
	}
}



func TestListRecentSessions_DefaultLimit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	s := &Session{ID: "recent-default", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, s)

	sessions, err := store.ListRecentSessions(ctx, 0)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("got %d sessions with default limit, want 1", len(sessions))
	}
}

// ── AutoPrune 测试 ─────────────────────────────────────────────



func TestListRecentSessions_WithData(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := float64(time.Now().Unix())

	// Create sessions in different order than expected output
	store.CreateSession(ctx, &Session{ID: "old-session", Source: "test", StartedAt: now - 100})
	store.CreateSession(ctx, &Session{ID: "new-session", Source: "test", StartedAt: now})

	// Insert messages to set last_active
	store.InsertMessage(ctx, &MessageRecord{SessionID: "old-session", Role: "user", Content: "old", Timestamp: now - 50})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "new-session", Role: "user", Content: "new", Timestamp: now})

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	// Most recent first
	if sessions[0].ID != "new-session" {
		t.Errorf("first session = %q, want new-session", sessions[0].ID)
	}
}



func TestListRecentSessions_Empty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions empty: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}
