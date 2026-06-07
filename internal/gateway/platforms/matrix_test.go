package platforms

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Matrix adapter -- constructor
// ---------------------------------------------------------------------------

func TestNewMatrixAdapter(t *testing.T) {
	t.Parallel()

	a := NewMatrixAdapter("https://matrix.org", "token123", "@user:matrix.org")
	if a == nil {
		t.Fatal("NewMatrixAdapter() returned nil")
	}
	if a.homeServer != "https://matrix.org" {
		t.Errorf("homeServer = %q, want %q", a.homeServer, "https://matrix.org")
	}
	if a.accessToken != "token123" {
		t.Errorf("accessToken = %q, want %q", a.accessToken, "token123")
	}
	if a.userID != "@user:matrix.org" {
		t.Errorf("userID = %q, want %q", a.userID, "@user:matrix.org")
	}
}

// ---------------------------------------------------------------------------
// Matrix adapter -- interface compliance
// ---------------------------------------------------------------------------

func TestMatrixAdapterImplementsPlatformAdapter(t *testing.T) {
	t.Parallel()

	var _ PlatformAdapter = (*MatrixAdapter)(nil)
}

// ---------------------------------------------------------------------------
// Matrix adapter -- property accessors
// ---------------------------------------------------------------------------

func TestMatrixAdapterProperties(t *testing.T) {
	t.Parallel()

	a := NewMatrixAdapter("https://matrix.org", "token", "@user:matrix.org")
	if a.Name() != "Matrix" {
		t.Errorf("Name() = %q, want %q", a.Name(), "Matrix")
	}
	if a.PlatformType() != PlatformMatrix {
		t.Errorf("PlatformType() = %q, want %q", a.PlatformType(), PlatformMatrix)
	}
	if a.MaxMessageLength() != 4096 {
		t.Errorf("MaxMessageLength() = %d, want 4096", a.MaxMessageLength())
	}
	if a.SupportsStreaming() {
		t.Error("SupportsStreaming() = true, want false")
	}
}

// ---------------------------------------------------------------------------
// Matrix adapter -- homeServer trailing slash trimming
// ---------------------------------------------------------------------------

func TestMatrixAdapterTrailingSlashTrimmed(t *testing.T) {
	t.Parallel()

	a := NewMatrixAdapter("https://matrix.org/", "token", "@user:matrix.org")
	if strings.HasSuffix(a.homeServer, "/") {
		t.Errorf("homeServer = %q, trailing slash should be trimmed", a.homeServer)
	}
}

// ---------------------------------------------------------------------------
// Matrix adapter -- Configure
// ---------------------------------------------------------------------------

func TestMatrixConfigureSuccess(t *testing.T) {
	t.Parallel()

	a := &MatrixAdapter{}
	err := a.Configure(map[string]any{
		"home_server":  "https://example.com",
		"access_token": "token",
		"user_id":      "@bot:example.com",
	})
	if err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if a.homeServer != "https://example.com" {
		t.Errorf("homeServer = %q, want %q", a.homeServer, "https://example.com")
	}
	if a.accessToken != "token" {
		t.Errorf("accessToken = %q, want %q", a.accessToken, "token")
	}
	if a.userID != "@bot:example.com" {
		t.Errorf("userID = %q, want %q", a.userID, "@bot:example.com")
	}
}

func TestMatrixConfigureMissingHomeServer(t *testing.T) {
	t.Parallel()

	a := &MatrixAdapter{}
	err := a.Configure(map[string]any{
		"access_token": "token",
	})
	if err == nil {
		t.Fatal("expected error when home_server is missing")
	}
}

func TestMatrixConfigureMissingAccessToken(t *testing.T) {
	t.Parallel()

	a := &MatrixAdapter{}
	err := a.Configure(map[string]any{
		"home_server": "https://example.com",
	})
	if err == nil {
		t.Fatal("expected error when access_token is missing")
	}
}

func TestMatrixConfigureTrailingSlashTrimmed(t *testing.T) {
	t.Parallel()

	a := &MatrixAdapter{}
	err := a.Configure(map[string]any{
		"home_server":  "https://example.com/",
		"access_token": "token",
	})
	if err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if strings.HasSuffix(a.homeServer, "/") {
		t.Errorf("homeServer = %q, trailing slash should be trimmed after Configure", a.homeServer)
	}
}
