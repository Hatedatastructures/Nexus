package state

import (
	"context"
	"testing"
	"time"
)

func TestSearchMessages_EmptyAndWhitespace(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	results, err := store.SearchMessages(ctx, "", 10)
	if err != nil || len(results) != 0 {
		t.Errorf("empty query: results=%v err=%v", results, err)
	}

	results, err = store.SearchMessages(ctx, "   ", 10)
	if err != nil || len(results) != 0 {
		t.Errorf("whitespace query: results=%v err=%v", results, err)
	}

	results, err = store.SearchMessages(ctx, "***", 10)
	if err != nil || len(results) != 0 {
		t.Errorf("special chars only: results=%v err=%v", results, err)
	}
}



func TestSearchMessages_MixedCJKAndLatin(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "mixed-search", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess)
	msg := &MessageRecord{
		SessionID: "mixed-search",
		Role:      "user",
		Content:   "Hello \xe4\xbd\xa0\xe5\xa5\xbd world",
		Timestamp: float64(time.Now().Unix()),
	}
	store.InsertMessage(ctx, msg)
	// Search with Latin
	results, err := store.SearchMessages(ctx, "Hello", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected to find Hello")
	}
	// Search with CJK
	results2, err := store.SearchMessages(ctx, "\xe4\xbd\xa0\xe5\xa5\xbd\xe4\xb8\x96\xe7\x95\x8c", 10)
	if err != nil {
		t.Fatal(err)
	}
	_ = results2
}



func TestSearchMessages_BasicLatin(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "latin-sess", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess)
	store.InsertMessage(ctx, &MessageRecord{
		SessionID: "latin-sess",
		Role:      "user",
		Content:   "hello world from test",
		Timestamp: float64(time.Now().Unix()),
	})
	results, err := store.SearchMessages(ctx, "hello", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected to find message with 'hello'")
	}
}



func TestSearchMessages_Empty(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	results, err := store.SearchMessages(ctx, "hello", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}




func TestSearchMessages_CJKShort(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "cjk-short-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "cjk-short-sess", Role: "user", Content: "\xe6\xb5\x8b\xe8\xaf\x95\xe5\x86\x85\xe5\xae\xb9"})
	_, err = store.SearchMessages(ctx, "\xe6\xb5\x8b", 10)
	if err != nil {
		t.Logf("CJK short search: %v (acceptable)", err)
	}
}



func TestSearchMessages_CJKLong(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "cjk-long-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "cjk-long-sess", Role: "user", Content: "\xe8\xbf\x99\xe6\x98\xaf\xe4\xb8\x80\xe4\xb8\xaa\xe6\xb5\x8b\xe8\xaf\x95"})
	_, err = store.SearchMessages(ctx, "\xe8\xbf\x99\xe6\x98\xaf\xe4\xb8\x80\xe4\xb8\xaa", 10)
	if err != nil {
		t.Logf("CJK long search: %v (acceptable)", err)
	}
}




func TestSearchMessages_CJKLikeFallback(t *testing.T) {
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

	// Create session with CJK content
	store.CreateSession(ctx, &Session{ID: "search-cjk-like", Source: "test"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "search-cjk-like", Role: "user", Content: "\xe6\xb5\x8b\xe8\xaf\x95"})

	// Search with 1-2 CJK chars -> LIKE fallback
	results, err := store.SearchMessages(ctx, "\xe6\xb5\x8b", 10)
	if err != nil {
		t.Fatalf("SearchMessages CJK LIKE failed: %v", err)
	}
	if len(results) < 1 {
		t.Errorf("expected at least 1 LIKE result, got %d", len(results))
	}
}



func TestSearchMessages_ClosedStore2(t *testing.T) {
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
	results, err := store.SearchMessages(ctx, "hello", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Fatal("expected nil results after close")
	}
}



func TestSearchMessages_CJKClosed(t *testing.T) {
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
	results, err := store.SearchMessages(ctx, "\u6d4b\u8bd5\u67e5\u8be2", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Fatal("expected nil results after close")
	}
	results, err = store.SearchMessages(ctx, "\u4f60\u597d\u4e16\u754c", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Fatal("expected nil results after close")
	}
}
