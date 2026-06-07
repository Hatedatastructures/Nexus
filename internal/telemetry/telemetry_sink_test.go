package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── Event type tests ───

func TestEventTypeConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		got  EventType
		want string
	}{
		{EventHTTPStarted, "HttpRequestStarted"},
		{EventHTTPSucceeded, "HttpRequestSucceeded"},
		{EventHTTPFailed, "HttpRequestFailed"},
		{EventTurnStarted, "TurnStarted"},
		{EventTurnCompleted, "TurnCompleted"},
		{EventTurnFailed, "TurnFailed"},
		{EventToolStarted, "ToolExecutionStarted"},
		{EventToolFinished, "ToolExecutionFinished"},
		{EventCompTriggered, "CompressionTriggered"},
		{EventCompCompleted, "CompressionCompleted"},
	}

	for _, tc := range tests {
		if string(tc.got) != tc.want {
			t.Errorf("EventType = %q, want %q", tc.got, tc.want)
		}
	}
}

// ─── JsonlTelemetrySink tests ───

func TestNewJsonlTelemetrySink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.jsonl")

	sink, err := NewJsonlTelemetrySink(path)
	if err != nil {
		t.Fatalf("NewJsonlTelemetrySink() error = %v", err)
	}
	defer func() { _ = sink.Close() }()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("sink file was not created")
	}
}

func TestNewJsonlTelemetrySink_CreatesDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "telemetry.jsonl")

	sink, err := NewJsonlTelemetrySink(path)
	if err != nil {
		t.Fatalf("NewJsonlTelemetrySink() error = %v", err)
	}
	defer func() { _ = sink.Close() }()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("sink file was not created in nested directory")
	}
}

func TestJsonlTelemetrySink_Record(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.jsonl")

	sink, err := NewJsonlTelemetrySink(path)
	if err != nil {
		t.Fatalf("NewJsonlTelemetrySink() error = %v", err)
	}

	event := &Event{
		Type:      EventTurnStarted,
		SessionID: "sess-123",
	}

	sink.Record(event)

	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if len(data) == 0 {
		t.Fatal("sink file is empty after Record()")
	}

	var parsed Event
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if parsed.Type != EventTurnStarted {
		t.Errorf("Type = %q, want %q", parsed.Type, EventTurnStarted)
	}
	if parsed.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want %q", parsed.SessionID, "sess-123")
	}
	if parsed.Timestamp == 0 {
		t.Error("Timestamp should be auto-filled when zero")
	}
}

func TestJsonlTelemetrySink_RecordWithExistingTimestamp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.jsonl")

	sink, err := NewJsonlTelemetrySink(path)
	if err != nil {
		t.Fatalf("NewJsonlTelemetrySink() error = %v", err)
	}

	ts := int64(1700000000000)
	event := &Event{
		Type:      EventTurnCompleted,
		Timestamp: ts,
	}

	sink.Record(event)
	_ = sink.Close()

	data, _ := os.ReadFile(path)
	var parsed Event
	_ = json.Unmarshal(data, &parsed)

	if parsed.Timestamp != ts {
		t.Errorf("Timestamp = %d, want %d (should not be overwritten)", parsed.Timestamp, ts)
	}
}

func TestJsonlTelemetrySink_MultipleRecords(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.jsonl")

	sink, err := NewJsonlTelemetrySink(path)
	if err != nil {
		t.Fatalf("NewJsonlTelemetrySink() error = %v", err)
	}

	for i := 0; i < 5; i++ {
		sink.Record(&Event{
			Type:      EventToolStarted,
			SessionID: "multi",
			Data:      json.RawMessage(`{"step":0}`),
		})
	}
	_ = sink.Close()

	if sink.Count() != 5 {
		t.Errorf("Count() = %d, want 5", sink.Count())
	}

	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 5 {
		t.Errorf("lines = %d, want 5", len(lines))
	}
}

func TestJsonlTelemetrySink_Count(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.jsonl")

	sink, err := NewJsonlTelemetrySink(path)
	if err != nil {
		t.Fatalf("NewJsonlTelemetrySink() error = %v", err)
	}
	defer func() { _ = sink.Close() }()

	if sink.Count() != 0 {
		t.Errorf("Count() = %d, want 0", sink.Count())
	}

	sink.Record(&Event{Type: EventTurnStarted})
	sink.Record(&Event{Type: EventTurnCompleted})

	if sink.Count() != 2 {
		t.Errorf("Count() = %d, want 2", sink.Count())
	}
}

func TestJsonlTelemetrySink_RecordWithAllFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.jsonl")

	sink, err := NewJsonlTelemetrySink(path)
	if err != nil {
		t.Fatalf("NewJsonlTelemetrySink() error = %v", err)
	}

	event := &Event{
		Type:      EventHTTPFailed,
		Timestamp: time.Now().UnixMilli(),
		SessionID: "sess-full",
		Duration:  1234,
		Data:      json.RawMessage(`{"url":"https://example.com"}`),
		Error:     "connection refused",
	}

	sink.Record(event)
	_ = sink.Close()

	data, _ := os.ReadFile(path)
	var parsed Event
	_ = json.Unmarshal(data, &parsed)

	if parsed.Duration != 1234 {
		t.Errorf("Duration = %d, want 1234", parsed.Duration)
	}
	if parsed.Error != "connection refused" {
		t.Errorf("Error = %q, want %q", parsed.Error, "connection refused")
	}
	if string(parsed.Data) != `{"url":"https://example.com"}` {
		t.Errorf("Data = %s", parsed.Data)
	}
}
