package platforms

import (
	"context"
	"testing"
)

// ---------------------------------------------------------------------------
// DingTalk adapter -- constructor
// ---------------------------------------------------------------------------

func TestNewDingTalkAdapter(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key123", "secret456")
	if a == nil {
		t.Fatal("NewDingTalkAdapter() returned nil")
	}
	if a.appKey != "key123" {
		t.Errorf("appKey = %q, want %q", a.appKey, "key123")
	}
	if a.appSecret != "secret456" {
		t.Errorf("appSecret = %q, want %q", a.appSecret, "secret456")
	}
}

// ---------------------------------------------------------------------------
// DingTalk adapter -- interface compliance
// ---------------------------------------------------------------------------

func TestDingTalkAdapterImplementsPlatformAdapter(t *testing.T) {
	t.Parallel()

	var _ PlatformAdapter = (*DingTalkAdapter)(nil)
}

// ---------------------------------------------------------------------------
// DingTalk adapter -- property accessors
// ---------------------------------------------------------------------------

func TestDingTalkAdapterProperties(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	if a.Name() != "DingTalk" {
		t.Errorf("Name() = %q, want %q", a.Name(), "DingTalk")
	}
	if a.PlatformType() != PlatformDingTalk {
		t.Errorf("PlatformType() = %q, want %q", a.PlatformType(), PlatformDingTalk)
	}
	if a.MaxMessageLength() != 20000 {
		t.Errorf("MaxMessageLength() = %d, want 20000", a.MaxMessageLength())
	}
	if !a.SupportsStreaming() {
		t.Error("SupportsStreaming() = false, want true")
	}
}

// ---------------------------------------------------------------------------
// DingTalk adapter -- Connect / Disconnect
// ---------------------------------------------------------------------------

func TestDingTalkAdapterDisconnect(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	err := a.Disconnect(context.Background())
	if err != nil {
		t.Errorf("Disconnect() error = %v, want nil", err)
	}
}

func TestDingTalkAdapterDisconnectIdempotent(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	_ = a.Disconnect(context.Background())
	_ = a.Disconnect(context.Background()) // should not panic
}

// ---------------------------------------------------------------------------
// DingTalk adapter -- SendTyping (noop)
// ---------------------------------------------------------------------------

func TestDingTalkSendTypingNoop(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	if err := a.SendTyping(context.Background(), "chat"); err != nil {
		t.Errorf("SendTyping should be nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// DingTalk adapter -- Configure
// ---------------------------------------------------------------------------

func TestDingTalkConfigureSuccess(t *testing.T) {
	t.Parallel()

	a := &DingTalkAdapter{}
	err := a.Configure(map[string]any{
		"app_key":         "mykey",
		"app_secret":      "mysecret",
		"callback_secret": "cbsecret",
	})
	if err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if a.appKey != "mykey" {
		t.Errorf("appKey = %q, want %q", a.appKey, "mykey")
	}
	if a.appSecret != "mysecret" {
		t.Errorf("appSecret = %q, want %q", a.appSecret, "mysecret")
	}
	if a.callbackSecret != "cbsecret" {
		t.Errorf("callbackSecret = %q, want %q", a.callbackSecret, "cbsecret")
	}
}

func TestDingTalkConfigureMissingAppKey(t *testing.T) {
	t.Parallel()

	a := &DingTalkAdapter{}
	err := a.Configure(map[string]any{
		"app_secret": "secret",
	})
	if err == nil {
		t.Fatal("expected error when app_key is missing")
	}
}

func TestDingTalkConfigureMissingAppSecret(t *testing.T) {
	t.Parallel()

	a := &DingTalkAdapter{}
	err := a.Configure(map[string]any{
		"app_key": "key",
	})
	if err == nil {
		t.Fatal("expected error when app_secret is missing")
	}
}

func TestDingTalkConfigureEmptyMap(t *testing.T) {
	t.Parallel()

	a := &DingTalkAdapter{}
	err := a.Configure(map[string]any{})
	if err == nil {
		t.Fatal("expected error when all settings are missing")
	}
}
