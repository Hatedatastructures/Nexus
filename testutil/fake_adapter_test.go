package testutil

import (
	"context"
	"fmt"
	"testing"

	"nexus-agent/internal/gateway/platforms"
)

func TestFakeAdapterName(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		f := &FakeAdapter{}
		if f.Name() != "FakeAdapter" {
			t.Errorf("Name() = %q, want %q", f.Name(), "FakeAdapter")
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		f := &FakeAdapter{AdapterName: "SlackAdapter"}
		if f.Name() != "SlackAdapter" {
			t.Errorf("Name() = %q, want %q", f.Name(), "SlackAdapter")
		}
	})
}

func TestFakeAdapterPlatformType(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		f := &FakeAdapter{}
		if f.PlatformType() != platforms.PlatformLocal {
			t.Errorf("PlatformType() = %v, want %v", f.PlatformType(), platforms.PlatformLocal)
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		f := &FakeAdapter{Platform: platforms.PlatformTelegram}
		if f.PlatformType() != platforms.PlatformTelegram {
			t.Errorf("PlatformType() = %v, want %v", f.PlatformType(), platforms.PlatformTelegram)
		}
	})
}

func TestFakeAdapterConnect(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		f := &FakeAdapter{}
		ch, err := f.Connect(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ch == nil {
			t.Fatal("expected non-nil channel")
		}
		if !f.Connected {
			t.Error("Connected should be true")
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()
		f := &FakeAdapter{ConnectError: fmt.Errorf("connection failed")}
		_, err := f.Connect(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("with preset channel", func(t *testing.T) {
		t.Parallel()
		ch := make(chan *platforms.MessageEvent, 5)
		f := &FakeAdapter{ConnectChannel: ch}
		gotCh, err := f.Connect(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotCh != ch {
			t.Error("expected preset channel to be returned")
		}
	})
}

func TestFakeAdapterDisconnect(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		f := &FakeAdapter{}
		err := f.Disconnect(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !f.Disconnected {
			t.Error("Disconnected should be true")
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()
		f := &FakeAdapter{DisconnectError: fmt.Errorf("disconnect failed")}
		err := f.Disconnect(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestFakeAdapterSend(t *testing.T) {
	t.Parallel()

	f := &FakeAdapter{}
	result, err := f.Send(context.Background(), "chat123", "hello", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success = true")
	}
	if len(f.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1", len(f.Messages))
	}
	if f.Messages[0].Method != "Send" {
		t.Errorf("Method = %q, want %q", f.Messages[0].Method, "Send")
	}
	if f.Messages[0].ChatID != "chat123" {
		t.Errorf("ChatID = %q, want %q", f.Messages[0].ChatID, "chat123")
	}
	if f.Messages[0].Content != "hello" {
		t.Errorf("Content = %q, want %q", f.Messages[0].Content, "hello")
	}
}

func TestFakeAdapterSendWithPresetError(t *testing.T) {
	t.Parallel()
	f := &FakeAdapter{SendError: fmt.Errorf("send failed")}
	_, err := f.Send(context.Background(), "chat1", "msg", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFakeAdapterEditMessage(t *testing.T) {
	t.Parallel()
	f := &FakeAdapter{}
	result, err := f.EditMessage(context.Background(), "chat1", "msg1", "edited")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success = true")
	}
	last, _ := f.LastMessage()
	if last.Method != "EditMessage" {
		t.Errorf("Method = %q, want %q", last.Method, "EditMessage")
	}
}

func TestFakeAdapterDeleteMessage(t *testing.T) {
	t.Parallel()
	f := &FakeAdapter{}
	err := f.DeleteMessage(context.Background(), "chat1", "msg1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last, _ := f.LastMessage()
	if last.Method != "DeleteMessage" {
		t.Errorf("Method = %q, want %q", last.Method, "DeleteMessage")
	}
	if last.Extra["message_id"] != "msg1" {
		t.Errorf("message_id = %v, want %q", last.Extra["message_id"], "msg1")
	}
}

func TestFakeAdapterSendTyping(t *testing.T) {
	t.Parallel()
	f := &FakeAdapter{}
	err := f.SendTyping(context.Background(), "chat1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last, _ := f.LastMessage()
	if last.Method != "SendTyping" {
		t.Errorf("Method = %q, want %q", last.Method, "SendTyping")
	}
}

func TestFakeAdapterSendImage(t *testing.T) {
	t.Parallel()
	f := &FakeAdapter{}
	result, err := f.SendImage(context.Background(), "chat1", "http://img.png", "caption", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success = true")
	}
	last, _ := f.LastMessage()
	if last.Method != "SendImage" {
		t.Errorf("Method = %q, want %q", last.Method, "SendImage")
	}
}

func TestFakeAdapterSendVoice(t *testing.T) {
	t.Parallel()
	f := &FakeAdapter{}
	_, err := f.SendVoice(context.Background(), "chat1", "/audio/file.ogg", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last, _ := f.LastMessage()
	if last.Method != "SendVoice" {
		t.Errorf("Method = %q, want %q", last.Method, "SendVoice")
	}
}

func TestFakeAdapterSendVideo(t *testing.T) {
	t.Parallel()
	f := &FakeAdapter{}
	_, err := f.SendVideo(context.Background(), "chat1", "/video/file.mp4", "video caption", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last, _ := f.LastMessage()
	if last.Method != "SendVideo" {
		t.Errorf("Method = %q, want %q", last.Method, "SendVideo")
	}
}

func TestFakeAdapterSendDocument(t *testing.T) {
	t.Parallel()
	f := &FakeAdapter{}
	_, err := f.SendDocument(context.Background(), "chat1", "/doc/file.pdf", "doc caption", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last, _ := f.LastMessage()
	if last.Method != "SendDocument" {
		t.Errorf("Method = %q, want %q", last.Method, "SendDocument")
	}
}

func TestFakeAdapterMaxMessageLength(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		f := &FakeAdapter{}
		if f.MaxMessageLength() != 4096 {
			t.Errorf("MaxMessageLength() = %d, want 4096", f.MaxMessageLength())
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		f := &FakeAdapter{MaxMsgLength: 2048}
		if f.MaxMessageLength() != 2048 {
			t.Errorf("MaxMessageLength() = %d, want 2048", f.MaxMessageLength())
		}
	})
}

func TestFakeAdapterSupportsStreaming(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		f := &FakeAdapter{}
		if f.SupportsStreaming() {
			t.Error("default SupportsStreaming() should be false")
		}
	})

	t.Run("enabled", func(t *testing.T) {
		t.Parallel()
		f := &FakeAdapter{StreamingEnabled: true}
		if !f.SupportsStreaming() {
			t.Error("SupportsStreaming() should be true when enabled")
		}
	})
}

func TestFakeAdapterReset(t *testing.T) {
	t.Parallel()
	f := &FakeAdapter{}
	_, _ = f.Send(context.Background(), "c", "m", nil)
	f.Reset()
	if len(f.Messages) != 0 {
		t.Error("Messages not cleared")
	}
	if f.Connected {
		t.Error("Connected should be false after Reset")
	}
	if f.Disconnected {
		t.Error("Disconnected should be false after Reset")
	}
}

func TestFakeAdapterMessagesByMethod(t *testing.T) {
	t.Parallel()
	f := &FakeAdapter{}
	_, _ = f.Send(context.Background(), "c1", "msg1", nil)
	_, _ = f.Send(context.Background(), "c2", "msg2", nil)
	_, _ = f.EditMessage(context.Background(), "c1", "m1", "edited")

	sendMsgs := f.MessagesByMethod("Send")
	if len(sendMsgs) != 2 {
		t.Errorf("MessagesByMethod(Send) = %d, want 2", len(sendMsgs))
	}
	editMsgs := f.MessagesByMethod("EditMessage")
	if len(editMsgs) != 1 {
		t.Errorf("MessagesByMethod(EditMessage) = %d, want 1", len(editMsgs))
	}
}

func TestFakeAdapterLastMessageEmpty(t *testing.T) {
	t.Parallel()
	f := &FakeAdapter{}
	_, err := f.LastMessage()
	if err == nil {
		t.Fatal("expected error when no messages")
	}
}

func TestFakeAdapterDefaultSendResult(t *testing.T) {
	t.Parallel()
	f := &FakeAdapter{}
	result := f.defaultSendResult()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Success {
		t.Error("expected Success = true")
	}

	// With preset result
	preset := &platforms.SendResult{Success: false, MessageID: "preset-id"}
	f2 := &FakeAdapter{SendResult: preset}
	if f2.defaultSendResult().MessageID != "preset-id" {
		t.Error("expected preset result to be used")
	}
}

func TestOptsToMap(t *testing.T) {
	t.Parallel()

	t.Run("nil opts", func(t *testing.T) {
		t.Parallel()
		m := optsToMap(nil)
		if m != nil {
			t.Errorf("expected nil for nil opts, got %v", m)
		}
	})

	t.Run("with values", func(t *testing.T) {
		t.Parallel()
		opts := &platforms.SendOptions{
			ReplyToMsgID: "msg1",
			ParseMode:    "markdown",
			Silent:       true,
		}
		m := optsToMap(opts)
		if m["reply_to"] != "msg1" {
			t.Errorf("reply_to = %v, want %q", m["reply_to"], "msg1")
		}
		if m["parse_mode"] != "markdown" {
			t.Errorf("parse_mode = %v, want %q", m["parse_mode"], "markdown")
		}
		if m["silent"] != true {
			t.Errorf("silent = %v, want true", m["silent"])
		}
	})
}
