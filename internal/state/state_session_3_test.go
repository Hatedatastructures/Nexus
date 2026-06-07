package state

import (
	"context"
	"testing"
)

func TestUpdateSession_PartialFields(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "part-sess", Source: "original"}
	store.CreateSession(ctx, sess)
	sess.Source = "updated"
	sess.Title = "new title"
	if err := store.UpdateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession(ctx, "part-sess")
	if got.Source != "updated" {
		t.Fatalf("expected Source=updated, got %s", got.Source)
	}
	if got.Title != "new title" {
		t.Fatalf("expected Title=new title, got %s", got.Title)
	}
}



func TestUpdateSession_NotFound(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "ghost-sess", Title: "no one"}
	err = store.UpdateSession(ctx, sess)
	if err != nil {
		t.Logf("UpdateSession on nonexistent returned: %v (acceptable)", err)
	}
}



func TestEndSession_Twice(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "end2-sess"})
	if err := store.EndSession(ctx, "end2-sess", "done"); err != nil {
		t.Fatal(err)
	}
	if err := store.EndSession(ctx, "end2-sess", "override"); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession(ctx, "end2-sess")
	if got.EndReason != "done" {
		t.Fatalf("first reason should win, got %s", got.EndReason)
	}
}



func TestEndSession_NoActive(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "noactive-sess"})
	store.EndSession(ctx, "noactive-sess", "first")
	store.EndSession(ctx, "noactive-sess", "second")
	got, _ := store.GetSession(ctx, "noactive-sess")
	if got.EndReason != "first" {
		t.Fatalf("expected first reason, got %s", got.EndReason)
	}
}



func TestListSessions_FilterSource(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "src-cli", Source: "cli"})
	store.CreateSession(ctx, &Session{ID: "src-api", Source: "api"})
	sessions, err := store.ListSessions(ctx, &SessionFilter{Source: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "src-cli" {
		t.Fatalf("expected src-cli, got %s", sessions[0].ID)
	}
}




func TestScanSession_NullFields(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "null-sess"}
	store.CreateSession(ctx, sess)
	got, err := store.GetSession(ctx, "null-sess")
	if err != nil {
		t.Fatal(err)
	}
	if got.ParentSessionID != "" {
		t.Fatalf("expected empty ParentSessionID, got %s", got.ParentSessionID)
	}
	if got.EndedAt != 0 {
		t.Fatalf("expected zero EndedAt, got %f", got.EndedAt)
	}
	if got.EndReason != "" {
		t.Fatalf("expected empty EndReason, got %s", got.EndReason)
	}
}




func TestListRecentSessions_Order(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "old-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "old-sess", Role: "user", Content: "first"})
	store.CreateSession(ctx, &Session{ID: "new-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "new-sess", Role: "user", Content: "second"})
	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
}




func TestListRecentSessions_WithActivity(t *testing.T) {
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

	// Create two sessions with messages
	store.CreateSession(ctx, &Session{ID: "recent-a", Source: "test"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "recent-a", Role: "user", Content: "first"})
	store.CreateSession(ctx, &Session{ID: "recent-b", Source: "test"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "recent-b", Role: "user", Content: "second"})

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	// Most recent message should be first
	if sessions[0].ID != "recent-b" {
		t.Errorf("expected recent-b first, got %s", sessions[0].ID)
	}
}



func TestUpdateSession_FullFields(t *testing.T) {
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

	store.CreateSession(ctx, &Session{ID: "update-full", Source: "cli"})

	updated := &Session{
		ID:              "update-full",
		Source:          "api",
		UserID:          "user-123",
		Model:          "gpt-4",
		Title:          "Updated Title",
		ParentSessionID: "parent-1",
		EndReason:      "compression",
	}
	if err := store.UpdateSession(ctx, updated); err != nil {
		t.Fatalf("UpdateSession failed: %v", err)
	}

	got, err := store.GetSession(ctx, "update-full")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "api" {
		t.Errorf("expected source=api, got %s", got.Source)
	}
	if got.UserID != "user-123" {
		t.Errorf("expected user_id=user-123, got %s", got.UserID)
	}
	if got.Model != "gpt-4" {
		t.Errorf("expected model=gpt-4, got %s", got.Model)
	}
	if got.Title != "Updated Title" {
		t.Errorf("expected title=Updated Title, got %s", got.Title)
	}
	if got.ParentSessionID != "parent-1" {
		t.Errorf("expected parent_session_id=parent-1, got %s", got.ParentSessionID)
	}
	if got.EndReason != "compression" {
		t.Errorf("expected end_reason=compression, got %s", got.EndReason)
	}
}






func TestListRecentSessions_ClosedStore2(t *testing.T) {
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
	_, err = store.ListRecentSessions(ctx, 10)
	if err == nil {
		t.Fatal("expected error after close")
	}
}



func TestListSessions_ClosedStore2(t *testing.T) {
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
	_, err = store.ListSessions(ctx, nil)
	if err == nil {
		t.Fatal("expected error after close")
	}
}



func TestCreateSession_ClosedStore2(t *testing.T) {
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
	err = store.CreateSession(ctx, &Session{ID: "s1", Source: "test"})
	if err == nil {
		t.Fatal("expected error after close")
	}
}



func TestEndSession_ClosedStore2(t *testing.T) {
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
	err = store.EndSession(ctx, "s1", "done")
	if err == nil {
		t.Fatal("expected error after close")
	}
}



func TestUpdateSession_ClosedStore2(t *testing.T) {
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
	err = store.UpdateSession(ctx, &Session{ID: "s1", Source: "test"})
	if err == nil {
		t.Fatal("expected error after close")
	}
}



func TestListSessions_FilterOnClosed(t *testing.T) {
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
	ended := true
	_, err = store.ListSessions(ctx, &SessionFilter{Source: "test", UserID: "u1", Ended: &ended, Limit: 10, Offset: 0})
	if err == nil {
		t.Fatal("expected error after close")
	}
}
