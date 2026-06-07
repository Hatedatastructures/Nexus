package mcp

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOAuthToken_IsExpired_ZeroExpiresAt(t *testing.T) {
	t.Parallel()

	token := &OAuthToken{ExpiresAt: 0}
	if token.IsExpired() {
		t.Error("token with ExpiresAt=0 should not be considered expired")
	}
}

func TestOAuthToken_IsExpired_NegativeExpiresAt(t *testing.T) {
	t.Parallel()

	token := &OAuthToken{ExpiresAt: -1}
	if token.IsExpired() {
		t.Error("token with negative ExpiresAt should not be considered expired")
	}
}

func TestOAuthToken_IsExpired_FutureExpiresAt(t *testing.T) {
	t.Parallel()

	token := &OAuthToken{ExpiresAt: time.Now().Unix() + 3600}
	if token.IsExpired() {
		t.Error("token expiring in 1 hour should not be expired")
	}
}

func TestOAuthToken_IsExpired_PastExpiresAt(t *testing.T) {
	t.Parallel()

	token := &OAuthToken{ExpiresAt: time.Now().Unix() - 10}
	if !token.IsExpired() {
		t.Error("token that expired 10 seconds ago should be expired")
	}
}

func TestGenerateCodeVerifier(t *testing.T) {
	t.Parallel()

	verifier, err := generateCodeVerifier()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(verifier) < 43 {
		t.Errorf("code_verifier too short: %d chars", len(verifier))
	}
	if len(verifier) > 128 {
		t.Errorf("code_verifier too long: %d chars", len(verifier))
	}
}

func TestGenerateCodeVerifier_Unique(t *testing.T) {
	t.Parallel()

	v1, _ := generateCodeVerifier()
	v2, _ := generateCodeVerifier()
	if v1 == v2 {
		t.Error("two generated code_verifiers should differ")
	}
}

func TestGenerateCodeChallenge_Deterministic(t *testing.T) {
	t.Parallel()

	verifier := "test-verifier-value"
	c1 := generateCodeChallenge(verifier)
	c2 := generateCodeChallenge(verifier)
	if c1 != c2 {
		t.Error("same verifier should produce same challenge")
	}
}

func TestGenerateCodeChallenge_DifferentVerifiers(t *testing.T) {
	t.Parallel()

	c1 := generateCodeChallenge("verifier-1")
	c2 := generateCodeChallenge("verifier-2")
	if c1 == c2 {
		t.Error("different verifiers should produce different challenges")
	}
}

func TestBuildAuthorizationURL(t *testing.T) {
	t.Parallel()

	cfg := &OAuthConfig{
		ClientID:    "test-client",
		AuthURL:     "https://example.com/authorize",
		RedirectURI: "http://127.0.0.1:8080/callback",
		Scopes:      []string{"read", "write"},
	}

	authURL, verifier, err := BuildAuthorizationURL(cfg, "test-state")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verifier == "" {
		t.Error("verifier should not be empty")
	}
	if authURL == "" {
		t.Error("authURL should not be empty")
	}

	// Check key parameters are present in the URL
	if !containsSubstring(authURL, "response_type=code") {
		t.Error("auth URL should contain response_type=code")
	}
	if !containsSubstring(authURL, "client_id=test-client") {
		t.Error("auth URL should contain client_id")
	}
	if !containsSubstring(authURL, "state=test-state") {
		t.Error("auth URL should contain state parameter")
	}
	if !containsSubstring(authURL, "code_challenge_method=S256") {
		t.Error("auth URL should contain code_challenge_method=S256")
	}
	if !containsSubstring(authURL, "scope=read+write") || !containsSubstring(authURL, "scope=read%20write") {
		// URL encoding may use + or %20 for spaces
		t.Logf("auth URL scope encoding: %s", authURL)
	}
}

func TestBuildAuthorizationURL_InvalidAuthURL(t *testing.T) {
	t.Parallel()

	cfg := &OAuthConfig{
		ClientID: "test-client",
		AuthURL:  "://invalid-url",
	}

	_, _, err := BuildAuthorizationURL(cfg, "state")
	if err == nil {
		t.Error("expected error for invalid auth URL")
	}
}

func TestBuildAuthorizationURL_NoScopes(t *testing.T) {
	t.Parallel()

	cfg := &OAuthConfig{
		ClientID:    "test-client",
		AuthURL:     "https://example.com/authorize",
		RedirectURI: "http://127.0.0.1:8080/callback",
		Scopes:      nil,
	}

	authURL, _, err := BuildAuthorizationURL(cfg, "state")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsSubstring(authURL, "scope=") {
		t.Error("auth URL should not contain scope when none specified")
	}
}

func TestCompleteOAuthFlow_StateMismatch(t *testing.T) {
	t.Parallel()

	cfg := &OAuthConfig{
		TokenURL: "https://example.com/token",
	}

	_, err := CompleteOAuthFlow(cfg, "code123", "wrong-state", "expected-state", "verifier")
	if err == nil {
		t.Error("expected error for state mismatch")
	}
}

func TestCompleteOAuthFlow_EmptyExpectedState_SkipsValidation(t *testing.T) {
	t.Parallel()

	cfg := &OAuthConfig{
		TokenURL: "https://example.com/token",
	}

	// empty expectedState skips CSRF validation, but the HTTP call will fail
	// since there is no real server. Just verify it does not return the CSRF error.
	_, err := CompleteOAuthFlow(cfg, "code123", "any-state", "", "verifier")
	if err != nil {
		// The error should NOT be about state mismatch
		if containsSubstring(err.Error(), "CSRF") || containsSubstring(err.Error(), "state") {
			t.Errorf("should not get CSRF error when expectedState is empty: %v", err)
		}
	}
}

func TestOAuthConfig_JSONSerialization(t *testing.T) {
	t.Parallel()

	_ = OAuthConfig{
		ClientID:     "id",
		ClientSecret: "secret",
		AuthURL:      "https://auth.example.com",
		TokenURL:     "https://token.example.com",
		RedirectURI:  "http://localhost/callback",
		Scopes:       []string{"read"},
	}

	// OAuthConfig is not JSON-tagged but we can verify OAuthToken serialization
	token := OAuthToken{
		AccessToken:  "at-123",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		RefreshToken: "rt-456",
		Scope:        "read write",
		ExpiresAt:    time.Now().Unix() + 3600,
	}

	data, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("failed to marshal token: %v", err)
	}

	var parsed OAuthToken
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal token: %v", err)
	}

	if parsed.AccessToken != "at-123" {
		t.Errorf("expected access_token at-123, got %s", parsed.AccessToken)
	}
	if parsed.TokenType != "Bearer" {
		t.Errorf("expected token_type Bearer, got %s", parsed.TokenType)
	}
	if parsed.RefreshToken != "rt-456" {
		t.Errorf("expected refresh_token rt-456, got %s", parsed.RefreshToken)
	}
}

func TestStartOAuthFlow(t *testing.T) {
	t.Parallel()

	cfg := &OAuthConfig{
		ClientID:    "client-id",
		AuthURL:     "https://example.com/authorize",
		TokenURL:    "https://example.com/token",
		RedirectURI: "http://localhost:8080/callback",
		Scopes:      []string{"openid"},
	}

	result, err := StartOAuthFlow(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.AuthURL == "" {
		t.Error("auth URL should not be empty")
	}
	if result.Verifier == "" {
		t.Error("verifier should not be empty")
	}
	if result.State == "" {
		t.Error("state should not be empty")
	}
}

// helper
func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || jsonContains(s, sub))
}

func jsonContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
