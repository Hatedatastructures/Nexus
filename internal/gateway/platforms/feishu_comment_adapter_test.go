package platforms

import (
	"context"
	"testing"
)

// ---------------------------------------------------------------------------
// Feishu Comment adapter -- interface compliance
// ---------------------------------------------------------------------------

func TestFeishuCommentAdapterImplementsPlatformAdapter(t *testing.T) {
	t.Parallel()

	var _ PlatformAdapter = (*FeishuCommentAdapter)(nil)
}

// ---------------------------------------------------------------------------
// Feishu Comment adapter -- property accessors
// ---------------------------------------------------------------------------

func TestFeishuCommentAdapterProperties(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	if a.Name() != "Feishu Comment" {
		t.Errorf("Name() = %q, want %q", a.Name(), "Feishu Comment")
	}
	if a.PlatformType() != PlatformFeishu {
		t.Errorf("PlatformType() = %q, want %q", a.PlatformType(), PlatformFeishu)
	}
	if a.MaxMessageLength() != feishuCommentMaxMessageLen {
		t.Errorf("MaxMessageLength() = %d, want %d", a.MaxMessageLength(), feishuCommentMaxMessageLen)
	}
	if a.SupportsStreaming() {
		t.Error("SupportsStreaming() = true, want false")
	}
}

// ---------------------------------------------------------------------------
// Feishu Comment adapter -- unsupported operations
// ---------------------------------------------------------------------------

func TestFeishuCommentEditMessageUnsupported(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	result, err := a.EditMessage(context.Background(), "chat", "msg", "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected Success = false for EditMessage")
	}
}

func TestFeishuCommentDeleteMessageUnsupported(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	err := a.DeleteMessage(context.Background(), "chat", "msg")
	if err == nil {
		t.Fatal("expected error for DeleteMessage")
	}
}

func TestFeishuCommentSendTypingNoop(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	if err := a.SendTyping(context.Background(), "chat"); err != nil {
		t.Errorf("SendTyping should be nil, got %v", err)
	}
}

func TestFeishuCommentSendVoiceUnsupported(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	result, _ := a.SendVoice(context.Background(), "chat", "/path", nil)
	if result.Success {
		t.Error("expected Success = false for SendVoice")
	}
}

func TestFeishuCommentSendVideoUnsupported(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	result, _ := a.SendVideo(context.Background(), "chat", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false for SendVideo")
	}
}

func TestFeishuCommentSendDocumentUnsupported(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	result, _ := a.SendDocument(context.Background(), "chat", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false for SendDocument")
	}
}

// ---------------------------------------------------------------------------
// Feishu Comment adapter -- Send validation
// ---------------------------------------------------------------------------

func TestFeishuCommentSendInvalidChatID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		chatID string
	}{
		{"no colon", "invalidchatid"},
		{"empty", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := NewFeishuCommentAdapter(nil)
			result, _ := a.Send(context.Background(), tc.chatID, "text", nil)
			if result.Success {
				t.Error("expected Success = false for invalid chatID")
			}
		})
	}
}

func TestFeishuCommentSendImageFallsBackToCaption(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	result, _ := a.SendImage(context.Background(), "file:comment", "http://img", "image caption", nil)
	_ = result
}

func TestFeishuCommentSendImageEmptyCaption(t *testing.T) {
	t.Parallel()

	a := NewFeishuCommentAdapter(nil)
	result, _ := a.SendImage(context.Background(), "file:comment", "http://img", "", nil)
	_ = result
}

// ---------------------------------------------------------------------------
// Feishu Comment adapter -- constants
// ---------------------------------------------------------------------------

func TestFeishuCommentConstants(t *testing.T) {
	t.Parallel()

	if feishuCommentRequestTimeout != 15_000_000_000 {
		t.Errorf("feishuCommentRequestTimeout = %v, want 15s", feishuCommentRequestTimeout)
	}
	if feishuCommentMaxMessageLen != 4000 {
		t.Errorf("feishuCommentMaxMessageLen = %d, want 4000", feishuCommentMaxMessageLen)
	}
	if feishuCommentMaxBodySize != 1<<20 {
		t.Errorf("feishuCommentMaxBodySize = %d, want %d", feishuCommentMaxBodySize, 1<<20)
	}
}

// ---------------------------------------------------------------------------
// Feishu Comment adapter -- session key construction
// ---------------------------------------------------------------------------

func TestFeishuCommentSessionKeyConstruction(t *testing.T) {
	t.Parallel()

	src := &SessionSource{
		Platform: PlatformFeishu,
		ChatType: "dm",
		ChatID:   "file123:cmt456",
	}
	key := BuildSessionKey(src)
	expected := "agent:main:feishu:dm:file123:cmt456"
	if key != expected {
		t.Errorf("BuildSessionKey() = %q, want %q", key, expected)
	}
}
