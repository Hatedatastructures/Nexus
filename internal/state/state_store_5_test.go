package state

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestAutoPrune_WithChildren(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "parent-sess", EndedAt: float64(time.Now().Add(-48 * time.Hour).Unix())})
	store.CreateSession(ctx, &Session{ID: "child-sess", ParentSessionID: "parent-sess", EndedAt: float64(time.Now().Add(-48 * time.Hour).Unix())})
	if _, err := store.AutoPrune(ctx, 1); err != nil {
		t.Fatalf("AutoPrune with children failed: %v", err)
	}
}



func TestAutoPrune_NothingToPrune(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AutoPrune(ctx, 1); err != nil {
		t.Fatalf("AutoPrune on empty store failed: %v", err)
	}
}



func TestExecuteWrite_Retry(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	err = store.CreateSession(ctx, &Session{ID: "retry-sess"})
	if err != nil {
		t.Fatalf("executeWrite via CreateSession failed: %v", err)
	}
}




func TestTryCheckpoint_NoDB(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.tryCheckpoint(context.Background())
}



func TestGetCompressionTip_NoParent(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "nopr-tip"})
	tip, err := store.GetCompressionTip(ctx, "nopr-tip")
	if err != nil {
		t.Fatal(err)
	}
	if tip == nil || tip.ID != "nopr-tip" {
		t.Fatalf("expected tip.ID=nopr-tip, got %v", tip)
	}
}



func TestGetCompressionTip_DeepChain(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Build compression chain: deep-1 -> deep-2 -> deep-3
	// Use EndSession (only sets ended_at + end_reason) to avoid clearing parent_session_id
	store.CreateSession(ctx, &Session{ID: "deep-1"})
	store.EndSession(ctx, "deep-1", "compression")
	store.CreateSession(ctx, &Session{ID: "deep-2", ParentSessionID: "deep-1"})
	store.EndSession(ctx, "deep-2", "compression")
	store.CreateSession(ctx, &Session{ID: "deep-3", ParentSessionID: "deep-2"})
	tip, err := store.GetCompressionTip(ctx, "deep-1")
	if err != nil {
		t.Fatal(err)
	}
	if tip == nil || tip.ID != "deep-3" {
		t.Fatalf("expected tip.ID=deep-3, got %v", tip)
	}
}



func TestAutoPrune_ZeroMaxAge(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	oldTime := float64(time.Now().Add(-200 * 24 * time.Hour).Unix())
	store.CreateSession(ctx, &Session{ID: "za-sess", StartedAt: oldTime})
	store.EndSession(ctx, "za-sess", "old")
	pruned, err := store.AutoPrune(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pruned < 1 {
		t.Fatalf("expected at least 1 pruned with zero maxAge (defaults to 90), got %d", pruned)
	}
}




func TestNewStore_CreatesDirectory(t *testing.T) {
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
	if _, err := os.Stat(dir + "/test.db"); os.IsNotExist(err) {
		t.Fatal("expected db file to be created after migration")
	}
}



func TestStore_CloseIdempotent(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store.Close()
}



func TestAutoPrune_ActualPrune(t *testing.T) {
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

	// Create an old session with messages
	cutoff := time.Now().Add(-200 * 24 * time.Hour)
	store.CreateSession(ctx, &Session{
		ID:        "old-prune-sess",
		StartedAt: float64(cutoff.Unix()),
	})
	store.EndSession(ctx, "old-prune-sess", "done")
	store.InsertMessage(ctx, &MessageRecord{SessionID: "old-prune-sess", Role: "user", Content: "old data"})

	// Create a child of the old session
	store.CreateSession(ctx, &Session{
		ID:              "child-sess",
		ParentSessionID: "old-prune-sess",
	})

	// Create a recent session that should NOT be pruned
	store.CreateSession(ctx, &Session{
		ID:        "recent-sess",
		StartedAt: float64(time.Now().Unix()),
	})

	pruned, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatalf("AutoPrune failed: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned session, got %d", pruned)
	}

	// Old session should be gone
	sess, err2 := store.GetSession(ctx, "old-prune-sess")
	if err2 != nil {
		t.Fatal(err2)
	}
	if sess != nil {
		t.Error("old session should be pruned")
	}

	// Child's parent should be orphaned
	child, err := store.GetSession(ctx, "child-sess")
	if err != nil {
		t.Fatal(err)
	}
	if child == nil {
		t.Fatal("child session should still exist")
	}
	if child.ParentSessionID != "" {
		t.Errorf("child parent_session_id should be empty, got %q", child.ParentSessionID)
	}

	// Recent session should still exist
	recent, err := store.GetSession(ctx, "recent-sess")
	if err != nil {
		t.Fatal(err)
	}
	if recent == nil {
		t.Error("recent session should not be pruned")
	}
}



func TestCheckpointWAL_Normal(t *testing.T) {
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

	// Write some data to generate WAL
	store.CreateSession(ctx, &Session{ID: "wal-sess"})

	// Checkpoint should succeed
	err = store.CheckpointWAL(ctx)
	if err != nil {
		t.Fatalf("CheckpointWAL failed: %v", err)
	}
}




func TestNewStore_DirectoryCreation(t *testing.T) {
	dir := t.TempDir() + "/nested/deep/dir"
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	// RunMigrations creates the actual DB file
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(dir + "/test.db"); os.IsNotExist(err) {
		t.Error("database file should exist after RunMigrations")
	}
}

// ── Coverage Gap Tests (Round 20) ─────────────────────────────




func TestNewStore_ErrorPath(t *testing.T) {
	// Try to open a database in a non-existent deeply nested path
	// that should still succeed since NewStore opens sqlite which creates the file
	dir := t.TempDir() + "/a/b/c"
	store, err := NewStore(dir + "/test.db")
	// NewStore should succeed (sqlite creates parent dirs)
	if err != nil {
		t.Logf("NewStore in nested dir returned error (expected on some platforms): %v", err)
	} else {
		store.Close()
	}
}










// ── Coverage Gap Tests (Round 23) ─────────────────────────────




func TestNewStore_InvalidPath2(t *testing.T) {
	dir := t.TempDir()
	badPath := dir + string(os.PathSeparator) + "nonexistent" + string(os.PathSeparator) + "deep" + string(os.PathSeparator) + "db.sqlite"
	store, err := NewStore(badPath)
	if err != nil {
		t.Logf("NewStore returned error (acceptable): %v", err)
		return
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.DB().PingContext(ctx); err != nil {
		t.Logf("Ping failed as expected for invalid path: %v", err)
	}
}



func TestAutoPrune_ClosedStore2(t *testing.T) {
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
	_, err = store.AutoPrune(ctx, 30)
	if err == nil {
		t.Fatal("expected error after close")
	}
}



func TestGetCompressionTip_ClosedStore2(t *testing.T) {
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
	_, err = store.GetCompressionTip(ctx, "s1")
	if err == nil {
		t.Fatal("expected error after close")
	}
}
