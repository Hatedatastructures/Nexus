package platforms

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Webhook -- parseWebhookPayload
// ---------------------------------------------------------------------------

func TestWebhookParseWebhookPayload(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)

	t.Run("text field", func(t *testing.T) {
		t.Parallel()
		event := a.parseWebhookPayload(map[string]any{"text": "hello"})
		if event == nil {
			t.Fatal("expected non-nil event")
		}
		if event.Text != "hello" {
			t.Errorf("Text = %q, want %q", event.Text, "hello")
		}
		if event.Source.Platform != PlatformWebhook {
			t.Errorf("Platform = %q, want %q", event.Source.Platform, PlatformWebhook)
		}
	})

	t.Run("content field", func(t *testing.T) {
		t.Parallel()
		event := a.parseWebhookPayload(map[string]any{"content": "world"})
		if event == nil {
			t.Fatal("expected non-nil event")
		}
		if event.Text != "world" {
			t.Errorf("Text = %q, want %q", event.Text, "world")
		}
	})

	t.Run("message field", func(t *testing.T) {
		t.Parallel()
		event := a.parseWebhookPayload(map[string]any{"message": "fallback"})
		if event == nil {
			t.Fatal("expected non-nil event")
		}
		if event.Text != "fallback" {
			t.Errorf("Text = %q, want %q", event.Text, "fallback")
		}
	})

	t.Run("no text returns nil", func(t *testing.T) {
		t.Parallel()
		event := a.parseWebhookPayload(map[string]any{"other": 123})
		if event != nil {
			t.Errorf("expected nil for no-text payload, got %+v", event)
		}
	})

	t.Run("sender extraction", func(t *testing.T) {
		t.Parallel()
		event := a.parseWebhookPayload(map[string]any{
			"text":    "hi",
			"from":    "user1",
			"chat_id": "chat1",
		})
		if event == nil {
			t.Fatal("expected non-nil event")
		}
		if event.Source.UserID != "user1" {
			t.Errorf("UserID = %q, want %q", event.Source.UserID, "user1")
		}
		if event.Source.ChatID != "chat1" {
			t.Errorf("ChatID = %q, want %q", event.Source.ChatID, "chat1")
		}
	})

	t.Run("auto-generated message ID", func(t *testing.T) {
		t.Parallel()
		event := a.parseWebhookPayload(map[string]any{"text": "test"})
		if event == nil {
			t.Fatal("expected non-nil event")
		}
		if event.MessageID == "" {
			t.Error("MessageID should be auto-generated when not provided")
		}
	})

	t.Run("provided message ID preserved", func(t *testing.T) {
		t.Parallel()
		event := a.parseWebhookPayload(map[string]any{
			"text":       "test",
			"message_id": "custom-id",
		})
		if event == nil {
			t.Fatal("expected non-nil event")
		}
		if event.MessageID != "custom-id" {
			t.Errorf("MessageID = %q, want %q", event.MessageID, "custom-id")
		}
	})

	t.Run("chat type from payload", func(t *testing.T) {
		t.Parallel()
		event := a.parseWebhookPayload(map[string]any{
			"text":      "test",
			"chat_type": "group",
		})
		if event == nil {
			t.Fatal("expected non-nil event")
		}
		if event.Source.ChatType != "group" {
			t.Errorf("ChatType = %q, want %q", event.Source.ChatType, "group")
		}
	})
}

// ---------------------------------------------------------------------------
// Webhook -- generateCryptoID
// ---------------------------------------------------------------------------

func TestGenerateCryptoID(t *testing.T) {
	t.Parallel()

	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateCryptoID()
		if id == "" {
			t.Fatal("generateCryptoID returned empty string")
		}
		if len(id) != 32 {
			// 16 bytes -> 32 hex chars
			t.Errorf("generateCryptoID() length = %d, want 32", len(id))
		}
		if ids[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}

// ---------------------------------------------------------------------------
// Discord -- chatTypeFromChannelID (indirect via handleMessageCreate)
// ---------------------------------------------------------------------------

// We test the helper `formatInt` instead which chatTypeFromChannelID relies on.

// ---------------------------------------------------------------------------
// Slack -- chatTypeFromChannel (indirect, but we can verify the adapter)
// ---------------------------------------------------------------------------

// chatTypeFromChannel is private, tested via integration with handleEventsAPI.
// Instead, we verify that the Slack adapter's Disconnect is safe.

func TestSlackDisconnectWithoutConnect(t *testing.T) {
	t.Parallel()

	a := NewSlackAdapter("xoxb-test", "xapp-test")
	// Disconnect without Connect should not panic.
	err := a.Disconnect(t.Context())
	if err != nil {
		t.Errorf("Disconnect() error = %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// Discord -- Disconnect without Connect
// ---------------------------------------------------------------------------

func TestDiscordDisconnectWithoutConnect(t *testing.T) {
	t.Parallel()

	a := NewDiscordAdapter("token")
	err := a.Disconnect(t.Context())
	if err != nil {
		t.Errorf("Disconnect() error = %v, want nil", err)
	}
}
