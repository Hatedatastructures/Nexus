package state

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAutoPrune_OrphanCleanup(t *testing.T) {
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

	// Create parent session that is already ended and old enough to prune
	parent := &Session{
		ID:        "old-parent",
		Source:    "test",
		StartedAt: float64(time.Now().Unix() - 200*86400),
	}
	if err := store.CreateSession(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := store.EndSession(ctx, "old-parent", "done"); err != nil {
		t.Fatal(err)
	}

	// Create child session referencing the parent
	child := &Session{
		ID:              "child-session",
		Source:          "test",
		ParentSessionID: "old-parent",
		StartedAt:       float64(time.Now().Unix() - 200*86400),
	}
	if err := store.CreateSession(ctx, child); err != nil {
		t.Fatal(err)
	}

	// Prune with 1 day max age - should delete old-parent and orphan the child
	pruned, err := store.AutoPrune(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned, got %d", pruned)
	}

	// Child should still exist with parent_session_id cleared
	childSession, err := store.GetSession(ctx, "child-session")
	if err != nil {
		t.Fatal(err)
	}
	if childSession == nil {
		t.Fatal("child session should still exist")
	}
	if childSession.ParentSessionID != "" {
		t.Errorf("expected orphaned child to have empty parent_session_id, got %q", childSession.ParentSessionID)
	}
}



func TestAutoPrune_DeletesEndedSessions(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	// Create an ended session
	sess := &Session{
		ID:      "prune-me",
		Source:  "test",
		StartedAt: float64(time.Now().Unix() - 200*86400),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}

	// End it
	if err := store.EndSession(ctx, "prune-me", "completed"); err != nil {
		t.Fatal(err)
	}

	// Insert a message for this session
	msg := &MessageRecord{
		SessionID: "prune-me",
		Role:      "user",
		Content:   "hello",
		Timestamp: float64(time.Now().Unix()),
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}

	// Run prune with maxSessions=0 to force deletion
	pruned, err := store.AutoPrune(ctx, 0)
	if err != nil {
		t.Fatalf("AutoPrune error: %v", err)
	}
	if pruned < 1 {
		t.Errorf("expected at least 1 pruned session, got %d", pruned)
	}

	// Verify session is gone
	got, _ := store.GetSession(ctx, "prune-me")
	if got != nil {
		t.Error("session should have been pruned")
	}
}



func TestAutoPrune_DeletesWithMessages(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Create old ended session with messages
	sess := &Session{
		ID:        "prune-with-msgs",
		Source:    "test",
		StartedAt: float64(time.Now().Unix() - 200*86400),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		msg := &MessageRecord{
			SessionID: "prune-with-msgs",
			Role:      "user",
			Content:   fmt.Sprintf("msg-%d", i),
			Timestamp: float64(time.Now().Unix()),
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	// End the session
	if err := store.EndSession(ctx, "prune-with-msgs", "done"); err != nil {
		t.Fatal(err)
	}
	count, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 pruned session, got %d", count)
	}
	// Verify messages are gone too
	msgCount, _ := store.GetMessageCount(ctx, "prune-with-msgs")
	if msgCount != 0 {
		t.Errorf("expected 0 messages after prune, got %d", msgCount)
	}
}



func TestGetCompressionTip_TraversalDepth(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Build a chain of compression sessions
	parentID := "depth-root"
	sess := &Session{ID: parentID, Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		parent, _ := store.GetSession(ctx, parentID)
		childID := fmt.Sprintf("depth-child-%d", i)
		child := &Session{
			ID:              childID,
			Source:          "test",
			ParentSessionID: parentID,
			StartedAt:       parent.StartedAt + 1,
		}
		if err := store.CreateSession(ctx, child); err != nil {
			t.Fatal(err)
		}
		// End parent as compression
		store.EndSession(ctx, parentID, "compression")
		parentID = childID
	}
	tip, err := store.GetCompressionTip(ctx, "depth-root")
	if err != nil {
		t.Fatal(err)
	}
	if tip == nil {
		t.Fatal("expected non-nil tip")
	}
	if tip.ID != "depth-child-4" {
		t.Errorf("expected deepest child, got %s", tip.ID)
	}
}



func TestCheckpointWAL_WithWALData(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Write some data to generate WAL frames
	for i := 0; i < 10; i++ {
		sess := &Session{
			ID:        fmt.Sprintf("wal-sess-%d", i),
			Source:    "test",
			StartedAt: float64(time.Now().Unix()),
		}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatal(err)
		}
	}
	// Now checkpoint
	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatalf("CheckpointWAL error: %v", err)
	}
}



func TestGetCompressionTip_NoChildren(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "tip-nochild", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	tip, err := store.GetCompressionTip(ctx, "tip-nochild")
	if err != nil {
		t.Fatal(err)
	}
	if tip == nil {
		t.Fatal("tip should not be nil")
	}
	if tip.ID != "tip-nochild" {
		t.Errorf("expected tip ID tip-nochild, got %s", tip.ID)
	}
}



func TestGetCompressionTip_Nonexistent(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	tip, err := store.GetCompressionTip(ctx, "nonexistent-session")
	if err != nil {
		t.Fatal(err)
	}
	if tip != nil {
		t.Error("expected nil for nonexistent session")
	}
}




func TestCheckpointWAL_FreshDB(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatalf("checkpoint on fresh DB should not error: %v", err)
	}
}




func TestAutoPrune_ActuallyDeletes(t *testing.T) {
	dir := t.TempDir() + "/test.db"
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Create a session and end it with old timestamp
	sess := &Session{
		ID:        "prune-old",
		StartedAt: float64(time.Now().Add(-200 * 24 * time.Hour).Unix()),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := store.EndSession(ctx, "prune-old", "done"); err != nil {
		t.Fatal(err)
	}
	// Create an active session that should NOT be pruned
	sess2 := &Session{
		ID:        "prune-active",
		StartedAt: float64(time.Now().Add(-200 * 24 * time.Hour).Unix()),
	}
	if err := store.CreateSession(ctx, sess2); err != nil {
		t.Fatal(err)
	}
	count, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 pruned session, got %d", count)
	}
	// Active session should still exist
	_, err = store.GetSession(ctx, "prune-active")
	if err != nil {
		t.Fatalf("active session should still exist: %v", err)
	}
}




func TestCheckpointWAL_WithData(t *testing.T) {
	dir := t.TempDir() + "/test.db"
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Write some data to generate WAL entries
	sess := &Session{ID: "wal-sess", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatalf("CheckpointWAL should not fail: %v", err)
	}
}
