package platforms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------------------------------------------------------------------------
// API Server adapter -- constructor
// ---------------------------------------------------------------------------

func TestNewAPIServerAdapter(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	if a == nil {
		t.Fatal("NewAPIServerAdapter() returned nil")
	}
	if a.port != apiServerDefaultPort {
		t.Errorf("port = %d, want %d", a.port, apiServerDefaultPort)
	}
	if a.pendingResponses == nil {
		t.Error("pendingResponses should be initialized")
	}
}

// ---------------------------------------------------------------------------
// API Server adapter -- interface compliance
// ---------------------------------------------------------------------------

func TestAPIServerAdapterImplementsPlatformAdapter(t *testing.T) {
	t.Parallel()

	var _ PlatformAdapter = (*APIServerAdapter)(nil)
}

// ---------------------------------------------------------------------------
// API Server adapter -- property accessors
// ---------------------------------------------------------------------------

func TestAPIServerAdapterProperties(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	if a.Name() != "API Server" {
		t.Errorf("Name() = %q, want %q", a.Name(), "API Server")
	}
	if a.PlatformType() != PlatformAPIServer {
		t.Errorf("PlatformType() = %q, want %q", a.PlatformType(), PlatformAPIServer)
	}
	if a.MaxMessageLength() != 128000 {
		t.Errorf("MaxMessageLength() = %d, want 128000", a.MaxMessageLength())
	}
	if !a.SupportsStreaming() {
		t.Error("SupportsStreaming() = false, want true")
	}
}

// ---------------------------------------------------------------------------
// API Server adapter -- Send / EditMessage / DeleteMessage / SendTyping
// ---------------------------------------------------------------------------

func TestAPIServerSendDeliversToPendingResponse(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	ch := make(chan string, 1)
	a.pendingResponses["req_123"] = ch

	result, err := a.Send(context.Background(), "req_123", "hello response", nil)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !result.Success {
		t.Error("expected Success = true")
	}
	if result.MessageID != "req_123" {
		t.Errorf("MessageID = %q, want %q", result.MessageID, "req_123")
	}

	select {
	case msg := <-ch:
		if msg != "hello response" {
			t.Errorf("pending response = %q, want %q", msg, "hello response")
		}
	default:
		t.Fatal("expected response on pending channel")
	}
}

func TestAPIServerSendNoPendingResponse(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	result, err := a.Send(context.Background(), "unknown_id", "text", nil)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !result.Success {
		t.Error("expected Success = true even with no pending response")
	}
}

func TestAPIServerEditMessageDelegatesToSend(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	ch := make(chan string, 1)
	a.pendingResponses["req_edit"] = ch

	result, err := a.EditMessage(context.Background(), "req_edit", "msg", "edited text")
	if err != nil {
		t.Fatalf("EditMessage() error = %v", err)
	}
	if !result.Success {
		t.Error("expected Success = true")
	}

	select {
	case msg := <-ch:
		if msg != "edited text" {
			t.Errorf("pending response = %q, want %q", msg, "edited text")
		}
	default:
		t.Fatal("expected response on pending channel")
	}
}

func TestAPIServerDeleteMessageNoop(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	err := a.DeleteMessage(context.Background(), "chat", "msg")
	if err != nil {
		t.Errorf("DeleteMessage should be nil, got %v", err)
	}
}

func TestAPIServerSendTypingNoop(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	if err := a.SendTyping(context.Background(), "chat"); err != nil {
		t.Errorf("SendTyping should be nil, got %v", err)
	}
}

func TestAPIServerSendImage(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	ch := make(chan string, 1)
	a.pendingResponses["req_img"] = ch

	result, err := a.SendImage(context.Background(), "req_img", "http://img", "caption", nil)
	if err != nil {
		t.Fatalf("SendImage() error = %v", err)
	}
	if !result.Success {
		t.Error("expected Success = true")
	}

	select {
	case msg := <-ch:
		if msg != "caption" {
			t.Errorf("pending response = %q, want %q", msg, "caption")
		}
	default:
		t.Fatal("expected response on pending channel")
	}
}

func TestAPIServerSendVoiceUnsupported(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	result, _ := a.SendVoice(context.Background(), "chat", "/path", nil)
	if result.Success {
		t.Error("expected Success = false for SendVoice")
	}
}

func TestAPIServerSendVideoUnsupported(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	result, _ := a.SendVideo(context.Background(), "chat", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false for SendVideo")
	}
}

func TestAPIServerSendDocumentUnsupported(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	result, _ := a.SendDocument(context.Background(), "chat", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false for SendDocument")
	}
}

// ---------------------------------------------------------------------------
// API Server adapter -- handleListModels
// ---------------------------------------------------------------------------

func TestAPIServerHandleListModels(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	a.handleListModels(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON parse error: %v", err)
	}
	if resp["object"] != "list" {
		t.Errorf("object = %v, want list", resp["object"])
	}
}

// ---------------------------------------------------------------------------
// API Server adapter -- handleHealth
// ---------------------------------------------------------------------------

func TestAPIServerHandleHealth(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	a.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON parse error: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want %q", resp["status"], "ok")
	}
}

// ---------------------------------------------------------------------------
// API Server adapter -- generateChunkID
// ---------------------------------------------------------------------------

func TestGenerateChunkID(t *testing.T) {
	t.Parallel()

	ids := make(map[string]bool)
	for i := 0; i < 50; i++ {
		id := generateChunkID()
		if id == "" {
			t.Fatal("generateChunkID returned empty string")
		}
		if ids[id] {
			t.Fatalf("duplicate chunk ID: %s", id)
		}
		ids[id] = true
	}
}

// ---------------------------------------------------------------------------
// API Server adapter -- constants
// ---------------------------------------------------------------------------

func TestAPIServerConstants(t *testing.T) {
	t.Parallel()

	if apiServerDefaultPort != 8081 {
		t.Errorf("apiServerDefaultPort = %d, want 8081", apiServerDefaultPort)
	}
}
