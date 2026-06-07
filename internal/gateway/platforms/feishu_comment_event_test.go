package platforms

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Feishu Comment adapter -- HandleEvent
// ---------------------------------------------------------------------------

func TestFeishuCommentHandleEventRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	a.msgCh = make(chan *MessageEvent, 100)

	req := httptest.NewRequest("POST", "/", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	a.HandleEvent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestFeishuCommentHandleEventWrongEventType(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	a.msgCh = make(chan *MessageEvent, 100)

	payload, _ := json.Marshal(map[string]any{
		"header": map[string]any{
			"event_type": "other.event.type",
			"event_id":   "evt123",
		},
		"event": map[string]any{
			"content": "test",
		},
	})

	req := httptest.NewRequest("POST", "/", strings.NewReader(string(payload)))
	w := httptest.NewRecorder()
	a.HandleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestFeishuCommentHandleEventEmptyContent(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	a.msgCh = make(chan *MessageEvent, 100)

	payload, _ := json.Marshal(map[string]any{
		"header": map[string]any{
			"event_type": "drive.file.comment",
			"event_id":   "evt123",
		},
		"event": map[string]any{
			"content": "",
		},
	})

	req := httptest.NewRequest("POST", "/", strings.NewReader(string(payload)))
	w := httptest.NewRecorder()
	a.HandleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestFeishuCommentHandleEventValidComment(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	a.msgCh = make(chan *MessageEvent, 100)

	payload, _ := json.Marshal(map[string]any{
		"header": map[string]any{
			"event_type": "drive.file.comment",
			"event_id":   "evt_valid",
			"token":      "vtoken",
			"app_id":     "app123",
		},
		"event": map[string]any{
			"comment_id": "cmt123",
			"file_token": "file456",
			"file_type":  "doc",
			"content":    "Nice document!",
			"user_id":    "user789",
			"user_name":  "TestUser",
		},
	})

	req := httptest.NewRequest("POST", "/", strings.NewReader(string(payload)))
	w := httptest.NewRecorder()
	a.HandleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	select {
	case event := <-a.msgCh:
		if event.Text != "Nice document!" {
			t.Errorf("Text = %q, want %q", event.Text, "Nice document!")
		}
		if event.MessageType != MsgText {
			t.Errorf("MessageType = %q, want %q", event.MessageType, MsgText)
		}
		if event.MessageID != "cmt123" {
			t.Errorf("MessageID = %q, want %q", event.MessageID, "cmt123")
		}
		if event.Source.Platform != PlatformFeishu {
			t.Errorf("Platform = %q, want %q", event.Source.Platform, PlatformFeishu)
		}
		if event.Source.ChatID != "file456:cmt123" {
			t.Errorf("ChatID = %q, want %q", event.Source.ChatID, "file456:cmt123")
		}
		if event.Source.UserID != "user789" {
			t.Errorf("UserID = %q, want %q", event.Source.UserID, "user789")
		}
	default:
		t.Fatal("expected message on channel")
	}
}

// ---------------------------------------------------------------------------
// Feishu Comment adapter -- verifyFeishuSignature
// ---------------------------------------------------------------------------

func TestVerifyFeishuSignature(t *testing.T) {
	t.Parallel()

	token := "mytoken123"
	timestamp := "1700000000"
	body := `{"test":"data"}`

	h := hmac.New(sha256.New, []byte(token))
	h.Write([]byte(timestamp))
	h.Write([]byte("\n"))
	h.Write([]byte(body))
	expectedSig := base64.StdEncoding.EncodeToString(h.Sum(nil))

	tests := []struct {
		name      string
		signature string
		expectOK  bool
	}{
		{"valid signature", expectedSig, true},
		{"invalid signature", "invalidsig", false},
		{"empty signature", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := verifyFeishuSignature(token, timestamp, body, tc.signature)
			if result != tc.expectOK {
				t.Errorf("verifyFeishuSignature() = %v, want %v", result, tc.expectOK)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Feishu Comment adapter -- HandleEvent signature verification
// ---------------------------------------------------------------------------

func TestFeishuCommentHandleEventMissingSignature(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	a.verificationToken = "mytoken"
	a.msgCh = make(chan *MessageEvent, 100)

	payload := `{"header":{"event_type":"drive.file.comment"},"event":{"content":"test"}}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(payload))
	w := httptest.NewRecorder()
	a.HandleEvent(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// ---------------------------------------------------------------------------
// Feishu Comment adapter -- FeishuCommentEvent struct
// ---------------------------------------------------------------------------

func TestFeishuCommentEventParsing(t *testing.T) {
	t.Parallel()

	raw := `{
		"schema": "2.0",
		"header": {
			"event_id": "evt_001",
			"event_type": "drive.file.comment",
			"token": "verify_token",
			"app_id": "cli_xxx"
		},
		"event": {
			"comment_id": "cmt_001",
			"file_token": "fldocxxx",
			"file_type": "doc",
			"content": "Great work!",
			"user_id": "uid123",
			"user_name": "Alice",
			"reply_msg_id": "reply001"
		}
	}`

	var event FeishuCommentEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if event.Schema != "2.0" {
		t.Errorf("Schema = %q, want %q", event.Schema, "2.0")
	}
	if event.Header.EventID != "evt_001" {
		t.Errorf("Header.EventID = %q, want %q", event.Header.EventID, "evt_001")
	}
	if event.Header.EventType != "drive.file.comment" {
		t.Errorf("Header.EventType = %q, want %q", event.Header.EventType, "drive.file.comment")
	}
	if event.Event.CommentID != "cmt_001" {
		t.Errorf("Event.CommentID = %q, want %q", event.Event.CommentID, "cmt_001")
	}
	if event.Event.FileToken != "fldocxxx" {
		t.Errorf("Event.FileToken = %q, want %q", event.Event.FileToken, "fldocxxx")
	}
	if event.Event.Content != "Great work!" {
		t.Errorf("Event.Content = %q, want %q", event.Event.Content, "Great work!")
	}
	if event.Event.UserID != "uid123" {
		t.Errorf("Event.UserID = %q, want %q", event.Event.UserID, "uid123")
	}
	if event.Event.UserName != "Alice" {
		t.Errorf("Event.UserName = %q, want %q", event.Event.UserName, "Alice")
	}
	if event.Event.ReplyMsgID != "reply001" {
		t.Errorf("Event.ReplyMsgID = %q, want %q", event.Event.ReplyMsgID, "reply001")
	}
}
