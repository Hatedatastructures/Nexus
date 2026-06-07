package platforms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Webhook adapter -- handleWebhook HTTP handler
// ---------------------------------------------------------------------------

func TestWebhookHandlerRejectsNonPost(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	msgCh := make(chan *MessageEvent, 100)
	handler := a.handleWebhook(msgCh)

	tests := []struct {
		method string
		status int
	}{
		{"GET", http.StatusMethodNotAllowed},
		{"PUT", http.StatusMethodNotAllowed},
		{"DELETE", http.StatusMethodNotAllowed},
	}

	for _, tc := range tests {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, "/webhook", nil)
			w := httptest.NewRecorder()
			handler(w, req)
			if w.Code != tc.status {
				t.Errorf("status = %d, want %d", w.Code, tc.status)
			}
		})
	}
}

func TestWebhookHandlerAcceptsValidPost(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	msgCh := make(chan *MessageEvent, 100)
	handler := a.handleWebhook(msgCh)

	body := `{"text":"hello world","user_id":"user1"}`
	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestWebhookHandlerRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	msgCh := make(chan *MessageEvent, 100)
	handler := a.handleWebhook(msgCh)

	req := httptest.NewRequest("POST", "/webhook", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestWebhookHandlerSecretValidation(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	a.secret = "mysecret"
	msgCh := make(chan *MessageEvent, 100)
	handler := a.handleWebhook(msgCh)

	tests := []struct {
		name       string
		authHeader string
		status     int
	}{
		{"valid bearer", "Bearer mysecret", http.StatusOK},
		{"valid raw secret", "mysecret", http.StatusOK},
		{"invalid secret", "Bearer wrong", http.StatusUnauthorized},
		{"missing auth", "", http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := `{"text":"hello"}`
			req := httptest.NewRequest("POST", "/webhook", strings.NewReader(body))
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()
			handler(w, req)
			if w.Code != tc.status {
				t.Errorf("status = %d, want %d", w.Code, tc.status)
			}
		})
	}
}

func TestWebhookHandlerNoSecret(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	// secret is empty
	msgCh := make(chan *MessageEvent, 100)
	handler := a.handleWebhook(msgCh)

	body := `{"text":"hello"}`
	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (no secret required)", w.Code, http.StatusOK)
	}
}

func TestWebhookHandlerPushesMessage(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	msgCh := make(chan *MessageEvent, 100)
	handler := a.handleWebhook(msgCh)

	body := `{"text":"test message","user_id":"u1","chat_id":"c1"}`
	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler(w, req)

	select {
	case event := <-msgCh:
		if event.Text != "test message" {
			t.Errorf("Text = %q, want %q", event.Text, "test message")
		}
		if event.Source.UserID != "u1" {
			t.Errorf("UserID = %q, want %q", event.Source.UserID, "u1")
		}
		if event.Source.ChatID != "c1" {
			t.Errorf("ChatID = %q, want %q", event.Source.ChatID, "c1")
		}
	default:
		t.Fatal("expected message on channel")
	}
}

func TestWebhookHandlerDropsNoText(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	msgCh := make(chan *MessageEvent, 100)
	handler := a.handleWebhook(msgCh)

	body := `{"other":"no text field"}`
	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler(w, req)

	// Still returns 200 but no message pushed
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	select {
	case <-msgCh:
		t.Error("should not push message when no text")
	default:
		// expected
	}
}

// ---------------------------------------------------------------------------
// Webhook adapter -- Send
// ---------------------------------------------------------------------------

func TestWebhookSendNoURLConfigured(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	result, _ := a.Send(context.Background(), "chat", "hello", nil)
	if result.Success {
		t.Error("expected Success = false when WEBHOOK_SEND_URL is empty")
	}
	if !strings.Contains(result.Error, "WEBHOOK_SEND_URL") {
		t.Errorf("Error = %q, should mention WEBHOOK_SEND_URL", result.Error)
	}
}

func TestWebhookSendImageNoURL(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	result, _ := a.SendImage(context.Background(), "chat", "http://img", "cap", nil)
	if result.Success {
		t.Error("expected Success = false when WEBHOOK_SEND_URL is empty")
	}
}

func TestWebhookSendToServer(t *testing.T) {
	t.Parallel()

	receivedBody := make(map[string]any)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer mysendsecret" {
			t.Errorf("Authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer mysendsecret")
		}
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	a := NewWebhookAdapter(nil)
	a.sendURL = server.URL
	a.sendSecret = "mysendsecret"

	result, err := a.Send(context.Background(), "chat1", "test message", nil)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !result.Success {
		t.Errorf("expected Success = true, got Error = %q", result.Error)
	}
	if receivedBody["text"] != "test message" {
		t.Errorf("text = %v, want %q", receivedBody["text"], "test message")
	}
	if receivedBody["chat_id"] != "chat1" {
		t.Errorf("chat_id = %v, want %q", receivedBody["chat_id"], "chat1")
	}
}

func TestWebhookSendServerReturnsError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	a := NewWebhookAdapter(nil)
	a.sendURL = server.URL

	result, _ := a.Send(context.Background(), "chat", "msg", nil)
	if result.Success {
		t.Error("expected Success = false for server error")
	}
}

// ---------------------------------------------------------------------------
// Webhook adapter -- EditMessage / DeleteMessage (unsupported)
// ---------------------------------------------------------------------------

func TestWebhookEditMessageReturnsError(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	result, _ := a.EditMessage(context.Background(), "chat", "msg", "text")
	if result.Success {
		t.Error("expected Success = false")
	}
}

func TestWebhookDeleteMessageReturnsError(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	err := a.DeleteMessage(context.Background(), "chat", "msg")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// Webhook adapter -- unsupported media
// ---------------------------------------------------------------------------

func TestWebhookSendVoiceNotSupported(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	result, _ := a.SendVoice(context.Background(), "chat", "/path", nil)
	if result.Success {
		t.Error("expected Success = false")
	}
}

func TestWebhookSendVideoNotSupported(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	result, _ := a.SendVideo(context.Background(), "chat", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false")
	}
}

func TestWebhookSendDocumentNotSupported(t *testing.T) {
	t.Parallel()

	a := NewWebhookAdapter(nil)
	result, _ := a.SendDocument(context.Background(), "chat", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false")
	}
}
