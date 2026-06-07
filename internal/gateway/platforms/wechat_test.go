package platforms

import (
	"context"
	"crypto/sha1"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WeChat adapter -- constructor
// ---------------------------------------------------------------------------

func TestNewWeChatAdapter(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app123", "secret456", "token789")
	if a == nil {
		t.Fatal("NewWeChatAdapter() returned nil")
	}
	if a.appID != "app123" {
		t.Errorf("appID = %q, want %q", a.appID, "app123")
	}
	if a.secret != "secret456" {
		t.Errorf("secret = %q, want %q", a.secret, "secret456")
	}
	if a.token != "token789" {
		t.Errorf("token = %q, want %q", a.token, "token789")
	}
}

// ---------------------------------------------------------------------------
// WeChat adapter -- interface compliance
// ---------------------------------------------------------------------------

func TestWeChatAdapterImplementsPlatformAdapter(t *testing.T) {
	t.Parallel()

	var _ PlatformAdapter = (*WeChatAdapter)(nil)
}

// ---------------------------------------------------------------------------
// WeChat adapter -- property accessors
// ---------------------------------------------------------------------------

func TestWeChatAdapterProperties(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	if a.Name() != "WeChat" {
		t.Errorf("Name() = %q, want %q", a.Name(), "WeChat")
	}
	if a.PlatformType() != PlatformWeChat {
		t.Errorf("PlatformType() = %q, want %q", a.PlatformType(), PlatformWeChat)
	}
	if a.MaxMessageLength() != 2048 {
		t.Errorf("MaxMessageLength() = %d, want 2048", a.MaxMessageLength())
	}
	if a.SupportsStreaming() {
		t.Error("SupportsStreaming() = true, want false")
	}
}

// ---------------------------------------------------------------------------
// WeChat adapter -- Connect / Disconnect
// ---------------------------------------------------------------------------

func TestWeChatAdapterConnectReturnsChannel(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	ch, err := a.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if ch == nil {
		t.Fatal("Connect() returned nil channel")
	}
}

func TestWeChatAdapterDisconnect(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	_, _ = a.Connect(context.Background())
	err := a.Disconnect(context.Background())
	if err != nil {
		t.Errorf("Disconnect() error = %v, want nil", err)
	}
}

func TestWeChatAdapterDisconnectIdempotent(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	_, _ = a.Connect(context.Background())

	// Second Disconnect should not panic
	_ = a.Disconnect(context.Background())
	_ = a.Disconnect(context.Background())
}

// ---------------------------------------------------------------------------
// WeChat adapter -- EditMessage (unsupported)
// ---------------------------------------------------------------------------

func TestWeChatEditMessageUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	result, err := a.EditMessage(context.Background(), "chat", "msg", "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected Success = false for WeChat EditMessage")
	}
	if result.Error == "" {
		t.Error("expected non-empty error message for EditMessage")
	}
}

// ---------------------------------------------------------------------------
// WeChat adapter -- DeleteMessage (unsupported)
// ---------------------------------------------------------------------------

func TestWeChatDeleteMessageUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	err := a.DeleteMessage(context.Background(), "chat", "msg")
	if err == nil {
		t.Fatal("expected error for WeChat DeleteMessage")
	}
}

// ---------------------------------------------------------------------------
// WeChat adapter -- SendTyping (noop)
// ---------------------------------------------------------------------------

func TestWeChatSendTypingNoop(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	if err := a.SendTyping(context.Background(), "chat"); err != nil {
		t.Errorf("SendTyping should be nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// WeChat adapter -- SendDocument (unsupported)
// ---------------------------------------------------------------------------

func TestWeChatSendDocumentUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	result, _ := a.SendDocument(context.Background(), "chat", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false for WeChat SendDocument")
	}
}

// ---------------------------------------------------------------------------
// WeChat adapter -- VerifySignature
// ---------------------------------------------------------------------------

func TestWeChatVerifySignature(t *testing.T) {
	t.Parallel()

	token := "mytoken123"
	a := NewWeChatAdapter("app", "secret", token)

	tests := []struct {
		name      string
		timestamp string
		nonce     string
		expectOK  bool
	}{
		{
			name:      "valid signature",
			timestamp: "1234567890",
			nonce:     "abc123",
			expectOK:  true,
		},
		{
			name:      "invalid signature",
			timestamp: "1234567890",
			nonce:     "abc123",
			expectOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parts := []string{token, tc.timestamp, tc.nonce}
			sort.Strings(parts)
			joined := strings.Join(parts, "")
			hash := fmt.Sprintf("%x", sha1.Sum([]byte(joined)))

			if !tc.expectOK {
				hash = "invalidsignature"
			}

			result := a.VerifySignature(hash, tc.timestamp, tc.nonce)
			if result != tc.expectOK {
				t.Errorf("VerifySignature() = %v, want %v", result, tc.expectOK)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WeChat adapter -- Configure
// ---------------------------------------------------------------------------

func TestWeChatConfigureSuccess(t *testing.T) {
	t.Parallel()

	a := &WeChatAdapter{}
	err := a.Configure(map[string]any{
		"app_id": "myapp",
		"secret": "mysecret",
		"token":  "mytoken",
	})
	if err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if a.appID != "myapp" {
		t.Errorf("appID = %q, want %q", a.appID, "myapp")
	}
	if a.secret != "mysecret" {
		t.Errorf("secret = %q, want %q", a.secret, "mysecret")
	}
	if a.token != "mytoken" {
		t.Errorf("token = %q, want %q", a.token, "mytoken")
	}
}

func TestWeChatConfigureMissingAppID(t *testing.T) {
	t.Parallel()

	a := &WeChatAdapter{}
	err := a.Configure(map[string]any{
		"secret": "mysecret",
		"token":  "mytoken",
	})
	if err == nil {
		t.Fatal("expected error when app_id is missing")
	}
}

func TestWeChatConfigureMissingSecret(t *testing.T) {
	t.Parallel()

	a := &WeChatAdapter{}
	err := a.Configure(map[string]any{
		"app_id": "myapp",
		"token":  "mytoken",
	})
	if err == nil {
		t.Fatal("expected error when secret is missing")
	}
}

func TestWeChatConfigureMissingToken(t *testing.T) {
	t.Parallel()

	a := &WeChatAdapter{}
	err := a.Configure(map[string]any{
		"app_id": "myapp",
		"secret": "mysecret",
	})
	if err == nil {
		t.Fatal("expected error when token is missing")
	}
}

func TestWeChatConfigureEmptyMap(t *testing.T) {
	t.Parallel()

	a := &WeChatAdapter{}
	err := a.Configure(map[string]any{})
	if err == nil {
		t.Fatal("expected error when all settings are missing")
	}
}

// ---------------------------------------------------------------------------
// WeChat adapter -- sanitizeCDATA
// ---------------------------------------------------------------------------

func TestSanitizeCDATA(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"]]>", "]]]]><![CDATA[>"},
		{"no cdata here", "no cdata here"},
		{"end]]>more", "end]]]]><![CDATA[>more"},
		{"multiple]]>]]>end", "multiple]]]]><![CDATA[>]]]]><![CDATA[>end"},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := sanitizeCDATA(tc.input)
			if got != tc.expected {
				t.Errorf("sanitizeCDATA(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WeChat adapter -- buildReplyXML
// ---------------------------------------------------------------------------

func TestWeChatBuildReplyXML(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	xml := a.buildReplyXML("toUser", "fromUser")

	if !strings.Contains(xml, "ToUserName") {
		t.Error("reply XML should contain ToUserName")
	}
	if !strings.Contains(xml, "FromUserName") {
		t.Error("reply XML should contain FromUserName")
	}
	if !strings.Contains(xml, "正在处理中") {
		t.Error("reply XML should contain processing message")
	}
}
