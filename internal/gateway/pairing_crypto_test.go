package gateway

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

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
