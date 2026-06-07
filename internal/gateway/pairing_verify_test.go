package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
