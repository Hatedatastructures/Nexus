package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNewStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if store.db == nil {
		t.Fatal("store.db is nil")
	}
	if store.DB() == nil {
		t.Fatal("store.DB() returned nil")
	}
}



func TestGetCompressionTip_NoChain(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "tip-1", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	got, err := store.GetCompressionTip(ctx, "tip-1")
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	if got == nil || got.ID != "tip-1" {
		t.Error("should return the same session when no chain exists")
	}
}



func TestGetCompressionTip_WithChain(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := float64(time.Now().Unix())

	// Parent session (ended with compression)
	parent := &Session{ID: "chain-parent", Source: "cli", StartedAt: now - 100}
	store.CreateSession(ctx, parent)
	store.EndSession(ctx, "chain-parent", "compression")

	// Child session (compression continuation)
	// Use a started_at well after the parent's ended_at (set by EndSession at runtime)
	child := &Session{
		ID:              "chain-child",
		Source:          "cli",
		ParentSessionID: "chain-parent",
		StartedAt:       now + 200,
	}
	store.CreateSession(ctx, child)

	got, err := store.GetCompressionTip(ctx, "chain-parent")
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	if got == nil {
		t.Fatal("GetCompressionTip returned nil")
	}
	if got.ID != "chain-child" {
		t.Errorf("tip = %q, want %q", got.ID, "chain-child")
	}
}



func TestGetCompressionTip_NonexistentSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	got, err := store.GetCompressionTip(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetCompressionTip error: %v", err)
	}
	if got != nil {
		t.Error("should return nil for nonexistent session")
	}
}

// ── Message CRUD 测试 ─────────────────────────────────────────



func TestAutoPrune(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create an old ended session
	oldSession := &Session{
		ID:        "old-session",
		Source:    "cli",
		StartedAt: float64(time.Now().Unix() - 200*86400), // 200 days ago
	}
	store.CreateSession(ctx, oldSession)
	store.EndSession(ctx, "old-session", "completed")

	// Create a new active session
	newSession := &Session{
		ID:        "new-session",
		Source:    "cli",
		StartedAt: float64(time.Now().Unix()),
	}
	store.CreateSession(ctx, newSession)

	// Prune sessions older than 90 days
	removed, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	// Old session should be gone
	got, _ := store.GetSession(ctx, "old-session")
	if got != nil {
		t.Error("old session should be pruned")
	}

	// New session should remain
	got2, _ := store.GetSession(ctx, "new-session")
	if got2 == nil {
		t.Error("new session should not be pruned")
	}
}



func TestAutoPrune_DefaultMaxAge(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create session with negative maxAge → should default to 90
	session := &Session{
		ID:        "prune-default",
		Source:    "cli",
		StartedAt: float64(time.Now().Unix() - 100*86400),
	}
	store.CreateSession(ctx, session)
	store.EndSession(ctx, "prune-default", "done")

	removed, err := store.AutoPrune(ctx, -1)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
}



func TestAutoPrune_NoExpiredSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{
		ID:        "active-session",
		Source:    "cli",
		StartedAt: float64(time.Now().Unix()),
	}
	store.CreateSession(ctx, session)

	removed, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}



func TestAutoPrune_OrphansChildren(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Old parent with child
	parent := &Session{
		ID:        "old-parent",
		Source:    "cli",
		StartedAt: float64(time.Now().Unix() - 200*86400),
	}
	store.CreateSession(ctx, parent)
	store.EndSession(ctx, "old-parent", "done")

	child := &Session{
		ID:              "orphan-child",
		Source:          "cli",
		ParentSessionID: "old-parent",
		StartedAt:       float64(time.Now().Unix() - 200*86400),
	}
	store.CreateSession(ctx, child)
	store.EndSession(ctx, "orphan-child", "done")

	removed, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (parent and child both old)", removed)
	}
}

// ── CheckpointWAL 测试 ────────────────────────────────────────



func TestCheckpointWAL(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatalf("CheckpointWAL: %v", err)
	}
}

// ── SessionPersister 测试 ─────────────────────────────────────



func TestExecuteWrite_CancelledContext(t *testing.T) {
	store := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := store.executeWrite(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, "CREATE TABLE should_not_exist (id INTEGER)")
		return err
	})
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}



func TestExecuteWrite_Success(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.executeWrite(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, "CREATE TABLE test_write (id INTEGER)")
		return err
	})
	if err != nil {
		t.Fatalf("executeWrite: %v", err)
	}

	var count int
	store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name='test_write'").Scan(&count)
	if count != 1 {
		t.Error("table should have been created")
	}
}

// ── 并发安全测试 ──────────────────────────────────────────────



func TestConcurrentWrites(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			s := &Session{
				ID:        fmt.Sprintf("concurrent-%d", idx),
				Source:    "cli",
				StartedAt: float64(time.Now().Unix()),
			}
			if err := store.CreateSession(ctx, s); err != nil {
				t.Errorf("goroutine %d: CreateSession failed: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	sessions, _ := store.ListSessions(ctx, nil)
	if len(sessions) != goroutines {
		t.Errorf("got %d sessions, want %d", len(sessions), goroutines)
	}
}

// ── isLockedErr 测试 ───────────────────────────────────────────



func TestTryCheckpoint(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "cp-test", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess)
	store.InsertMessage(ctx, &MessageRecord{
		SessionID: "cp-test", Role: "user", Content: "checkpoint test",
	})

	store.tryCheckpoint(ctx)
}



func TestTryCheckpoint_EmptyDB(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	store.tryCheckpoint(ctx)
}

// ── createFTSDefault / createFTSTrigram 测试 ──────────────────



func TestAutoPrune_WithExpiredSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create an expired session (started 100 days ago)
	oldTime := float64(time.Now().Add(-100 * 24 * time.Hour).Unix())
	store.CreateSession(ctx, &Session{ID: "expired", Source: "test", StartedAt: oldTime})
	store.EndSession(ctx, "expired", "complete")

	// Create a recent session
	store.CreateSession(ctx, &Session{ID: "recent", Source: "test", StartedAt: float64(time.Now().Unix())})

	count, err := store.AutoPrune(ctx, 30)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 pruned, got %d", count)
	}

	// Recent session should still exist
	sess, _ := store.GetSession(ctx, "recent")
	if sess == nil {
		t.Error("recent session should not be pruned")
	}

	// Expired session should be gone
	sess, _ = store.GetSession(ctx, "expired")
	if sess != nil {
		t.Error("expired session should be pruned")
	}
}

// ── searchCJKLike 测试 ─────────────────────────────────────────
