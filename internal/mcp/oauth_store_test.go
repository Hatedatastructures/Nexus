package mcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewTokenStore_WithExplicitPath(t *testing.T) {
	t.Parallel()

	store, err := NewTokenStore("/tmp/test-tokens.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if store.tokensPath != "/tmp/test-tokens.json" {
		t.Errorf("expected tokensPath /tmp/test-tokens.json, got %s", store.tokensPath)
	}
}

func TestNewTokenStore_EmptyPath_UsesDefault(t *testing.T) {
	t.Parallel()

	store, err := NewTokenStore("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.tokensPath == "" {
		t.Error("expected non-empty default tokens path")
	}
}

func TestTokenStore_SaveAndLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")

	store, err := NewTokenStore(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	token := &OAuthToken{
		AccessToken:  "access-abc",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		RefreshToken: "refresh-def",
		Scope:        "read",
		ExpiresAt:    time.Now().Unix() + 3600,
	}

	if err := store.SaveToken(token); err != nil {
		t.Fatalf("SaveToken failed: %v", err)
	}

	if !store.Exists() {
		t.Error("token file should exist after save")
	}

	loaded, err := store.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken failed: %v", err)
	}

	if loaded.AccessToken != "access-abc" {
		t.Errorf("expected access_token access-abc, got %s", loaded.AccessToken)
	}
	if loaded.RefreshToken != "refresh-def" {
		t.Errorf("expected refresh_token refresh-def, got %s", loaded.RefreshToken)
	}
}

func TestTokenStore_SaveToken_Nil(t *testing.T) {
	t.Parallel()

	store, _ := NewTokenStore("/tmp/nil-test.json")
	err := store.SaveToken(nil)
	if err == nil {
		t.Error("expected error for nil token")
	}
}

func TestTokenStore_LoadToken_NotExist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	store, _ := NewTokenStore(path)
	_, err := store.LoadToken()
	if err == nil {
		t.Error("expected error when loading nonexistent token")
	}
}

func TestTokenStore_DeleteToken_Idempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "delete-test.json")

	store, _ := NewTokenStore(path)

	// Delete on nonexistent file should not error
	if err := store.DeleteToken(); err != nil {
		t.Fatalf("DeleteToken on nonexistent should not error: %v", err)
	}

	// Save then delete
	_ = store.SaveToken(&OAuthToken{AccessToken: "at", ExpiresAt: time.Now().Unix() + 100})
	if err := store.DeleteToken(); err != nil {
		t.Fatalf("DeleteToken failed: %v", err)
	}
	if store.Exists() {
		t.Error("token file should be deleted")
	}

	// Delete again should not error
	if err := store.DeleteToken(); err != nil {
		t.Fatalf("second DeleteToken should not error: %v", err)
	}
}

func TestTokenStore_Exists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "exist-test.json")

	store, _ := NewTokenStore(path)

	if store.Exists() {
		t.Error("file should not exist initially")
	}

	_ = store.SaveToken(&OAuthToken{AccessToken: "at", ExpiresAt: 9999999999})

	if !store.Exists() {
		t.Error("file should exist after save")
	}
}

func TestSafeFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"myserver", "myserver"},
		{"my-server", "my-server"},
		{"my_server", "my_server"},
		{"my/server", "my_server"},
		{"my\\server", "my_server"},
		{"my:server", "my_server"},
		{"", "default"},
		{"___", "default"},
		{"___test___", "test"},
	}

	for _, tt := range tests {
		got := safeFilename(tt.input)
		if got != tt.expected {
			t.Errorf("safeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
		}
		if len(got) > 128 {
			t.Errorf("safeFilename(%q) result too long: %d", tt.input, len(got))
		}
	}
}

func TestSafeFilename_LongInput(t *testing.T) {
	t.Parallel()

	longName := ""
	for i := 0; i < 200; i++ {
		longName += "a"
	}
	result := safeFilename(longName)
	if len(result) > 128 {
		t.Errorf("safeFilename should truncate to 128 chars, got %d", len(result))
	}
}

func TestServerTokenPath(t *testing.T) {
	t.Parallel()

	path, err := ServerTokenPath("my-mcp-server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path")
	}
	// Should end with .json
	if filepath.Ext(path) != ".json" {
		t.Errorf("expected .json extension, got %s", filepath.Ext(path))
	}
}

func TestDefaultTokenDir(t *testing.T) {
	t.Parallel()

	dir, err := DefaultTokenDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir == "" {
		t.Error("expected non-empty directory")
	}
}

func TestDefaultTokenPath_WithNexusHome(t *testing.T) {
	t.Parallel()

	orig := os.Getenv("NEXUS_HOME")
	_ = os.Setenv("NEXUS_HOME", "/custom/nexus/home")
	defer func() { _ = os.Setenv("NEXUS_HOME", orig) }()

	path, err := defaultTokenPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Use filepath.ToSlash for cross-platform comparison
	normalized := filepath.ToSlash(path)
	if normalized != "custom/nexus/home/mcp-tokens/default.json" &&
		normalized != "/custom/nexus/home/mcp-tokens/default.json" {
		t.Errorf("unexpected path: %s (normalized: %s)", path, normalized)
	}
}

func TestTokenStore_SaveAndLoad_EmptyAccessToken(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty-at.json")

	store, _ := NewTokenStore(path)

	token := &OAuthToken{
		AccessToken: "",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Unix() + 3600,
	}

	if err := store.SaveToken(token); err != nil {
		t.Fatalf("SaveToken failed: %v", err)
	}

	_, err := store.LoadToken()
	if err == nil {
		t.Error("expected error for empty access_token on load")
	}
}

func TestTokenStore_SaveAndLoad_InvalidJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.json")

	// Write invalid JSON directly
	_ = os.WriteFile(path, []byte("not valid json"), 0644)

	store, _ := NewTokenStore(path)
	_, err := store.LoadToken()
	if err == nil {
		t.Error("expected error for invalid JSON file")
	}
}
