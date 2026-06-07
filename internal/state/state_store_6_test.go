package state

import (
	"context"
	"testing"
	"time"
)

func TestCheckpointWAL_ClosedStore2(t *testing.T) {
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
	err = store.CheckpointWAL(ctx)
	if err == nil {
		t.Fatal("expected error after close")
	}
}



func TestGetCompressionTip_ReturnsSelf(t *testing.T) {
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
	ts := float64(time.Now().Unix())
	if err := store.CreateSession(ctx, &Session{ID: "no-chain", Source: "test", StartedAt: ts}); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetCompressionTip(ctx, "no-chain")
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil {
		t.Fatal("expected non-nil session")
	}
	if sess.ID != "no-chain" {
		t.Fatalf("expected ID=no-chain, got %s", sess.ID)
	}
}
