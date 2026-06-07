package platforms

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// WhatsApp adapter -- ReceiveWebhook
// ---------------------------------------------------------------------------

func TestWhatsAppReceiveWebhookTextMessage(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	a.msgCh = make(chan *MessageEvent, 128)

	payload, _ := json.Marshal(map[string]any{
		"entry": []map[string]any{
			{
				"changes": []map[string]any{
					{
						"value": map[string]any{
							"messages": []map[string]any{
								{
									"id":        "wamid123",
									"from":      "1234567890",
									"type":      "text",
									"timestamp": 1700000000,
									"text": map[string]string{
										"body": "Hello WhatsApp",
									},
								},
							},
						},
					},
				},
			},
		},
	})

	err := a.ReceiveWebhook(payload)
	if err != nil {
		t.Fatalf("ReceiveWebhook() error = %v", err)
	}

	select {
	case event := <-a.msgCh:
		if event.Text != "Hello WhatsApp" {
			t.Errorf("Text = %q, want %q", event.Text, "Hello WhatsApp")
		}
		if event.MessageType != MsgText {
			t.Errorf("MessageType = %q, want %q", event.MessageType, MsgText)
		}
		if event.Source.Platform != PlatformWhatsApp {
			t.Errorf("Platform = %q, want %q", event.Source.Platform, PlatformWhatsApp)
		}
		if event.Source.ChatID != "1234567890" {
			t.Errorf("ChatID = %q, want %q", event.Source.ChatID, "1234567890")
		}
		if event.MessageID != "wamid123" {
			t.Errorf("MessageID = %q, want %q", event.MessageID, "wamid123")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message on channel")
	}
}

func TestWhatsAppReceiveWebhookImageMessage(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	a.msgCh = make(chan *MessageEvent, 128)

	payload, _ := json.Marshal(map[string]any{
		"entry": []map[string]any{
			{
				"changes": []map[string]any{
					{
						"value": map[string]any{
							"messages": []map[string]any{
								{
									"id":        "wamid_img",
									"from":      "1234567890",
									"type":      "image",
									"timestamp": 1700000000,
									"image": map[string]string{
										"id":      "media_id_123",
										"caption": "A photo",
									},
								},
							},
						},
					},
				},
			},
		},
	})

	_ = a.ReceiveWebhook(payload)

	select {
	case event := <-a.msgCh:
		if event.MessageType != MsgPhoto {
			t.Errorf("MessageType = %q, want %q", event.MessageType, MsgPhoto)
		}
		if event.Text != "A photo" {
			t.Errorf("Text = %q, want %q", event.Text, "A photo")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message on channel")
	}
}

func TestWhatsAppReceiveWebhookUnsupportedType(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	a.msgCh = make(chan *MessageEvent, 128)

	payload, _ := json.Marshal(map[string]any{
		"entry": []map[string]any{
			{
				"changes": []map[string]any{
					{
						"value": map[string]any{
							"messages": []map[string]any{
								{
									"id":        "wamid_unsup",
									"from":      "1234567890",
									"type":      "contacts",
									"timestamp": 1700000000,
								},
							},
						},
					},
				},
			},
		},
	})

	_ = a.ReceiveWebhook(payload)

	select {
	case event := <-a.msgCh:
		if !strings.Contains(event.Text, "unsupported message type") {
			t.Errorf("Text = %q, should contain unsupported type indicator", event.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message on channel")
	}
}

func TestWhatsAppReceiveWebhookInvalidJSON(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	err := a.ReceiveWebhook([]byte("invalid json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWhatsAppReceiveWebhookNoMessages(t *testing.T) {
	t.Parallel()

	a := NewWhatsAppAdapter("token", "phoneID")
	a.msgCh = make(chan *MessageEvent, 128)

	payload, _ := json.Marshal(map[string]any{
		"entry": []map[string]any{},
	})

	err := a.ReceiveWebhook(payload)
	if err != nil {
		t.Fatalf("ReceiveWebhook() error = %v", err)
	}

	select {
	case <-a.msgCh:
		t.Error("expected no message for empty webhook")
	case <-time.After(100 * time.Millisecond):
		// expected
	}
}

// ---------------------------------------------------------------------------
// WhatsApp adapter -- convertMessage internal types
// ---------------------------------------------------------------------------

func TestWhatsAppMessageTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msgType    string
		expected   MessageType
		hasMedia   bool
		mediaField string
	}{
		{"text", MsgText, false, ""},
		{"image", MsgPhoto, true, "image"},
		{"audio", MsgVoice, false, ""},
		{"voice", MsgVoice, false, ""},
		{"video", MsgVideo, false, ""},
		{"document", MsgDocument, false, ""},
		{"sticker", MsgSticker, false, ""},
		{"location", MsgLocation, false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.msgType, func(t *testing.T) {
			t.Parallel()

			a := NewWhatsAppAdapter("token", "phoneID")
			msg := &whatsappMessage{
				ID:        "test",
				From:      "123",
				Type:      tc.msgType,
				Timestamp: 1700000000,
			}
			if tc.msgType == "text" {
				msg.Text.Body = "hello"
			}

			event := a.convertMessage(msg)
			if event == nil {
				t.Fatal("convertMessage() returned nil")
			}
			if event.MessageType != tc.expected {
				t.Errorf("MessageType = %q, want %q", event.MessageType, tc.expected)
			}
		})
	}
}
