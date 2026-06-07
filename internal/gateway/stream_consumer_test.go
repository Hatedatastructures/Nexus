package gateway

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nexus-agent/internal/gateway/platforms"
)

// ---------------------------------------------------------------------------
// mockStreamAdapter — records calls for stream tests
// ---------------------------------------------------------------------------

type mockStreamAdapter struct {
	sendCount atomic.Int32
	editCount atomic.Int32
	lastText  atomic.Pointer[string]
	streaming bool
	sendErr   error
	editErr   error
}

func (m *mockStreamAdapter) Name() string { return "mockStream" }
func (m *mockStreamAdapter) PlatformType() platforms.Platform {
	return platforms.PlatformTelegram
}
func (m *mockStreamAdapter) Connect(_ context.Context) (<-chan *platforms.MessageEvent, error) {
	return nil, nil
}
func (m *mockStreamAdapter) Disconnect(_ context.Context) error { return nil }
func (m *mockStreamAdapter) Send(_ context.Context, _ string, text string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	m.sendCount.Add(1)
	cp := text
	m.lastText.Store(&cp)
	idx := m.sendCount.Load()
	msgID := strings.Repeat("m", int(idx))
	return &platforms.SendResult{Success: true, MessageID: msgID}, nil
}
func (m *mockStreamAdapter) EditMessage(_ context.Context, _ string, _ string, text string) (*platforms.SendResult, error) {
	if m.editErr != nil {
		return nil, m.editErr
	}
	m.editCount.Add(1)
	cp := text
	m.lastText.Store(&cp)
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockStreamAdapter) DeleteMessage(_ context.Context, _ string, _ string) error { return nil }
func (m *mockStreamAdapter) SendTyping(_ context.Context, _ string) error              { return nil }
func (m *mockStreamAdapter) SendImage(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "img"}, nil
}
func (m *mockStreamAdapter) SendVoice(_ context.Context, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "voice"}, nil
}
func (m *mockStreamAdapter) SendVideo(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "vid"}, nil
}
func (m *mockStreamAdapter) SendDocument(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "doc"}, nil
}
func (m *mockStreamAdapter) MaxMessageLength() int   { return 4096 }
func (m *mockStreamAdapter) SupportsStreaming() bool { return m.streaming }

// ---------------------------------------------------------------------------
// NewStreamConsumer
// ---------------------------------------------------------------------------

func TestNewStreamConsumer(t *testing.T) {
	t.Parallel()

	t.Run("applies defaults when zero", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "chat1", 0, 0)
		if sc.bufferSize != 40 {
			t.Errorf("bufferSize = %d, want 40", sc.bufferSize)
		}
		if sc.editInterval != time.Second {
			t.Errorf("editInterval = %v, want 1s", sc.editInterval)
		}
	})

	t.Run("uses provided values", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "chat1", 100, 500*time.Millisecond)
		if sc.bufferSize != 100 {
			t.Errorf("bufferSize = %d, want 100", sc.bufferSize)
		}
		if sc.editInterval != 500*time.Millisecond {
			t.Errorf("editInterval = %v, want 500ms", sc.editInterval)
		}
	})
}

// ---------------------------------------------------------------------------
// OnDelta
// ---------------------------------------------------------------------------

func TestStreamConsumer_OnDelta(t *testing.T) {
	t.Parallel()

	t.Run("pushes delta to channel", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "ch1", 10, 10*time.Millisecond)
		sc.OnDelta("hello")
		select {
		case d := <-sc.deltaCh:
			if d != "hello" {
				t.Errorf("got %q, want %q", d, "hello")
			}
		default:
			t.Error("expected delta in channel")
		}
	})

	t.Run("ignores empty text", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "ch1", 10, 10*time.Millisecond)
		sc.OnDelta("")
		if len(sc.deltaCh) != 0 {
			t.Error("expected empty channel after empty delta")
		}
	})

	t.Run("ignores after finish", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "ch1", 10, 10*time.Millisecond)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go sc.Run(ctx)
		sc.Finish(ctx)

		sc.OnDelta("after finish")
		if len(sc.deltaCh) != 0 {
			t.Error("expected no delta after finish")
		}
	})
}

// ---------------------------------------------------------------------------
// Run + Finish integration
// ---------------------------------------------------------------------------

func TestStreamConsumer_RunAndFinish(t *testing.T) {
	t.Parallel()

	t.Run("sends when buffer threshold reached", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: false}
		sc := NewStreamConsumer(adapter, "ch1", 5, 1*time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go sc.Run(ctx)

		sc.OnDelta("hello world")
		time.Sleep(100 * time.Millisecond)

		msgID := sc.Finish(ctx)
		if msgID == "" {
			t.Error("expected non-empty message ID")
		}
		if adapter.sendCount.Load() == 0 {
			t.Error("expected at least one send call")
		}
	})

	t.Run("first send then edit when streaming supported", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "ch1", 5, 1*time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go sc.Run(ctx)

		sc.OnDelta("hello")
		time.Sleep(100 * time.Millisecond)

		sc.OnDelta(" world")
		time.Sleep(100 * time.Millisecond)

		sc.Finish(ctx)

		if adapter.sendCount.Load() != 1 {
			t.Errorf("sendCount = %d, want 1", adapter.sendCount.Load())
		}
		if adapter.editCount.Load() == 0 {
			t.Error("expected at least one edit call")
		}
	})

	t.Run("finish with empty buffer returns empty", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "ch1", 5, 1*time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go sc.Run(ctx)

		msgID := sc.Finish(ctx)
		if msgID != "" {
			t.Errorf("expected empty msgID, got %q", msgID)
		}
	})

	t.Run("finish sends new message when no streaming", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: false}
		sc := NewStreamConsumer(adapter, "ch1", 999, 1*time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go sc.Run(ctx)

		sc.OnDelta("final content")
		time.Sleep(50 * time.Millisecond)

		msgID := sc.Finish(ctx)
		if msgID == "" {
			t.Error("expected non-empty msgID")
		}
	})

	t.Run("context cancellation stops run", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "ch1", 5, 1*time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		go sc.Run(ctx)

		cancel()
		time.Sleep(50 * time.Millisecond)

		finishCtx, finishCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer finishCancel()
		sc.Finish(finishCtx)
	})
}
