package gateway

import (
	"encoding/hex"
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
// GenerateCode
// ---------------------------------------------------------------------------

func TestPairingStore_GenerateCode(t *testing.T) {
	t.Parallel()

	t.Run("generates valid code", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		code, err := store.GenerateCode("platform1", "user1")
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != pairingCodeLength {
			t.Errorf("code length = %d, want %d", len(code), pairingCodeLength)
		}
		for _, c := range code {
			if !isUnambiguous(c) {
				t.Errorf("code contains non-unambiguous char: %c", c)
			}
		}
	})

	t.Run("persists to disk", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		code, err := store.GenerateCode("disktest", "user1")
		if err != nil {
			t.Fatal(err)
		}
		_ = code

		path := filepath.Join(dir, "disktest.json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Fatal("expected file to be created")
		}

		store2, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		recs := store2.getRecords("disktest", "user1")
		if len(recs) != 1 {
			t.Fatalf("expected 1 record after reload, got %d", len(recs))
		}
	})

	t.Run("rate limited", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.GenerateCode("plat", "user1")
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.GenerateCode("plat", "user1")
		if err == nil {
			t.Error("expected rate limit error")
		}
	})

	t.Run("max pending codes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}

		for i := 0; i < maxPendingPerUser; i++ {
			store.mu.Lock()
			for _, r := range store.records["plat2"] {
				r.LastGeneratedAt = time.Now().Add(-rateLimitInterval - time.Second)
			}
			store.mu.Unlock()
			_, err := store.GenerateCode("plat2", "user_max")
			if err != nil {
				t.Fatalf("generate %d failed: %v", i, err)
			}
		}

		store.mu.Lock()
		for _, r := range store.records["plat2"] {
			r.LastGeneratedAt = time.Now().Add(-rateLimitInterval - time.Second)
		}
		store.mu.Unlock()

		_, err = store.GenerateCode("plat2", "user_max")
		if err == nil {
			t.Error("expected max pending error")
		}
	})
}

// ---------------------------------------------------------------------------
// VerifyCode
// ---------------------------------------------------------------------------

func TestPairingStore_VerifyCode(t *testing.T) {
	t.Parallel()

	t.Run("successful verification", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		code, err := store.GenerateCode("vplat", "vuser")
		if err != nil {
			t.Fatal(err)
		}
		ok, err := store.VerifyCode("vplat", "vuser", code)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Error("expected successful verification")
		}
	})

	t.Run("wrong code fails", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.GenerateCode("vplat2", "vuser2")
		if err != nil {
			t.Fatal(err)
		}
		ok, err := store.VerifyCode("vplat2", "vuser2", "WRONGCODE")
		if err == nil {
			t.Error("expected error for wrong code")
		}
		if ok {
			t.Error("expected false for wrong code")
		}
	})

	t.Run("already verified code rejected", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		code, err := store.GenerateCode("vplat3", "vuser3")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = store.VerifyCode("vplat3", "vuser3", code)
		ok, err := store.VerifyCode("vplat3", "vuser3", code)
		if err == nil {
			t.Error("expected error for already verified code")
		}
		if ok {
			t.Error("expected false for already verified code")
		}
	})

	t.Run("expired code rejected", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		code, err := store.GenerateCode("vplat4", "vuser4")
		if err != nil {
			t.Fatal(err)
		}
		store.mu.Lock()
		recs := store.records["vplat4"]
		for _, r := range recs {
			if r.UserID == "vuser4" {
				r.ExpiresAt = time.Now().Add(-1 * time.Hour)
			}
		}
		store.mu.Unlock()

		ok, err := store.VerifyCode("vplat4", "vuser4", code)
		if err == nil {
			t.Error("expected error for expired code")
		}
		if ok {
			t.Error("expected false for expired code")
		}
	})

	t.Run("legacy code verification", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		store.mu.Lock()
		store.records["legacy_plat"] = append(store.records["legacy_plat"], &PairingRecord{
			Code:      "LEGACY12",
			Hash:      "",
			Salt:      "",
			Platform:  "legacy_plat",
			UserID:    "legacy_user",
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(pairingTTL),
		})
		store.mu.Unlock()

		ok, err := store.VerifyCode("legacy_plat", "legacy_user", "LEGACY12")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Error("expected legacy verification to succeed")
		}
	})

	t.Run("lockout after too many attempts", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.GenerateCode("lock_plat", "lock_user")
		if err != nil {
			t.Fatal(err)
		}

		for i := 0; i <= lockoutThreshold; i++ {
			_, _ = store.VerifyCode("lock_plat", "lock_user", "BADCODE")
		}

		store.mu.RLock()
		recs := store.getRecords("lock_plat", "lock_user")
		hasLockout := false
		for _, r := range recs {
			if !r.LockedUntil.IsZero() {
				hasLockout = true
			}
		}
		store.mu.RUnlock()

		if !hasLockout {
			t.Error("expected lockout to be set after exceeding threshold")
		}
	})

	t.Run("locked code rejected", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		code, err := store.GenerateCode("lock2_plat", "lock2_user")
		if err != nil {
			t.Fatal(err)
		}
		store.mu.Lock()
		recs := store.records["lock2_plat"]
		for _, r := range recs {
			if r.UserID == "lock2_user" {
				r.LockedUntil = time.Now().Add(10 * time.Minute)
			}
		}
		store.mu.Unlock()

		ok, err := store.VerifyCode("lock2_plat", "lock2_user", code)
		if err == nil {
			t.Error("expected error for locked code")
		}
		if ok {
			t.Error("expected false for locked code")
		}
	})
}

// ---------------------------------------------------------------------------
// GetPendingCodes
// ---------------------------------------------------------------------------

func TestPairingStore_GetPendingCodes(t *testing.T) {
	t.Parallel()

	t.Run("returns only pending codes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		code, err := store.GenerateCode("pend_plat", "pend_user")
		if err != nil {
			t.Fatal(err)
		}
		_ = code

		pending := store.GetPendingCodes("pend_plat", "pend_user")
		if len(pending) != 1 {
			t.Fatalf("expected 1 pending, got %d", len(pending))
		}
	})

	t.Run("excludes verified codes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		code, err := store.GenerateCode("pend2_plat", "pend2_user")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = store.VerifyCode("pend2_plat", "pend2_user", code)

		pending := store.GetPendingCodes("pend2_plat", "pend2_user")
		if len(pending) != 0 {
			t.Errorf("expected 0 pending, got %d", len(pending))
		}
	})

	t.Run("excludes expired codes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.GenerateCode("pend3_plat", "pend3_user")
		if err != nil {
			t.Fatal(err)
		}
		store.mu.Lock()
		for _, r := range store.records["pend3_plat"] {
			r.ExpiresAt = time.Now().Add(-1 * time.Hour)
		}
		store.mu.Unlock()

		pending := store.GetPendingCodes("pend3_plat", "pend3_user")
		if len(pending) != 0 {
			t.Errorf("expected 0 pending for expired, got %d", len(pending))
		}
	})

	t.Run("empty for unknown user", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := NewPairingStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		pending := store.GetPendingCodes("none_plat", "none_user")
		if len(pending) != 0 {
			t.Errorf("expected 0 pending, got %d", len(pending))
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

// ---------------------------------------------------------------------------
// generateRandomCode
// ---------------------------------------------------------------------------

func TestGenerateRandomCode(t *testing.T) {
	t.Parallel()

	t.Run("generates correct length", func(t *testing.T) {
		t.Parallel()
		code, err := generateRandomCode(8)
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 8 {
			t.Errorf("length = %d, want 8", len(code))
		}
	})

	t.Run("uses unambiguous alphabet", func(t *testing.T) {
		t.Parallel()
		code, err := generateRandomCode(100)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range code {
			if !isUnambiguous(c) {
				t.Errorf("char %c not in unambiguous alphabet", c)
			}
		}
	})

	t.Run("different codes on successive calls", func(t *testing.T) {
		t.Parallel()
		code1, err := generateRandomCode(8)
		if err != nil {
			t.Fatal(err)
		}
		code2, err := generateRandomCode(8)
		if err != nil {
			t.Fatal(err)
		}
		if code1 == code2 {
			t.Error("expected different codes (extremely unlikely collision)")
		}
	})

	t.Run("zero length", func(t *testing.T) {
		t.Parallel()
		code, err := generateRandomCode(0)
		if err != nil {
			t.Fatal(err)
		}
		if code != "" {
			t.Errorf("expected empty string, got %q", code)
		}
	})
}

// ---------------------------------------------------------------------------
// pairingHashCode
// ---------------------------------------------------------------------------

func TestPairingHashCode(t *testing.T) {
	t.Parallel()

	t.Run("deterministic", func(t *testing.T) {
		t.Parallel()
		salt := []byte("testsalt12345678")
		h1 := pairingHashCode("TESTCODE", salt)
		h2 := pairingHashCode("TESTCODE", salt)
		if h1 != h2 {
			t.Error("expected same hash for same inputs")
		}
	})

	t.Run("different codes produce different hashes", func(t *testing.T) {
		t.Parallel()
		salt := []byte("testsalt12345678")
		h1 := pairingHashCode("CODEAAAA", salt)
		h2 := pairingHashCode("CODEBBBB", salt)
		if h1 == h2 {
			t.Error("expected different hashes")
		}
	})

	t.Run("different salts produce different hashes", func(t *testing.T) {
		t.Parallel()
		h1 := pairingHashCode("SAMECODE", []byte("salt1"))
		h2 := pairingHashCode("SAMECODE", []byte("salt2"))
		if h1 == h2 {
			t.Error("expected different hashes for different salts")
		}
	})

	t.Run("hex encoded output", func(t *testing.T) {
		t.Parallel()
		salt := []byte("testsalt")
		h := pairingHashCode("CODE", salt)
		_, err := hex.DecodeString(h)
		if err != nil {
			t.Errorf("hash is not valid hex: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// atomicWriteFile
// ---------------------------------------------------------------------------

func TestAtomicWriteFile(t *testing.T) {
	t.Parallel()

	t.Run("writes and renames", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.json")
		data := []byte(`{"key":"value"}`)

		if err := atomicWriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(data) {
			t.Errorf("got %q, want %q", got, data)
		}

		if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
			t.Error("temp file should not exist after atomic write")
		}
	})

	t.Run("overwrites existing", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "overwrite.json")

		if err := atomicWriteFile(path, []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := atomicWriteFile(path, []byte("new"), 0600); err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "new" {
			t.Errorf("got %q, want %q", got, "new")
		}
	})
}

// ---------------------------------------------------------------------------
// isUnambiguous helper
// ---------------------------------------------------------------------------

func isUnambiguous(c rune) bool {
	for _, ch := range unambiguousAlphabet {
		if c == ch {
			return true
		}
	}
	return false
}
