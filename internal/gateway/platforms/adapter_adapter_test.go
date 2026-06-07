package platforms

import (
	"testing"
)

// ---------------------------------------------------------------------------
// PlatformAdapter interface compile-time checks
// ---------------------------------------------------------------------------

// Verify concrete types satisfy PlatformAdapter.
func TestPlatformAdapterImplementations(t *testing.T) {
	t.Parallel()

	var _ PlatformAdapter = (*TelegramAdapter)(nil)
	var _ PlatformAdapter = (*DiscordAdapter)(nil)
	var _ PlatformAdapter = (*SlackAdapter)(nil)
	var _ PlatformAdapter = (*SignalAdapter)(nil)
	var _ PlatformAdapter = (*WebhookAdapter)(nil)
}

// ---------------------------------------------------------------------------
// Adapter basic property tests (no network required)
// ---------------------------------------------------------------------------

func TestTelegramAdapterProperties(t *testing.T) {
	t.Parallel()

	a := NewTelegramAdapter("test-token")
	if a.Name() != "Telegram" {
		t.Errorf("Name() = %q, want %q", a.Name(), "Telegram")
	}
	if a.PlatformType() != PlatformTelegram {
		t.Errorf("PlatformType() = %q, want %q", a.PlatformType(), PlatformTelegram)
	}
	if a.MaxMessageLength() != 4096 {
		t.Errorf("MaxMessageLength() = %d, want 4096", a.MaxMessageLength())
	}
	if !a.SupportsStreaming() {
		t.Error("SupportsStreaming() = false, want true")
	}
}

func TestDiscordAdapterProperties(t *testing.T) {
	t.Parallel()

	a := NewDiscordAdapter("test-token")
	if a.Name() != "Discord" {
		t.Errorf("Name() = %q, want %q", a.Name(), "Discord")
	}
	if a.PlatformType() != PlatformDiscord {
		t.Errorf("PlatformType() = %q, want %q", a.PlatformType(), PlatformDiscord)
	}
	if a.MaxMessageLength() != 2000 {
		t.Errorf("MaxMessageLength() = %d, want 2000", a.MaxMessageLength())
	}
	if !a.SupportsStreaming() {
		t.Error("SupportsStreaming() = false, want true")
	}
}

func TestSlackAdapterProperties(t *testing.T) {
	t.Parallel()

	a := NewSlackAdapter("xoxb-test", "xapp-test")
	if a.Name() != "Slack" {
		t.Errorf("Name() = %q, want %q", a.Name(), "Slack")
	}
	if a.PlatformType() != PlatformSlack {
		t.Errorf("PlatformType() = %q, want %q", a.PlatformType(), PlatformSlack)
	}
	if a.MaxMessageLength() != 4000 {
		t.Errorf("MaxMessageLength() = %d, want 4000", a.MaxMessageLength())
	}
	if !a.SupportsStreaming() {
		t.Error("SupportsStreaming() = false, want true")
	}
}

func TestSignalAdapterProperties(t *testing.T) {
	t.Parallel()

	a := NewSignalAdapter(nil)
	if a.Name() != "Signal" {
		t.Errorf("Name() = %q, want %q", a.Name(), "Signal")
	}
	if a.PlatformType() != PlatformSignal {
		t.Errorf("PlatformType() = %q, want %q", a.PlatformType(), PlatformSignal)
	}
	if a.MaxMessageLength() != 2000 {
		t.Errorf("MaxMessageLength() = %d, want 2000", a.MaxMessageLength())
	}
	if a.SupportsStreaming() {
		t.Error("SupportsStreaming() = true, want false")
	}
}

func TestWebhookAdapterProperties(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	if a.Name() != "Webhook" {
		t.Errorf("Name() = %q, want %q", a.Name(), "Webhook")
	}
	if a.PlatformType() != PlatformWebhook {
		t.Errorf("PlatformType() = %q, want %q", a.PlatformType(), PlatformWebhook)
	}
	if a.MaxMessageLength() != 10000 {
		t.Errorf("MaxMessageLength() = %d, want 10000", a.MaxMessageLength())
	}
	if a.SupportsStreaming() {
		t.Error("SupportsStreaming() = true, want false")
	}
}

// ---------------------------------------------------------------------------
// ConfigurableAdapter -- Configure validation
// ---------------------------------------------------------------------------

func TestTelegramAdapterConfigureMissingToken(t *testing.T) {
	t.Parallel()

	a := &TelegramAdapter{}
	err := a.Configure(map[string]any{})
	if err == nil {
		t.Fatal("expected error when token is missing")
	}
}

func TestTelegramAdapterConfigureWithToken(t *testing.T) {
	t.Parallel()

	a := &TelegramAdapter{}
	err := a.Configure(map[string]any{"token": "123:ABC"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.token != "123:ABC" {
		t.Errorf("token = %q, want %q", a.token, "123:ABC")
	}
}

func TestDiscordAdapterConfigureMissingToken(t *testing.T) {
	t.Parallel()

	a := &DiscordAdapter{}
	err := a.Configure(map[string]any{})
	if err == nil {
		t.Fatal("expected error when token is missing")
	}
}

func TestDiscordAdapterConfigureWithToken(t *testing.T) {
	t.Parallel()

	a := &DiscordAdapter{}
	err := a.Configure(map[string]any{"token": "bot-token"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSlackAdapterConfigureMissingTokens(t *testing.T) {
	t.Parallel()

	a := &SlackAdapter{}
	err := a.Configure(map[string]any{})
	if err == nil {
		t.Fatal("expected error when tokens are missing")
	}
}

func TestSlackAdapterConfigureWithTokens(t *testing.T) {
	t.Parallel()

	a := &SlackAdapter{}
	err := a.Configure(map[string]any{
		"bot_token": "xoxb-test",
		"app_token": "xapp-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Signal adapter -- unsupported operations
// ---------------------------------------------------------------------------

func TestSignalEditMessageUnsupported(t *testing.T) {
	t.Parallel()

	a := NewSignalAdapter(nil)
	result, err := a.EditMessage(t.Context(), "chat", "msg", "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected Success = false for Signal EditMessage")
	}
}

func TestSignalDeleteMessageUnsupported(t *testing.T) {
	t.Parallel()

	a := NewSignalAdapter(nil)
	err := a.DeleteMessage(t.Context(), "chat", "msg")
	if err == nil {
		t.Fatal("expected error for Signal DeleteMessage")
	}
}

// ---------------------------------------------------------------------------
// Webhook adapter -- unsupported operations
// ---------------------------------------------------------------------------

func TestWebhookEditMessageUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	result, err := a.EditMessage(t.Context(), "chat", "msg", "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected Success = false for Webhook EditMessage")
	}
}

func TestWebhookDeleteMessageUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	err := a.DeleteMessage(t.Context(), "chat", "msg")
	if err == nil {
		t.Fatal("expected error for Webhook DeleteMessage")
	}
}

func TestWebhookSendVoiceUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	result, _ := a.SendVoice(t.Context(), "chat", "/path", nil)
	if result.Success {
		t.Error("expected Success = false for Webhook SendVoice")
	}
}

func TestWebhookSendVideoUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	result, _ := a.SendVideo(t.Context(), "chat", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false for Webhook SendVideo")
	}
}

func TestWebhookSendDocumentUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	result, _ := a.SendDocument(t.Context(), "chat", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false for Webhook SendDocument")
	}
}

func TestWebhookSendNoURL(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	result, _ := a.Send(t.Context(), "chat", "hello", nil)
	if result.Success {
		t.Error("expected Success = false when WEBHOOK_SEND_URL is empty")
	}
}

func TestWebhookSendTypingNoop(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	if err := a.SendTyping(t.Context(), "chat"); err != nil {
		t.Errorf("SendTyping should be nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Slack adapter -- unsupported media
// ---------------------------------------------------------------------------

func TestSlackSendVoiceUnsupported(t *testing.T) {
	t.Parallel()

	a := NewSlackAdapter("xoxb-test", "xapp-test")
	result, _ := a.SendVoice(t.Context(), "ch", "/path", nil)
	if result.Success {
		t.Error("expected Success = false for Slack SendVoice")
	}
}

func TestSlackSendDocumentUnsupported(t *testing.T) {
	t.Parallel()

	a := NewSlackAdapter("xoxb-test", "xapp-test")
	result, _ := a.SendDocument(t.Context(), "ch", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false for Slack SendDocument")
	}
}

func TestSlackSendTypingNoop(t *testing.T) {
	t.Parallel()

	a := NewSlackAdapter("xoxb-test", "xapp-test")
	if err := a.SendTyping(t.Context(), "ch"); err != nil {
		t.Errorf("SendTyping should be nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Discord adapter -- unsupported media
// ---------------------------------------------------------------------------

func TestDiscordSendVoiceUnsupported(t *testing.T) {
	t.Parallel()

	a := NewDiscordAdapter("token")
	result, _ := a.SendVoice(t.Context(), "ch", "/path", nil)
	if result.Success {
		t.Error("expected Success = false for Discord SendVoice")
	}
}

func TestDiscordSendDocumentUnsupported(t *testing.T) {
	t.Parallel()

	a := NewDiscordAdapter("token")
	result, _ := a.SendDocument(t.Context(), "ch", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false for Discord SendDocument")
	}
}
