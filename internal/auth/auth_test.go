package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ─── Token.Valid tests ───

func TestToken_Valid_NilReceiver(t *testing.T) {
	t.Parallel()

	var tok *Token
	if tok.Valid() {
		t.Error("Valid() = true for nil Token, want false")
	}
}

func TestToken_Valid_EmptyAccessToken(t *testing.T) {
	t.Parallel()

	tok := &Token{AccessToken: ""}
	if tok.Valid() {
		t.Error("Valid() = true for empty AccessToken, want false")
	}
}

func TestToken_Valid_NotExpired(t *testing.T) {
	t.Parallel()

	tok := &Token{
		AccessToken: "valid-token",
		Expiry:      time.Now().Add(5 * time.Minute),
	}
	if !tok.Valid() {
		t.Error("Valid() = false for token expiring in 5 minutes, want true")
	}
}

func TestToken_Valid_Expired(t *testing.T) {
	t.Parallel()

	tok := &Token{
		AccessToken: "expired-token",
		Expiry:      time.Now().Add(-10 * time.Second),
	}
	if tok.Valid() {
		t.Error("Valid() = true for expired token, want false")
	}
}

func TestToken_Valid_ExpiresWithin60Seconds(t *testing.T) {
	t.Parallel()

	tok := &Token{
		AccessToken: "almost-expired",
		Expiry:      time.Now().Add(30 * time.Second),
	}
	if tok.Valid() {
		t.Error("Valid() = true for token expiring within 60s buffer, want false")
	}
}

func TestToken_Valid_ExactlyAt60SecondBoundary(t *testing.T) {
	t.Parallel()

	tok := &Token{
		AccessToken: "boundary",
		Expiry:      time.Now().Add(60 * time.Second),
	}
	// Within the 60s buffer window, should be false
	if tok.Valid() {
		t.Error("Valid() = true at exact 60s boundary, expected false due to buffer")
	}
}

// ─── NewGoogleOAuth tests ───

func TestNewGoogleOAuth(t *testing.T) {
	t.Parallel()

	g := NewGoogleOAuth("test-client-id")
	if g == nil {
		t.Fatal("NewGoogleOAuth() returned nil")
	}
	if g.clientID != "test-client-id" {
		t.Errorf("clientID = %q, want %q", g.clientID, "test-client-id")
	}
	if len(g.scopes) == 0 {
		t.Error("default scopes are empty")
	}
	if g.tokenFile == "" {
		t.Error("tokenFile is empty")
	}
}

// ─── WithScopes tests ───

func TestGoogleOAuth_WithScopes(t *testing.T) {
	t.Parallel()

	g := NewGoogleOAuth("id")
	result := g.WithScopes("scope1", "scope2")
	if result != g {
		t.Error("WithScopes() should return same instance for chaining")
	}
	if len(g.scopes) != 2 {
		t.Fatalf("len(scopes) = %d, want 2", len(g.scopes))
	}
	if g.scopes[0] != "scope1" || g.scopes[1] != "scope2" {
		t.Errorf("scopes = %v, want [scope1 scope2]", g.scopes)
	}
}

// ─── WithTokenFile tests ───

func TestGoogleOAuth_WithTokenFile(t *testing.T) {
	t.Parallel()

	g := NewGoogleOAuth("id")
	result := g.WithTokenFile("/custom/path/token.json")
	if result != g {
		t.Error("WithTokenFile() should return same instance for chaining")
	}
	if g.tokenFile != "/custom/path/token.json" {
		t.Errorf("tokenFile = %q, want %q", g.tokenFile, "/custom/path/token.json")
	}
}

// ─── PKCE helper tests ───

func TestGenerateCodeVerifier(t *testing.T) {
	t.Parallel()

	v1, err := generateCodeVerifier()
	if err != nil {
		t.Fatalf("generateCodeVerifier() error = %v", err)
	}
	if len(v1) == 0 {
		t.Error("code_verifier is empty")
	}

	v2, err := generateCodeVerifier()
	if err != nil {
		t.Fatalf("second generateCodeVerifier() error = %v", err)
	}
	if v1 == v2 {
		t.Error("two code_verifiers should differ")
	}
}

func TestGenerateCodeChallenge(t *testing.T) {
	t.Parallel()

	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := generateCodeChallenge(verifier)

	if challenge == "" {
		t.Error("code_challenge is empty")
	}
	if challenge == verifier {
		t.Error("code_challenge should not equal verifier")
	}

	// Deterministic: same verifier yields same challenge
	challenge2 := generateCodeChallenge(verifier)
	if challenge != challenge2 {
		t.Errorf("challenge not deterministic: %q != %q", challenge, challenge2)
	}
}

func TestGenerateCodeChallenge_KnownVector(t *testing.T) {
	t.Parallel()

	// RFC 7636 Appendix B test vector
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	expected := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	got := generateCodeChallenge(verifier)
	if got != expected {
		t.Errorf("S256 challenge = %q, want %q", got, expected)
	}
}

// ─── Token persistence tests ───

func TestGoogleOAuth_SaveAndLoadToken(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")

	g := NewGoogleOAuth("test-id").WithTokenFile(tokenPath)

	original := &Token{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		Expiry:       time.Now().Add(time.Hour).Truncate(time.Second),
		TokenType:    "Bearer",
	}

	if err := g.SaveToken(original); err != nil {
		t.Fatalf("SaveToken() error = %v", err)
	}

	loaded, err := g.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken() error = %v", err)
	}
	if loaded.AccessToken != original.AccessToken {
		t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, original.AccessToken)
	}
	if loaded.RefreshToken != original.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", loaded.RefreshToken, original.RefreshToken)
	}
	if loaded.TokenType != original.TokenType {
		t.Errorf("TokenType = %q, want %q", loaded.TokenType, original.TokenType)
	}
}

func TestGoogleOAuth_LoadToken_FileNotExist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "nonexistent", "token.json")

	g := NewGoogleOAuth("test-id").WithTokenFile(tokenPath)

	token, err := g.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken() error = %v", err)
	}
	if token != nil {
		t.Errorf("LoadToken() = %v, want nil for nonexistent file", token)
	}
}

func TestGoogleOAuth_SaveToken_Nil(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	g := NewGoogleOAuth("test-id").WithTokenFile(tokenPath)

	err := g.SaveToken(nil)
	if err == nil {
		t.Fatal("SaveToken(nil) expected error, got nil")
	}
}

func TestGoogleOAuth_SaveToken_CreatesDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "deep", "nested", "token.json")
	g := NewGoogleOAuth("test-id").WithTokenFile(tokenPath)

	token := &Token{
		AccessToken:  "test",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(time.Hour),
		TokenType:    "Bearer",
	}

	if err := g.SaveToken(token); err != nil {
		t.Fatalf("SaveToken() error = %v", err)
	}

	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		t.Error("token file was not created")
	}
}

func TestGoogleOAuth_LoadToken_InvalidJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")

	if err := os.WriteFile(tokenPath, []byte("not-valid-json"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	g := NewGoogleOAuth("test-id").WithTokenFile(tokenPath)
	_, err := g.LoadToken()
	if err == nil {
		t.Fatal("LoadToken() expected error for invalid JSON")
	}
}

func TestGoogleOAuth_SaveToken_AtomicWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	g := NewGoogleOAuth("test-id").WithTokenFile(tokenPath)

	token := &Token{
		AccessToken:  "atomic",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(time.Hour),
		TokenType:    "Bearer",
	}

	if err := g.SaveToken(token); err != nil {
		t.Fatalf("SaveToken() error = %v", err)
	}

	// Verify no leftover temp file
	tmpPath := tokenPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temporary file should be cleaned up after save")
	}

	// Verify file content is valid JSON
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var parsed Token
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.AccessToken != "atomic" {
		t.Errorf("AccessToken = %q, want %q", parsed.AccessToken, "atomic")
	}
}

// ─── RefreshToken error tests ───

func TestGoogleOAuth_RefreshToken_NilToken(t *testing.T) {
	t.Parallel()

	g := NewGoogleOAuth("test-id")
	_, err := g.RefreshToken(t.Context(), nil)
	if err == nil {
		t.Fatal("RefreshToken(nil) expected error")
	}
}

func TestGoogleOAuth_RefreshToken_EmptyRefreshToken(t *testing.T) {
	t.Parallel()

	g := NewGoogleOAuth("test-id")
	_, err := g.RefreshToken(t.Context(), &Token{AccessToken: "x"})
	if err == nil {
		t.Fatal("RefreshToken with empty refresh_token expected error")
	}
}

// ─── buildAuthURL tests ───

func TestGoogleOAuth_BuildAuthURL(t *testing.T) {
	t.Parallel()

	g := NewGoogleOAuth("my-client-id")
	g.redirectURI = "http://127.0.0.1:9999/oauth/callback"

	url := g.buildAuthURL("test-challenge", "test-state")

	if url == "" {
		t.Fatal("buildAuthURL() returned empty string")
	}
	if g.clientID != "my-client-id" {
		t.Errorf("URL does not embed client_id correctly")
	}
	// Verify key parameters are present
	tests := []struct {
		param string
	}{
		{"client_id=my-client-id"},
		{"code_challenge=test-challenge"},
		{"code_challenge_method=S256"},
		{"state=test-state"},
		{"response_type=code"},
		{"access_type=offline"},
		{"prompt=consent"},
	}
	for _, tc := range tests {
		if !containsSubstring(url, tc.param) {
			t.Errorf("URL missing param: %q", tc.param)
		}
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
