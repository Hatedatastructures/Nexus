package testutil

import (
	"path/filepath"
	"testing"
)

func TestNewTempStore(t *testing.T) {
	store := NewTempStore(t)
	if store == nil {
		t.Fatal("NewTempStore returned nil")
	}
	// Verify the store is non-nil and cleanup is registered
	// (the actual database operations depend on migrations, so just verify creation)
}

func TestNewTempStoreWithPath(t *testing.T) {
	store, dbPath := NewTempStoreWithPath(t)
	if store == nil {
		t.Fatal("NewTempStoreWithPath returned nil store")
	}
	if dbPath == "" {
		t.Fatal("NewTempStoreWithPath returned empty path")
	}
	// dbPath should end with test.db
	if filepath.Base(dbPath) != "test.db" {
		t.Errorf("dbPath = %q, expected base name test.db", dbPath)
	}
}
