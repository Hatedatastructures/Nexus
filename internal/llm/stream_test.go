package llm

import (
	"context"
	"io"
	"strings"
	"testing"
)

type nopCloser struct {
	io.Reader
}

func (nopCloser) Close() error { return nil }

func TestParseSSEStream_SingleEvent(t *testing.T) {
	input := "data: hello world\n\n"
	body := nopCloser{Reader: strings.NewReader(input)}

	events := ParseSSEStream(context.Background(), body)
	var collected []string
	for e := range events {
		if e.Data != "" {
			collected = append(collected, e.Data)
		}
	}
	if len(collected) != 1 || collected[0] != "hello world" {
		t.Errorf("collected = %v, want [hello world]", collected)
	}
}

func TestParseSSEStream_MultipleEvents(t *testing.T) {
	input := "data: first\n\ndata: second\n\n"
	body := nopCloser{Reader: strings.NewReader(input)}

	events := ParseSSEStream(context.Background(), body)
	var collected []string
	for e := range events {
		if e.Data != "" {
			collected = append(collected, e.Data)
		}
	}
	if len(collected) != 2 {
		t.Fatalf("collected %d events, want 2", len(collected))
	}
	if collected[0] != "first" || collected[1] != "second" {
		t.Errorf("collected = %v, want [first, second]", collected)
	}
}

func TestParseSSEStream_MultiLineData(t *testing.T) {
	input := "data: line1\ndata: line2\n\n"
	body := nopCloser{Reader: strings.NewReader(input)}

	events := ParseSSEStream(context.Background(), body)
	var collected []string
	for e := range events {
		if e.Data != "" {
			collected = append(collected, e.Data)
		}
	}
	if len(collected) != 1 {
		t.Fatalf("collected %d events, want 1", len(collected))
	}
	if collected[0] != "line1\nline2" {
		t.Errorf("data = %q, want %q", collected[0], "line1\nline2")
	}
}

func TestParseSSEStream_DoneMarker(t *testing.T) {
	input := "data: content\n\ndata: [DONE]\n"
	body := nopCloser{Reader: strings.NewReader(input)}

	events := ParseSSEStream(context.Background(), body)
	var collected []string
	for e := range events {
		if e.Data != "" {
			collected = append(collected, e.Data)
		}
	}
	if len(collected) != 1 || collected[0] != "content" {
		t.Errorf("collected = %v, want [content] ([DONE] should not appear)", collected)
	}
}

func TestParseSSEStream_EventField(t *testing.T) {
	input := "event: message\ndata: payload\n\n"
	body := nopCloser{Reader: strings.NewReader(input)}

	events := ParseSSEStream(context.Background(), body)
	var eventTypes []string
	for e := range events {
		if e.Data != "" {
			eventTypes = append(eventTypes, e.Event)
		}
	}
	if len(eventTypes) != 1 || eventTypes[0] != "message" {
		t.Errorf("event types = %v, want [message]", eventTypes)
	}
}

func TestParseSSEStream_IDField(t *testing.T) {
	input := "id: 42\ndata: payload\n\n"
	body := nopCloser{Reader: strings.NewReader(input)}

	events := ParseSSEStream(context.Background(), body)
	for e := range events {
		if e.Data != "" {
			if e.ID != "42" {
				t.Errorf("ID = %q, want %q", e.ID, "42")
			}
			return
		}
	}
	t.Error("no events received")
}

func TestParseSSEStream_RetryField(t *testing.T) {
	input := "retry: 5000\ndata: payload\n\n"
	body := nopCloser{Reader: strings.NewReader(input)}

	events := ParseSSEStream(context.Background(), body)
	for e := range events {
		if e.Data != "" {
			if e.Retry != 5000 {
				t.Errorf("Retry = %d, want 5000", e.Retry)
			}
			return
		}
	}
	t.Error("no events received")
}

func TestParseSSEStream_RetryFieldCapped(t *testing.T) {
	input := "retry: 120000\ndata: payload\n\n"
	body := nopCloser{Reader: strings.NewReader(input)}

	events := ParseSSEStream(context.Background(), body)
	for e := range events {
		if e.Data != "" {
			if e.Retry != 60000 {
				t.Errorf("Retry = %d, want 60000 (capped)", e.Retry)
			}
			return
		}
	}
	t.Error("no events received")
}

func TestParseSSEStream_CommentIgnored(t *testing.T) {
	input := ": this is a comment\ndata: payload\n\n"
	body := nopCloser{Reader: strings.NewReader(input)}

	events := ParseSSEStream(context.Background(), body)
	var collected []string
	for e := range events {
		if e.Data != "" {
			collected = append(collected, e.Data)
		}
	}
	if len(collected) != 1 || collected[0] != "payload" {
		t.Errorf("collected = %v, want [payload]", collected)
	}
}

func TestParseSSEStream_InvalidLineIgnored(t *testing.T) {
	input := "invalidline\ndata: payload\n\n"
	body := nopCloser{Reader: strings.NewReader(input)}

	events := ParseSSEStream(context.Background(), body)
	var collected []string
	for e := range events {
		if e.Data != "" {
			collected = append(collected, e.Data)
		}
	}
	if len(collected) != 1 || collected[0] != "payload" {
		t.Errorf("collected = %v, want [payload]", collected)
	}
}

func TestParseSSEStream_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	input := "data: line1\n\ndata: line2\n\ndata: line3\n\n"
	body := nopCloser{Reader: strings.NewReader(input)}

	cancel()

	events := ParseSSEStream(ctx, body)
	count := 0
	for range events {
		count++
	}
	if count > 3 {
		t.Errorf("got %d events, expected cancellation to stop processing", count)
	}
}

func TestParseSSEStream_TrailingDataNoNewline(t *testing.T) {
	input := "data: last"
	body := nopCloser{Reader: strings.NewReader(input)}

	events := ParseSSEStream(context.Background(), body)
	var collected []string
	for e := range events {
		if e.Data != "" {
			collected = append(collected, e.Data)
		}
	}
	if len(collected) != 1 || collected[0] != "last" {
		t.Errorf("collected = %v, want [last] (trailing data without blank line)", collected)
	}
}

func TestReadSSEStream(t *testing.T) {
	input := "data: hello\ndata: world\n\n"
	body := nopCloser{Reader: strings.NewReader(input)}

	result, err := ReadSSEStream(context.Background(), body)
	if err != nil {
		t.Fatalf("ReadSSEStream error: %v", err)
	}
	if result != "hello\nworld" {
		t.Errorf("result = %q, want %q", result, "hello\nworld")
	}
}
