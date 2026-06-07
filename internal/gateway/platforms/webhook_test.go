package platforms

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Webhook adapter -- constructor
// ---------------------------------------------------------------------------

func TestNewWebhookAdapterConstructor(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	if a == nil {
		t.Fatal("NewWebhookAdapter() returned nil")
	}
	if a.port != webhookDefaultPort {
		t.Errorf("port = %d, want %d", a.port, webhookDefaultPort)
	}
	if a.path != webhookDefaultPath {
		t.Errorf("path = %q, want %q", a.path, webhookDefaultPath)
	}
}

// ---------------------------------------------------------------------------
// Webhook adapter -- interface compliance
// ---------------------------------------------------------------------------

func TestWebhookAdapterImplementsPlatformAdapter(t *testing.T) {
	t.Parallel()

	var _ PlatformAdapter = (*WebhookAdapter)(nil)
}

// ---------------------------------------------------------------------------
// Webhook adapter -- parseWebhookPayload (already partially in common_test.go)
// ---------------------------------------------------------------------------

func TestWebhookParsePayloadFromField(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	event := a.parseWebhookPayload(map[string]any{
		"text": "hello",
		"from": "user1",
	})
	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.Source.UserID != "user1" {
		t.Errorf("UserID = %q, want %q", event.Source.UserID, "user1")
	}
}

func TestWebhookParsePayloadChannelField(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	event := a.parseWebhookPayload(map[string]any{
		"text":    "hello",
		"channel": "ch1",
	})
	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.Source.ChatID != "ch1" {
		t.Errorf("ChatID = %q, want %q", event.Source.ChatID, "ch1")
	}
}

func TestWebhookParsePayloadRoomField(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	event := a.parseWebhookPayload(map[string]any{
		"text": "hello",
		"room": "room1",
	})
	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.Source.ChatID != "room1" {
		t.Errorf("ChatID = %q, want %q", event.Source.ChatID, "room1")
	}
}

func TestWebhookParsePayloadIDField(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	event := a.parseWebhookPayload(map[string]any{
		"text": "hello",
		"id":   "custom-id",
	})
	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.MessageID != "custom-id" {
		t.Errorf("MessageID = %q, want %q", event.MessageID, "custom-id")
	}
}

func TestWebhookParsePayloadTypeField(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	event := a.parseWebhookPayload(map[string]any{
		"text": "hello",
		"type": "channel",
	})
	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.Source.ChatType != "channel" {
		t.Errorf("ChatType = %q, want %q", event.Source.ChatType, "channel")
	}
}

func TestWebhookParsePayloadRawMessage(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	payload := map[string]any{
		"text":  "hello",
		"extra": "data",
	}
	event := a.parseWebhookPayload(payload)
	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.RawMessage == nil {
		t.Error("expected non-nil RawMessage")
	}
}


func TestWebhookParseIntFunc(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected int
		hasErr   bool
	}{
		{"0", 0, false},
		{"8080", 8080, false},
		{"  42  ", 42, false},
		{"", 0, false},
		{"abc", 0, true},
		{"-1", 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got, err := parseInt(tc.input)
			if tc.hasErr && err == nil {
				t.Errorf("parseInt(%q) expected error", tc.input)
			}
			if !tc.hasErr && err != nil {
				t.Errorf("parseInt(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.expected {
				t.Errorf("parseInt(%q) = %d, want %d", tc.input, got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Webhook adapter -- isLoopback
// ---------------------------------------------------------------------------

func TestWebhookIsLoopback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr string
		want bool
	}{
		{":8080", false},
		{"0.0.0.0:8080", false},
		{"127.0.0.1", true},
		{"localhost", true},
		{"::1", false},
		{"192.168.1.1:8080", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			t.Parallel()
			got := isLoopback(tc.addr)
			if got != tc.want {
				t.Errorf("isLoopback(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

