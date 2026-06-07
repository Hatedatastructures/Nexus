package platforms

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// ---------------------------------------------------------------------------
// WhatsApp adapter -- constructor
// ---------------------------------------------------------------------------

func TestNewWhatsAppAdapter(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token123", "phone456")
	if a == nil {
		t.Fatal("NewWhatsAppAdapter() returned nil")
	}
	if a.token != "token123" {
		t.Errorf("token = %q, want %q", a.token, "token123")
	}
	if a.phoneID != "phone456" {
		t.Errorf("phoneID = %q, want %q", a.phoneID, "phone456")
	}
}

// ---------------------------------------------------------------------------
// WhatsApp adapter -- interface compliance
// ---------------------------------------------------------------------------

func TestWhatsAppAdapterImplementsPlatformAdapter(t *testing.T) {
	t.Parallel()

	var _ PlatformAdapter = (*WhatsAppAdapter)(nil)
}

// ---------------------------------------------------------------------------
// WhatsApp adapter -- property accessors
// ---------------------------------------------------------------------------

func TestWhatsAppAdapterProperties(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	if a.Name() != "WhatsApp" {
		t.Errorf("Name() = %q, want %q", a.Name(), "WhatsApp")
	}
	if a.PlatformType() != PlatformWhatsApp {
		t.Errorf("PlatformType() = %q, want %q", a.PlatformType(), PlatformWhatsApp)
	}
	if a.MaxMessageLength() != 4096 {
		t.Errorf("MaxMessageLength() = %d, want 4096", a.MaxMessageLength())
	}
	if a.SupportsStreaming() {
		t.Error("SupportsStreaming() = true, want false")
	}
}

// ---------------------------------------------------------------------------
// WhatsApp adapter -- Connect / Disconnect
// ---------------------------------------------------------------------------

func TestWhatsAppAdapterConnectReturnsChannel(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	ch, err := a.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if ch == nil {
		t.Fatal("Connect() returned nil channel")
	}
}

func TestWhatsAppAdapterDisconnect(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	_, _ = a.Connect(context.Background())
	err := a.Disconnect(context.Background())
	if err != nil {
		t.Errorf("Disconnect() error = %v, want nil", err)
	}
}

func TestWhatsAppAdapterDisconnectIdempotent(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	_, _ = a.Connect(context.Background())
	_ = a.Disconnect(context.Background())
	_ = a.Disconnect(context.Background()) // should not panic
}

// ---------------------------------------------------------------------------
// WhatsApp adapter -- EditMessage (unsupported)
// ---------------------------------------------------------------------------

func TestWhatsAppEditMessageUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	result, err := a.EditMessage(context.Background(), "chat", "msg", "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected Success = false for WhatsApp EditMessage")
	}
}

// ---------------------------------------------------------------------------
// WhatsApp adapter -- DeleteMessage (unsupported)
// ---------------------------------------------------------------------------

func TestWhatsAppDeleteMessageUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	err := a.DeleteMessage(context.Background(), "chat", "msg")
	if err == nil {
		t.Fatal("expected error for WhatsApp DeleteMessage")
	}
}

// ---------------------------------------------------------------------------
// WhatsApp adapter -- SendTyping (noop)
// ---------------------------------------------------------------------------

func TestWhatsAppSendTypingNoop(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	if err := a.SendTyping(context.Background(), "chat"); err != nil {
		t.Errorf("SendTyping should be nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// WhatsApp adapter -- Configure
// ---------------------------------------------------------------------------

func TestWhatsAppConfigureSuccess(t *testing.T) {
	t.Parallel()

	a := &WhatsAppAdapter{}
	err := a.Configure(map[string]any{
		"token":        "mytoken",
		"phone_id":     "myphone",
		"verify_token": "verify",
		"app_secret":   "secret",
	})
	if err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if a.token != "mytoken" {
		t.Errorf("token = %q, want %q", a.token, "mytoken")
	}
	if a.phoneID != "myphone" {
		t.Errorf("phoneID = %q, want %q", a.phoneID, "myphone")
	}
	if a.verifyToken != "verify" {
		t.Errorf("verifyToken = %q, want %q", a.verifyToken, "verify")
	}
	if a.appSecret != "secret" {
		t.Errorf("appSecret = %q, want %q", a.appSecret, "secret")
	}
}

func TestWhatsAppConfigureMissingToken(t *testing.T) {
	t.Parallel()

	a := &WhatsAppAdapter{}
	err := a.Configure(map[string]any{
		"phone_id": "myphone",
	})
	if err == nil {
		t.Fatal("expected error when token is missing")
	}
}

func TestWhatsAppConfigureMissingPhoneID(t *testing.T) {
	t.Parallel()

	a := &WhatsAppAdapter{}
	err := a.Configure(map[string]any{
		"token": "mytoken",
	})
	if err == nil {
		t.Fatal("expected error when phone_id is missing")
	}
}

// ---------------------------------------------------------------------------
// WhatsApp adapter -- VerifyWebhook
// ---------------------------------------------------------------------------

func TestWhatsAppVerifyWebhook(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	a.verifyToken = "myverifytoken"

	tests := []struct {
		name         string
		mode         string
		challenge    string
		verifyToken  string
		expectOK     bool
		expectString string
	}{
		{
			name:         "valid subscribe",
			mode:         "subscribe",
			challenge:    "challenge123",
			verifyToken:  "myverifytoken",
			expectOK:     true,
			expectString: "challenge123",
		},
		{
			name:         "wrong mode",
			mode:         "unsubscribe",
			challenge:    "challenge123",
			verifyToken:  "myverifytoken",
			expectOK:     false,
			expectString: "",
		},
		{
			name:         "wrong token",
			mode:         "subscribe",
			challenge:    "challenge123",
			verifyToken:  "wrongtoken",
			expectOK:     false,
			expectString: "",
		},
		{
			name:         "empty mode",
			mode:         "",
			challenge:    "challenge123",
			verifyToken:  "myverifytoken",
			expectOK:     false,
			expectString: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			challenge, ok := a.VerifyWebhook(tc.mode, tc.challenge, tc.verifyToken)
			if ok != tc.expectOK {
				t.Errorf("VerifyWebhook() ok = %v, want %v", ok, tc.expectOK)
			}
			if challenge != tc.expectString {
				t.Errorf("VerifyWebhook() challenge = %q, want %q", challenge, tc.expectString)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WhatsApp adapter -- VerifyWebhookSignature
// ---------------------------------------------------------------------------

func TestWhatsAppVerifyWebhookSignature(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	a.appSecret = "myappsecret"

	body := []byte(`{"entry":[]}`)
	mac := hmac.New(sha256.New, []byte("myappsecret"))
	mac.Write(body)
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	tests := []struct {
		name      string
		signature string
		body      []byte
		expectOK  bool
	}{
		{
			name:      "valid signature",
			signature: "sha256=" + expectedSig,
			body:      body,
			expectOK:  true,
		},
		{
			name:      "invalid signature",
			signature: "sha256=invalidsig",
			body:      body,
			expectOK:  false,
		},
		{
			name:      "missing prefix",
			signature: expectedSig,
			body:      body,
			expectOK:  false,
		},
		{
			name:      "empty signature",
			signature: "",
			body:      body,
			expectOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := a.VerifyWebhookSignature(tc.signature, tc.body)
			if result != tc.expectOK {
				t.Errorf("VerifyWebhookSignature() = %v, want %v", result, tc.expectOK)
			}
		})
	}
}

func TestWhatsAppVerifyWebhookSignatureNoSecret(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	// appSecret is empty
	result := a.VerifyWebhookSignature("sha256=something", []byte("body"))
	if result {
		t.Error("expected false when appSecret is not configured")
	}
}

// ---------------------------------------------------------------------------
// WhatsApp adapter -- session key construction
// ---------------------------------------------------------------------------

func TestWhatsAppSessionKeyConstruction(t *testing.T) {
	t.Parallel()

	src := &SessionSource{
		Platform: PlatformWhatsApp,
		ChatType: "dm",
		ChatID:   "1234567890",
	}
	key := BuildSessionKey(src)
	expected := "agent:main:whatsapp:dm:1234567890"
	if key != expected {
		t.Errorf("BuildSessionKey() = %q, want %q", key, expected)
	}
}
