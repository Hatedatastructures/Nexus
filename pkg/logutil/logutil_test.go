package logutil

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// ─── InitLogger tests ───

func TestInitLogger_TextFormat(t *testing.T) {
	t.Parallel()

	closeFn := InitLogger("info", "text", "")
	if closeFn == nil {
		t.Fatal("InitLogger() returned nil closeFn")
	}
	closeFn()
}

func TestInitLogger_JSONFormat(t *testing.T) {
	t.Parallel()

	closeFn := InitLogger("info", "json", "")
	if closeFn == nil {
		t.Fatal("InitLogger() returned nil closeFn")
	}
	closeFn()
}

func TestInitLogger_AllLevels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level string
	}{
		{level: "debug"},
		{level: "info"},
		{level: "warn"},
		{level: "error"},
		{level: "unknown"}, // should default to info
	}

	for _, tc := range tests {
		t.Run(tc.level, func(t *testing.T) {
			t.Parallel()

			closeFn := InitLogger(tc.level, "text", "")
			if closeFn == nil {
				t.Fatalf("InitLogger(%q) returned nil closeFn", tc.level)
			}
			closeFn()
		})
	}
}

func TestInitLogger_WithLogDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	closeFn := InitLogger("info", "text", dir)
	if closeFn == nil {
		t.Fatal("InitLogger() returned nil closeFn")
	}
	closeFn()

	// Verify log file was created
	logPath := filepath.Join(dir, "agent.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("agent.log was not created in logDir")
	}
}

func TestInitLogger_WithJSONAndLogDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	closeFn := InitLogger("debug", "json", dir)
	if closeFn == nil {
		t.Fatal("InitLogger() returned nil closeFn")
	}
	closeFn()

	logPath := filepath.Join(dir, "agent.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("agent.log was not created")
	}
}

func TestInitLogger_InvalidLogDir(t *testing.T) {
	t.Parallel()

	// Use an invalid path that cannot be created as a directory
	// On Windows, creating a directory inside a file path fails gracefully
	closeFn := InitLogger("info", "text", "/dev/null/impossible")
	if closeFn == nil {
		t.Fatal("InitLogger() returned nil closeFn")
	}
	closeFn()
}

// ─── multiWriteCloser tests ───

func TestNewMultiWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	mw := newMultiWriter(&writeCloserAdapter{Writer: &buf})
	if mw == nil {
		t.Fatal("newMultiWriter() returned nil")
	}
}

func TestMultiWriteCloser_Write(t *testing.T) {
	t.Parallel()

	var buf1, buf2 bytes.Buffer
	wc1 := &writeCloserAdapter{Writer: &buf1}
	wc2 := &writeCloserAdapter{Writer: &buf2}

	mw := newMultiWriter(wc1, wc2)

	data := []byte("hello world")
	n, err := mw.Write(data)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != len(data) {
		t.Errorf("Write() n = %d, want %d", n, len(data))
	}
	if buf1.String() != "hello world" {
		t.Errorf("buf1 = %q, want %q", buf1.String(), "hello world")
	}
	if buf2.String() != "hello world" {
		t.Errorf("buf2 = %q, want %q", buf2.String(), "hello world")
	}
}

func TestMultiWriteCloser_WriteSingle(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	wc := &writeCloserAdapter{Writer: &buf}

	mw := newMultiWriter(wc)

	n, err := mw.Write([]byte("single"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 6 {
		t.Errorf("Write() n = %d, want 6", n)
	}
	if buf.String() != "single" {
		t.Errorf("buf = %q", buf.String())
	}
}

func TestMultiWriteCloser_Close(t *testing.T) {
	t.Parallel()

	wc1 := &writeCloserAdapter{}
	wc2 := &writeCloserAdapter{}

	mw := newMultiWriter(wc1, wc2)

	if err := mw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !wc1.closed || !wc2.closed {
		t.Error("Close() did not close all writers")
	}
}

func TestMultiWriteCloser_CloseError(t *testing.T) {
	t.Parallel()

	wc1 := &writeCloserAdapter{}
	wc2 := &writeCloserAdapter{closeErr: os.ErrClosed}

	mw := newMultiWriter(wc1, wc2)

	err := mw.Close()
	if err == nil {
		t.Fatal("Close() expected error, got nil")
	}
	// First writer should still be closed
	if !wc1.closed {
		t.Error("Close() did not close first writer despite second error")
	}
}

// ─── WithSession tests ───

func TestWithSession(t *testing.T) {
	t.Parallel()

	closeFn := InitLogger("info", "text", "")
	defer closeFn()

	logger := WithSession("test-session-123")
	if logger == nil {
		t.Fatal("WithSession() returned nil")
	}
}

func TestWithSession_EmptyID(t *testing.T) {
	t.Parallel()

	closeFn := InitLogger("info", "text", "")
	defer closeFn()

	logger := WithSession("")
	if logger == nil {
		t.Fatal("WithSession() returned nil for empty ID")
	}
}

// ─── WithComponent tests ───

func TestWithComponent(t *testing.T) {
	t.Parallel()

	closeFn := InitLogger("info", "text", "")
	defer closeFn()

	logger := WithComponent("auth")
	if logger == nil {
		t.Fatal("WithComponent() returned nil")
	}
}

func TestWithComponent_EmptyName(t *testing.T) {
	t.Parallel()

	closeFn := InitLogger("info", "text", "")
	defer closeFn()

	logger := WithComponent("")
	if logger == nil {
		t.Fatal("WithComponent() returned nil for empty name")
	}
}

// ─── Helper: writeCloserAdapter ───

type writeCloserAdapter struct {
	Writer   interface{ Write([]byte) (int, error) }
	closed   bool
	closeErr error
}

func (w *writeCloserAdapter) Write(p []byte) (int, error) {
	if w.Writer != nil {
		return w.Writer.Write(p)
	}
	return len(p), nil
}

func (w *writeCloserAdapter) Close() error {
	w.closed = true
	return w.closeErr
}
