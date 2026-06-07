package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ─── Global sink tests ───

func TestRecord_WithoutGlobalSink(t *testing.T) {
	t.Parallel()

	SetGlobalSink(nil)

	Record(&Event{Type: EventTurnStarted})
}

func TestGlobalSink_RecordAndClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "global.jsonl")

	sink, err := NewJsonlTelemetrySink(path)
	if err != nil {
		t.Fatalf("NewJsonlTelemetrySink() error = %v", err)
	}

	SetGlobalSink(sink)

	Record(&Event{Type: EventTurnStarted, SessionID: "g1"})
	Record(&Event{Type: EventTurnCompleted, SessionID: "g1"})

	if err := CloseGlobal(); err != nil {
		t.Fatalf("CloseGlobal() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2\ndata:\n%s", len(lines), string(data))
	}

	SetGlobalSink(nil)
}

func TestRecordSimple(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "simple.jsonl")

	sink, err := NewJsonlTelemetrySink(path)
	if err != nil {
		t.Fatalf("NewJsonlTelemetrySink() error = %v", err)
	}

	SetGlobalSink(sink)

	RecordSimple(EventToolStarted, "sess-simple", map[string]string{"tool": "bash"})

	_ = CloseGlobal()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(data) == 0 {
		t.Fatal("data is empty, nothing was recorded")
	}
	var parsed Event
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if parsed.Type != EventToolStarted {
		t.Errorf("Type = %q, want %q", parsed.Type, EventToolStarted)
	}
	if parsed.SessionID != "sess-simple" {
		t.Errorf("SessionID = %q", parsed.SessionID)
	}
	if len(parsed.Data) == 0 {
		t.Error("Data is empty")
	}

	SetGlobalSink(nil)
}

func TestRecordSimple_NilData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nil_data.jsonl")

	sink, err := NewJsonlTelemetrySink(path)
	if err != nil {
		t.Fatalf("NewJsonlTelemetrySink() error = %v", err)
	}

	SetGlobalSink(sink)

	RecordSimple(EventTurnStarted, "sess-nil", nil)

	_ = CloseGlobal()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var parsed Event
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if parsed.Type != EventTurnStarted {
		t.Errorf("Type = %q", parsed.Type)
	}

	SetGlobalSink(nil)
}

func TestCloseGlobal_WhenNil(t *testing.T) {
	t.Parallel()

	SetGlobalSink(nil)

	err := CloseGlobal()
	if err != nil {
		t.Errorf("CloseGlobal() with nil sink error = %v", err)
	}
}

func TestCloseGlobal_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "idem.jsonl")

	sink, _ := NewJsonlTelemetrySink(path)
	SetGlobalSink(sink)

	if err := CloseGlobal(); err != nil {
		t.Fatalf("first CloseGlobal() error = %v", err)
	}
	if err := CloseGlobal(); err != nil {
		t.Fatalf("second CloseGlobal() error = %v", err)
	}
	SetGlobalSink(nil)
}

// ─── Concurrency test ───

func TestJsonlTelemetrySink_ConcurrentRecord(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.jsonl")

	sink, err := NewJsonlTelemetrySink(path)
	if err != nil {
		t.Fatalf("NewJsonlTelemetrySink() error = %v", err)
	}

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sink.Record(&Event{
				Type:      EventToolStarted,
				SessionID: "concurrent",
			})
		}(i)
	}
	wg.Wait()
	_ = sink.Close()

	if sink.Count() != 100 {
		t.Errorf("Count() = %d, want 100", sink.Count())
	}
}
