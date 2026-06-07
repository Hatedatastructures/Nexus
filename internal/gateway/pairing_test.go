package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// NewPairingStore
// ---------------------------------------------------------------------------

func TestNewPairingStore(t *testing.T) {
	t.Parallel()

	t.Run("with custom baseDir", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		if store == nil {
			t.Fatal("expected non-nil store")
		}
		if store.baseDir != dir {
			t.Errorf("baseDir = %q, want %q", store.baseDir, dir)
		}
	})

	t.Run("empty baseDir uses default", func(t *testing.T) {
		t.Parallel()
		store, err := NewPairingStore("")
		if err != nil {
			t.Fatal(err)
		}
		home, _ := os.UserHomeDir()
		expected := filepath.Join(home, ".nexus", "pairing")
		if store.baseDir != expected {
			t.Errorf("baseDir = %q, want %q", store.baseDir, expected)
		}
	})

	t.Run("creates directory if not exists", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "sub", "pairing")
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Error("expected directory to be created")
		}
		_ = store
	})

	t.Run("loads existing records", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		records := `[{"code":"LEGACY1","hash":"","salt":"","platform":"test","user_id":"u1","created_at":"2025-01-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","verified":false}]`
		if err := os.WriteFile(filepath.Join(dir, "test.json"), []byte(records), 0600); err != nil {
			t.Fatal(err)
		}
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		recs := store.getRecords("test", "u1")
		if len(recs) != 1 {
			t.Fatalf("expected 1 record, got %d", len(recs))
		}
		if recs[0].Code != "LEGACY1" {
			t.Errorf("code = %q, want %q", recs[0].Code, "LEGACY1")
		}
	})
}

// ---------------------------------------------------------------------------
// PendingCodeDisplay
// ---------------------------------------------------------------------------

func TestPendingCodeDisplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rec  *PairingRecord
		want string
	}{
		{
			"new record with hash",
			&PairingRecord{Hash: "abcdef1234567890abcdef1234567890", Salt: "somesalt"},
			"abcdef12",
		},
		{
			"legacy record no hash",
			&PairingRecord{Code: "PLAINTEX", Hash: "", Salt: ""},
			"legacy",
		},
		{
			"short hash",
			&PairingRecord{Hash: "abc", Salt: "salt"},
			"abc",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := PendingCodeDisplay(tc.rec)
			if got != tc.want {
				t.Errorf("PendingCodeDisplay() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PurgeExpired
// ---------------------------------------------------------------------------

func TestPairingStore_PurgeExpired(t *testing.T) {
	t.Parallel()

	t.Run("purges expired records", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.GenerateCode("purge_plat", "purge_user")
		if err != nil {
			t.Fatal(err)
		}
		store.mu.Lock()
		for _, r := range store.records["purge_plat"] {
			r.ExpiresAt = time.Now().Add(-1 * time.Hour)
		}
		store.mu.Unlock()

		purged := store.PurgeExpired()
		if purged != 1 {
			t.Errorf("expected 1 purged, got %d", purged)
		}

		recs := store.getRecords("purge_plat", "purge_user")
		if len(recs) != 0 {
			t.Errorf("expected 0 records after purge, got %d", len(recs))
		}
	})

	t.Run("keeps verified records", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		code, err := store.GenerateCode("purge2_plat", "purge2_user")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = store.VerifyCode("purge2_plat", "purge2_user", code)

		store.mu.Lock()
		for _, r := range store.records["purge2_plat"] {
			r.ExpiresAt = time.Now().Add(-1 * time.Hour)
		}
		store.mu.Unlock()

		purged := store.PurgeExpired()
		if purged != 0 {
			t.Errorf("expected 0 purged (verified kept), got %d", purged)
		}
	})

	t.Run("no expired records returns 0", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		purged := store.PurgeExpired()
		if purged != 0 {
			t.Errorf("expected 0 purged, got %d", purged)
		}
	})
}
