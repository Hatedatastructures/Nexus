package llm

import (
	"bytes"
	"context"
	"testing"
)

// ── ParseBinaryEventStream ──────────────────────────────────────────────────

func TestParseBinaryEventStream_SingleFrame(t *testing.T) {
	headers := map[string]string{
		":event-type":   "contentBlockDelta",
		":message-type": "event",
	}
	payload := []byte(`{"delta":{"text":"hello"}}`)
	frame := buildFrame(t, headers, payload)

	ch := ParseBinaryEventStream(context.Background(), bytes.NewReader(frame))

	var events []*SSEEvent
	for e := range ch {
		events = append(events, e)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Event != "contentBlockDelta" {
		t.Errorf("Event = %q, want contentBlockDelta", events[0].Event)
	}
	if events[0].Data != `{"delta":{"text":"hello"}}` {
		t.Errorf("Data = %q", events[0].Data)
	}
}

func TestParseBinaryEventStream_MultipleFrames(t *testing.T) {
	var allBytes bytes.Buffer

	for i := 0; i < 3; i++ {
		headers := map[string]string{
			":event-type":   "contentBlockDelta",
			":message-type": "event",
		}
		frame := buildFrame(t, headers, []byte("chunk"))
		allBytes.Write(frame)
	}

	ch := ParseBinaryEventStream(context.Background(), bytes.NewReader(allBytes.Bytes()))
	var count int
	for range ch {
		count++
	}
	if count != 3 {
		t.Errorf("expected 3 events, got %d", count)
	}
}

func TestParseBinaryEventStream_ErrorFrameSkipped(t *testing.T) {
	errHeaders := map[string]string{
		":message-type": "error",
		":error-code":   "ThrottlingException",
	}
	errFrame := buildFrame(t, errHeaders, []byte(`{"message":"too many requests"}`))

	okHeaders := map[string]string{
		":event-type":   "messageStart",
		":message-type": "event",
	}
	okFrame := buildFrame(t, okHeaders, []byte(`{"role":"assistant"}`))

	var buf bytes.Buffer
	buf.Write(errFrame)
	buf.Write(okFrame)

	ch := ParseBinaryEventStream(context.Background(), bytes.NewReader(buf.Bytes()))
	var events []*SSEEvent
	for e := range ch {
		events = append(events, e)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event (error frame skipped), got %d", len(events))
	}
	if events[0].Event != "messageStart" {
		t.Errorf("Event = %q, want messageStart", events[0].Event)
	}
}

func TestParseBinaryEventStream_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	headers := map[string]string{":event-type": "test", ":message-type": "event"}
	frame := buildFrame(t, headers, []byte("data"))

	ch := ParseBinaryEventStream(ctx, bytes.NewReader(frame))

	// Read the first event
	<-ch

	// Cancel context to stop the goroutine
	cancel()

	// Channel should close
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed after context cancel")
	}
}

func TestParseBinaryEventStream_EmptyReader(t *testing.T) {
	ch := ParseBinaryEventStream(context.Background(), bytes.NewReader(nil))

	var events []*SSEEvent
	for e := range ch {
		events = append(events, e)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for empty reader, got %d", len(events))
	}
}

func TestParseBinaryEventStream_InvalidFrameStops(t *testing.T) {
	// Send garbage data that will fail prelude CRC check
	ch := ParseBinaryEventStream(context.Background(), bytes.NewReader(make([]byte, 20)))

	var events []*SSEEvent
	for e := range ch {
		events = append(events, e)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for invalid data, got %d", len(events))
	}
}
