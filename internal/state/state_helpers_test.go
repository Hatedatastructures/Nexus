package state

import (
	"context"
	"path/filepath"
	"testing"
)

// newTestStore creates a test Store using a temp directory
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := RunMigrations(context.Background(), store.DB()); err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}
	return store
}

func boolPtr(b bool) *bool { return &b }
