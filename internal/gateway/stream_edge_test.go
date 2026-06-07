package gateway

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// sendOrEdit
// ---------------------------------------------------------------------------

func TestStreamConsumer_sendOrEdit(t *testing.T) {
	t.Parallel()

	t.Run("first call uses Send", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: false}
		sc := NewStreamConsumer(adapter, "ch1", 5, time.Second)

		ok := sc.sendOrEdit(context.Background(), "hello")
		if !ok {
			t.Error("expected success")
		}
		if adapter.sendCount.Load() != 1 {
			t.Errorf("sendCount = %d, want 1", adapter.sendCount.Load())
		}
		if sc.currentMsgID != "m" {
			t.Errorf("currentMsgID = %q, want %q", sc.currentMsgID, "m")
		}
	})

	t.Run("subsequent call uses EditMessage when streaming", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "ch1", 5, time.Second)

		sc.sendOrEdit(context.Background(), "first")
		ok := sc.sendOrEdit(context.Background(), "first+more")
		if !ok {
			t.Error("expected success")
		}
		if adapter.editCount.Load() != 1 {
			t.Errorf("editCount = %d, want 1", adapter.editCount.Load())
		}
	})

	t.Run("non-streaming always uses Send", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: false}
		sc := NewStreamConsumer(adapter, "ch1", 5, time.Second)

		sc.sendOrEdit(context.Background(), "a")
		sc.sendOrEdit(context.Background(), "ab")
		sc.sendOrEdit(context.Background(), "abc")

		if adapter.sendCount.Load() != 3 {
			t.Errorf("sendCount = %d, want 3", adapter.sendCount.Load())
		}
		if adapter.editCount.Load() != 0 {
			t.Errorf("editCount = %d, want 0", adapter.editCount.Load())
		}
	})

	t.Run("send error returns false", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: false, sendErr: context.DeadlineExceeded}
		sc := NewStreamConsumer(adapter, "ch1", 5, time.Second)

		ok := sc.sendOrEdit(context.Background(), "fail")
		if ok {
			t.Error("expected false on send error")
		}
	})

	t.Run("edit error returns false", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true, editErr: context.DeadlineExceeded}
		sc := NewStreamConsumer(adapter, "ch1", 5, time.Second)

		sc.sendOrEdit(context.Background(), "ok")
		ok := sc.sendOrEdit(context.Background(), "fail")
		if ok {
			t.Error("expected false on edit error")
		}
	})
}

// ---------------------------------------------------------------------------
// tryDrainPending
// ---------------------------------------------------------------------------

func TestStreamConsumer_tryDrainPending(t *testing.T) {
	t.Parallel()

	t.Run("drains pending deltas", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "ch1", 5, time.Second)

		sc.OnDelta("a")
		sc.OnDelta("b")
		sc.OnDelta("c")

		drained := sc.tryDrainPending()
		if !drained {
			t.Error("expected drained = true")
		}
		if sc.buffer.String() != "abc" {
			t.Errorf("buffer = %q, want %q", sc.buffer.String(), "abc")
		}
	})

	t.Run("returns false when channel empty", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "ch1", 5, time.Second)

		drained := sc.tryDrainPending()
		if drained {
			t.Error("expected drained = false for empty channel")
		}
	})
}

// ---------------------------------------------------------------------------
// Finish — edge cases
// ---------------------------------------------------------------------------

func TestStreamConsumer_Finish_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("double finish is safe", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "ch1", 5, 1*time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go sc.Run(ctx)

		sc.OnDelta("data")
		time.Sleep(50 * time.Millisecond)

		id1 := sc.Finish(ctx)
		id2 := sc.Finish(ctx)
		if id1 == "" {
			t.Error("first finish returned empty ID")
		}
		if id2 == "" {
			t.Error("second finish returned empty ID")
		}
	})

	t.Run("finish with cancelled context returns empty", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "ch1", 5, 1*time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		go sc.Run(ctx)

		cancel()
		time.Sleep(50 * time.Millisecond)

		finishCtx, finishCancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer finishCancel()
		time.Sleep(2 * time.Millisecond)

		id := sc.Finish(finishCtx)
		_ = id
	})

	t.Run("finish edits when currentMsgID set and streaming", func(t *testing.T) {
		t.Parallel()
		adapter := &mockStreamAdapter{streaming: true}
		sc := NewStreamConsumer(adapter, "ch1", 5, 1*time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go sc.Run(ctx)

		for i := 0; i < 10; i++ {
			sc.OnDelta("hello ")
		}
		time.Sleep(200 * time.Millisecond)

		msgID := sc.Finish(ctx)
		if msgID == "" {
			t.Error("expected non-empty msgID")
		}
		if adapter.editCount.Load() == 0 {
			t.Error("expected at least one edit")
		}
	})
}
