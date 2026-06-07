package state

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionPersist_CloseTwice(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "cl2-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	sp.Close()
	sp.Close()
}



func TestSessionPersist_WriteAfterClose(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "wac-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	sp.Close()
	err := sp.RecordMessage(&MessageRecord{SessionID: "wac-sess", Role: "user", Content: "after close"})
	if err == nil {
		t.Error("expected error writing to closed persister")
	}
}



func TestSessionPersist_RecordTypes(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rt-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	if err := sp.RecordCompaction(5, 100); err != nil {
		t.Fatalf("RecordCompaction failed: %v", err)
	}
	if err := sp.RecordPromptHistory("hello world"); err != nil {
		t.Fatalf("RecordPromptHistory failed: %v", err)
	}
}





func TestSessionPersister_RotationFiles(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rot-files")
	sp.maxSize = 256
	sp.maxRotations = 2
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	for i := 0; i < 300; i++ {
		sp.RecordMessage(&MessageRecord{SessionID: "rot-s", Role: "user", Content: "padding data to fill buffer beyond limit for rotation"})
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 files after rotation, got %d", len(entries))
	}
}



func TestSessionPersister_RecordSessionMeta(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "meta-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	sess := &Session{ID: "meta-sess", Source: "test", Title: "Meta Test"}
	if err := sp.RecordSessionMeta(sess); err != nil {
		t.Fatalf("RecordSessionMeta failed: %v", err)
	}
}



func TestSessionPersister_RotationWithFiles(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rotation-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}

	// Write messages - rotation is based on file size threshold
	for i := 0; i < 20; i++ {
		sp.RecordMessage(&MessageRecord{SessionID: "rotation-test", Role: "user", Content: fmt.Sprintf("message payload %03d with extra padding to fill up space", i)})
	}

	sp.Close()

	// Check that the main file exists
	mainFile := filepath.Join(dir, "rotation-test.jsonl")
	if _, err := os.Stat(mainFile); os.IsNotExist(err) {
		t.Error("main JSONL file should exist")
	}
}





func TestSessionPersister_RecordTypes(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rec-types")

	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}

	// Record all types
	sp.RecordSessionMeta(&Session{ID: "rec-session", Source: "test"})
	sp.RecordMessage(&MessageRecord{SessionID: "rec-session", Role: "user", Content: "test message content"})
	sp.RecordCompaction(5, 100)
	sp.RecordPromptHistory("user prompt text")

	sp.Close()
}




func TestSessionPersister_SmallRotation(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "small-rot")

	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}

	// Write enough data to trigger rotation with default size
	for i := 0; i < 100; i++ {
		sp.RecordMessage(&MessageRecord{
			SessionID: "small-rot",
			Role:      "user",
			Content:   fmt.Sprintf("padding message %d with extra content to fill up the file size quickly", i),
		})
	}

	sp.Close()

	// Main file should exist
	mainFile := filepath.Join(dir, "small-rot.jsonl")
	if _, err := os.Stat(mainFile); os.IsNotExist(err) {
		t.Error("main JSONL file should exist")
	}
}



func TestSessionPersister_OpenStatErr(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "test-session")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	if err := sp.RecordSessionMeta(&Session{ID: "s1", Source: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := sp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	if sp.currentSize <= 0 {
		t.Fatal("expected currentSize > 0 after re-open")
	}
	sp.Close()
}



func TestRotateIfNeeded_SmallFile(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rotate-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	if err := sp.RecordPromptHistory("hello world"); err != nil {
		t.Fatal(err)
	}
	if sp.currentSize > defaultMaxSize {
		t.Fatal("file should be small, rotation should not have occurred")
	}
}
