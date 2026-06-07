package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOrphanChildren_Empty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orphan.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Empty parentIDs should be no-op
	if err := orphanChildren(ctx, db, nil); err != nil {
		t.Errorf("orphanChildren with nil: %v", err)
	}
	if err := orphanChildren(ctx, db, []string{}); err != nil {
		t.Errorf("orphanChildren with empty: %v", err)
	}
}

// ── NewStore 错误路径测试 ───────────────────────────────────────



func TestNewStore_InvalidPath(t *testing.T) {
	// sql.Open does not validate paths — error surfaces on first query.
	// Verify the Store opens without error, but a real query fails.
	store, err := NewStore("/nonexistent/deep/path/db.sqlite")
	if err != nil {
		// Some drivers may error on Open — that's acceptable
		t.Logf("NewStore returned error on invalid path (acceptable): %v", err)
		return
	}
	defer store.Close()
	if pingErr := store.DB().PingContext(context.Background()); pingErr == nil {
		t.Error("expected error when pinging invalid path DB, got nil")
	}
}

// ── sanitizeFTS5Query 补充测试 ─────────────────────────────────



func TestExecuteWrite_FnError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	err := store.executeWrite(ctx, func(db *sql.DB) error {
		return fmt.Errorf("intentional fn error")
	})
	if err == nil {
		t.Fatal("expected error from fn callback, got nil")
	}
	if !strings.Contains(err.Error(), "intentional fn error") {
		t.Errorf("error should wrap fn error, got: %v", err)
	}
}



func TestAutoPrune_ExpiredSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{
		ID:        "prune-me",
		Source:    "test",
		StartedAt: float64(time.Now().Add(-48 * time.Hour).Unix()),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// End the session
	if err := store.EndSession(ctx, "prune-me", "timeout"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	// Manually set ended_at to past the prune threshold
	_, err := store.DB().ExecContext(ctx,
		"UPDATE sessions SET ended_at = ? WHERE id = ?",
		float64(time.Now().Add(-48*time.Hour).Unix()), "prune-me")
	if err != nil {
		t.Fatalf("set ended_at: %v", err)
	}

	deleted, err := store.AutoPrune(ctx, 1)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	got, err := store.GetSession(ctx, "prune-me")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got != nil {
		t.Error("expired session should be deleted")
	}
}



func TestAutoPrune_ActiveSessionNotPruned(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{
		ID:        "keep-me",
		Source:    "test",
		StartedAt: float64(time.Now().Unix()),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	deleted, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}

	got, err := store.GetSession(ctx, "keep-me")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got == nil {
		t.Error("active session should still exist")
	}
}



func TestGetCompressionTip_NonCompressionChild(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	parent := &Session{
		ID:        "parent-nc",
		Source:    "test",
		StartedAt: float64(time.Now().Add(-2 * time.Hour).Unix()),
	}
	if err := store.CreateSession(ctx, parent); err != nil {
		t.Fatalf("CreateSession parent: %v", err)
	}
	// End parent with non-compression reason
	if err := store.EndSession(ctx, "parent-nc", "user_stop"); err != nil {
		t.Fatalf("EndSession parent: %v", err)
	}

	child := &Session{
		ID:              "child-nc",
		Source:          "test",
		ParentSessionID: "parent-nc",
		StartedAt:       float64(time.Now().Add(-1 * time.Hour).Unix()),
	}
	if err := store.CreateSession(ctx, child); err != nil {
		t.Fatalf("CreateSession child: %v", err)
	}

	tip, err := store.GetCompressionTip(ctx, "parent-nc")
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	// Non-compression parent should return itself, not the child
	if tip.ID != "parent-nc" {
		t.Errorf("expected parent-nc, got %s", tip.ID)
	}
}



func TestNewStore_Close(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "close-test.db")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}



func TestStore_DB(t *testing.T) {
	store := newTestStore(t)
	if store.DB() == nil {
		t.Error("DB() should not return nil")
	}
}



func TestAutoPrune_WithOrphans(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	parent := &Session{
		ID:        "prune-parent",
		Source:    "test",
		StartedAt: float64(time.Now().Add(-48 * time.Hour).Unix()),
	}
	if err := store.CreateSession(ctx, parent); err != nil {
		t.Fatalf("CreateSession parent: %v", err)
	}
	// End parent
	if err := store.EndSession(ctx, "prune-parent", "done"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	// Set ended_at to old
	_, err := store.DB().ExecContext(ctx,
		"UPDATE sessions SET ended_at = ? WHERE id = ?",
		float64(time.Now().Add(-48*time.Hour).Unix()), "prune-parent")
	if err != nil {
		t.Fatalf("set ended_at: %v", err)
	}

	child := &Session{
		ID:              "orphan-child",
		Source:          "test",
		ParentSessionID: "prune-parent",
		StartedAt:       float64(time.Now().Unix()),
	}
	if err := store.CreateSession(ctx, child); err != nil {
		t.Fatalf("CreateSession child: %v", err)
	}

	deleted, err := store.AutoPrune(ctx, 1)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// Child should still exist but with orphaned parent
	got, err := store.GetSession(ctx, "orphan-child")
	if err != nil {
		t.Fatalf("GetSession child: %v", err)
	}
	if got == nil {
		t.Fatal("child should still exist")
	}
	if got.ParentSessionID != "" {
		t.Errorf("child parent_session_id should be empty (orphaned), got %q", got.ParentSessionID)
	}
}



func TestGetCompressionTip_Chain(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().Unix()
	// Create parent session
	p := &Session{ID: "chain-p", Source: "test", StartedAt: float64(now - 300)}
	if err := store.CreateSession(ctx, p); err != nil {
		t.Fatalf("CreateSession parent: %v", err)
	}
	// End with compression reason
	if err := store.EndSession(ctx, "chain-p", "compression"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	// Set ended_at to an old value so children satisfy started_at >= ended_at
	_, err := store.DB().ExecContext(ctx,
		"UPDATE sessions SET ended_at = ? WHERE id = ?",
		float64(now-250), "chain-p")
	if err != nil {
		t.Fatalf("set ended_at: %v", err)
	}

	// Create continuation child (started_at >= parent's ended_at)
	c1 := &Session{
		ID:              "chain-c1",
		Source:          "test",
		ParentSessionID: "chain-p",
		StartedAt:       float64(now - 200),
	}
	if err := store.CreateSession(ctx, c1); err != nil {
		t.Fatalf("CreateSession c1: %v", err)
	}
	if err := store.EndSession(ctx, "chain-c1", "compression"); err != nil {
		t.Fatalf("EndSession c1: %v", err)
	}
	// Set c1 ended_at to allow c2 to be a continuation
	_, err = store.DB().ExecContext(ctx,
		"UPDATE sessions SET ended_at = ? WHERE id = ?",
		float64(now-150), "chain-c1")
	if err != nil {
		t.Fatalf("set c1 ended_at: %v", err)
	}

	// Create second continuation child
	c2 := &Session{
		ID:              "chain-c2",
		Source:          "test",
		ParentSessionID: "chain-c1",
		StartedAt:       float64(now - 100),
	}
	if err := store.CreateSession(ctx, c2); err != nil {
		t.Fatalf("CreateSession c2: %v", err)
	}

	// GetCompressionTip from root should return c2
	tip, err := store.GetCompressionTip(ctx, "chain-p")
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	if tip.ID != "chain-c2" {
		t.Errorf("expected chain-c2 as tip, got %s", tip.ID)
	}
}



func TestGetCompressionTip_NoTip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "no-tip", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	tip, err := store.GetCompressionTip(ctx, "no-tip")
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	if tip.ID != "no-tip" {
		t.Errorf("expected no-tip, got %s", tip.ID)
	}
}



// TestExecuteWrite_CheckpointTrigger 触发 WAL checkpoint (每50次写入)
func TestExecuteWrite_CheckpointTrigger(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Do checkpointEvery writes to trigger checkpoint
	for i := range checkpointEvery {
		sess := &Session{
			ID:        fmt.Sprintf("cp-%d", i),
			Source:    "test",
			StartedAt: float64(time.Now().Unix()),
		}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession %d: %v", i, err)
		}
	}
	// If we got here without deadlock/panic, checkpoint was triggered
}

// TestRunMigrations_FromV9 测试从 v9 升级到 v11 (触发 v10 和 v11 迁移)
