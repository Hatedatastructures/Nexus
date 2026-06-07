package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionPersister_RecordAndRead(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "test-session")

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}

	session := &Session{
		ID:     "test-session",
		Source: "cli",
	}
	if err := sp.RecordSessionMeta(session); err != nil {
		t.Fatalf("RecordSessionMeta: %v", err)
	}

	msg := &MessageRecord{
		SessionID: "test-session",
		Role:      "user",
		Content:   "test message",
	}
	if err := sp.RecordMessage(msg); err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}

	if err := sp.RecordCompaction(10, 500); err != nil {
		t.Fatalf("RecordCompaction: %v", err)
	}

	if err := sp.RecordPromptHistory("/help"); err != nil {
		t.Fatalf("RecordPromptHistory: %v", err)
	}

	if err := sp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify file exists and has content
	path := filepath.Join(dir, "test-session.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 {
		t.Errorf("expected 4 JSONL lines, got %d", len(lines))
	}
}



func TestSessionPersister_WriteWithoutOpen(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "unopened")

	err := sp.RecordSessionMeta(&Session{ID: "test"})
	if err == nil {
		t.Error("should error when writing without open")
	}
}



func TestSessionPersister_DoubleClose(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "double-close")

	sp.Open()
	sp.Close()

	// Second close should not panic
	if err := sp.Close(); err != nil {
		t.Errorf("second Close should not error: %v", err)
	}
}



func TestSessionPersister_Append(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "append-test")

	// First write
	sp.Open()
	sp.RecordSessionMeta(&Session{ID: "append-test"})
	sp.Close()

	// Second write — should append
	sp2 := NewSessionPersister(dir, "append-test")
	sp2.Open()
	sp2.RecordMessage(&MessageRecord{SessionID: "append-test", Role: "user", Content: "second"})
	sp2.Close()

	data, _ := os.ReadFile(filepath.Join(dir, "append-test.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines after append, got %d", len(lines))
	}
}



func TestSessionPersister_Rotation(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rotate-test")
	sp.maxSize = 100 // very small to trigger rotation
	sp.maxRotations = 2

	sp.Open()

	// Write enough data to trigger rotation
	for i := 0; i < 50; i++ {
		msg := &MessageRecord{
			SessionID: "rotate-test",
			Role:      "user",
			Content:   fmt.Sprintf("message number %d with padding to exceed size threshold", i),
		}
		if err := sp.RecordMessage(msg); err != nil {
			t.Fatalf("RecordMessage %d: %v", i, err)
		}
	}
	sp.Close()

	// Check that rotation files exist
	if _, err := os.Stat(filepath.Join(dir, "rotate-test.jsonl")); err != nil {
		t.Errorf("active file missing: %v", err)
	}
	// At least one rotation file should exist
	found := false
	for i := 1; i <= 3; i++ {
		p := filepath.Join(dir, fmt.Sprintf("rotate-test.%d.jsonl", i))
		if _, err := os.Stat(p); err == nil {
			found = true
		}
	}
	if !found {
		t.Error("expected at least one rotation file")
	}
}

// ── Migration 测试 ─────────────────────────────────────────────



func TestSessionPersister_OpenClose(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "test-session")

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := sp.RecordSessionMeta(&Session{ID: "test-session", Source: "test"}); err != nil {
		t.Fatalf("RecordSessionMeta: %v", err)
	}

	if err := sp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(filepath.Join(dir, "test-session.jsonl")); os.IsNotExist(err) {
		t.Error("JSONL file should exist after Close")
	}
}



func TestSessionPersister_WriteNotOpen(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "not-open")

	err := sp.RecordMessage(&MessageRecord{Role: "user", Content: "test"})
	if err == nil {
		t.Error("expected error when writing to unopened persister")
	}
}



func TestSessionPersister_CompactionAndPrompt(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "records-test")

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sp.Close()

	if err := sp.RecordCompaction(100, 500); err != nil {
		t.Fatalf("RecordCompaction: %v", err)
	}
	if err := sp.RecordPromptHistory("hello world"); err != nil {
		t.Fatalf("RecordPromptHistory: %v", err)
	}
}

// ── orphanChildren 测试 ─────────────────────────────────────────



func TestSessionPersister_BasicLifecycle(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPersister(dir, "lifecycle-test")

	if err := p.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := p.RecordSessionMeta(&Session{ID: "lifecycle-test", Source: "test"}); err != nil {
		t.Fatalf("RecordSessionMeta: %v", err)
	}
	if err := p.RecordMessage(&MessageRecord{Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}



func TestSessionPersister_OpenExistingFile(t *testing.T) {
	dir := t.TempDir()
	p1 := NewSessionPersister(dir, "existing-test")
	if err := p1.Open(); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := p1.RecordMessage(&MessageRecord{Role: "user", Content: "first"}); err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	if err := p1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Reopen should succeed
	p2 := NewSessionPersister(dir, "existing-test")
	if err := p2.Open(); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if err := p2.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}



func TestSessionPersister_WriteBeforeOpen(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPersister(dir, "no-open-test")
	// Writing without Open should not panic; it should be a no-op or error
	err := p.RecordMessage(&MessageRecord{Role: "user", Content: "no-open"})
	// We accept either error or nil, but not a panic
	_ = err
}



func TestSessionPersister_RotationTrigger(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPersister(dir, "rotation-test")
	p.maxSize = 200 // Small size to trigger rotation

	if err := p.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write enough data to trigger rotation
	for i := range 50 {
		err := p.RecordMessage(&MessageRecord{
			Role:      "user",
			Content:   fmt.Sprintf("message number %d with some padding content to fill space", i),
			Timestamp: float64(time.Now().Unix()),
		})
		if err != nil {
			t.Fatalf("RecordMessage %d: %v", i, err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Check that rotated files exist
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) < 2 {
		t.Errorf("expected rotation files, got %d entries", len(entries))
	}
}



func TestSessionPersister_WriteBeforeOpen_Error(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPersister(dir, "no-open-err")
	err := p.RecordMessage(&MessageRecord{Role: "user", Content: "no-open"})
	if err == nil {
		t.Error("expected error when writing before Open, got nil")
	}
	if !strings.Contains(err.Error(), "not open") {
		t.Errorf("error should mention not open, got: %v", err)
	}
}



func TestSessionPersister_RecordCompaction(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPersister(dir, "compact-test")
	if err := p.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := p.RecordCompaction(10, 500); err != nil {
		t.Fatalf("RecordCompaction: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}



func TestSessionPersister_RecordPromptHistory(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPersister(dir, "prompt-test")
	if err := p.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := p.RecordPromptHistory("hello world"); err != nil {
		t.Fatalf("RecordPromptHistory: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}



// TestSessionPersister_RotationMaxFiles 测试 rotation 删除最旧文件
func TestSessionPersister_RotationMaxFiles(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "maxrot-test")
	sp.maxSize = 100
	sp.maxRotations = 2

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write enough to trigger multiple rotations
	for i := range 100 {
		if err := sp.RecordMessage(&MessageRecord{
			Role:    "user",
			Content: fmt.Sprintf("rotation test message %d padding content to exceed threshold", i),
		}); err != nil {
			t.Fatalf("RecordMessage %d: %v", i, err)
		}
	}
	sp.Close()

	// Should have at most maxRotations + 1 files (current + rotated)
	entries, _ := os.ReadDir(dir)
	jsonlFiles := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			jsonlFiles++
		}
	}
	if jsonlFiles > sp.maxRotations+1 {
		t.Errorf("expected at most %d files, got %d", sp.maxRotations+1, jsonlFiles)
	}

	// Rotation file .2 should not exist (maxRotations=2 means .1 and .2, but .3 should be gone)
	if _, err := os.Stat(filepath.Join(dir, "maxrot-test.3.jsonl")); err == nil {
		t.Error("rotation file .3 should have been deleted (maxRotations=2)")
	}
}

// TestAutoPrune_WithMessages 测试删除会话时消息也被删除
