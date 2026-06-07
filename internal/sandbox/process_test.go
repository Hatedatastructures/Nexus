package sandbox

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestOSProcessHandle_Poll_NilProcess(t *testing.T) {
	t.Parallel()

	h := &OSProcessHandle{}
	_, err := h.Poll()
	if err == nil {
		t.Error("expected error for nil process")
	}
}

func TestOSProcessHandle_Poll_WithExitCode(t *testing.T) {
	t.Parallel()

	code := 0
	h := &OSProcessHandle{exitCode: &code}

	result, err := h.Poll()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil exit code")
	}
	if *result != 0 {
		t.Errorf("expected exit code 0, got %d", *result)
	}
}

func TestOSProcessHandle_Kill_NilProcess(t *testing.T) {
	t.Parallel()

	h := &OSProcessHandle{}
	if err := h.Kill(); err != nil {
		t.Fatalf("Kill on nil process should not error: %v", err)
	}
}

func TestOSProcessHandle_Kill_AlreadyKilled(t *testing.T) {
	t.Parallel()

	h := &OSProcessHandle{killed: true}
	if err := h.Kill(); err != nil {
		t.Fatalf("Kill on already killed should not error: %v", err)
	}
}

func TestOSProcessHandle_Stdout_Nil(t *testing.T) {
	t.Parallel()

	h := &OSProcessHandle{}
	reader := h.Stdout()
	if reader == nil {
		t.Error("expected non-nil reader")
	}
	buf := new(bytes.Buffer)
	n, _ := buf.ReadFrom(reader)
	if n != 0 {
		t.Errorf("expected 0 bytes from nil stdout, got %d", n)
	}
}

func TestOSProcessHandle_Stderr_Nil(t *testing.T) {
	t.Parallel()

	h := &OSProcessHandle{}
	reader := h.Stderr()
	if reader == nil {
		t.Error("expected non-nil reader")
	}
	buf := new(bytes.Buffer)
	n, _ := buf.ReadFrom(reader)
	if n != 0 {
		t.Errorf("expected 0 bytes from nil stderr, got %d", n)
	}
}

func TestOSProcessHandle_Stdout_WithData(t *testing.T) {
	t.Parallel()

	h := &OSProcessHandle{
		stdoutBuf: bytes.NewBufferString("output data"),
	}
	reader := h.Stdout()
	buf := new(bytes.Buffer)
	n, _ := buf.ReadFrom(reader)
	if n != 11 {
		t.Errorf("expected 11 bytes, got %d", n)
	}
	if buf.String() != "output data" {
		t.Errorf("expected 'output data', got %q", buf.String())
	}
}

func TestOSProcessHandle_Stderr_WithData(t *testing.T) {
	t.Parallel()

	h := &OSProcessHandle{
		stderrBuf: bytes.NewBufferString("error data"),
	}
	reader := h.Stderr()
	buf := new(bytes.Buffer)
	n, _ := buf.ReadFrom(reader)
	if n != 10 {
		t.Errorf("expected 10 bytes, got %d", n)
	}
	if buf.String() != "error data" {
		t.Errorf("expected 'error data', got %q", buf.String())
	}
}

func TestOSProcessHandle_Wait_AlreadyExited(t *testing.T) {
	t.Parallel()

	code := 42
	h := &OSProcessHandle{exitCode: &code}

	exitCode, err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 42 {
		t.Errorf("expected exit code 42, got %d", exitCode)
	}
}

func TestOSProcessHandle_Wait_CancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := &OSProcessHandle{
		cmd:     exec.CommandContext(ctx, "sleep", "10"),
		process: nil, // nil process makes Wait call Poll first
	}

	_, err := h.Wait(ctx)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestOSProcessHandle_Integration(t *testing.T) {
	t.Parallel()

	// Run a quick command that exits immediately
	cmd := exec.Command("echo", "hello")
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start echo command: %v", err)
	}

	h := &OSProcessHandle{
		cmd:       cmd,
		process:   cmd.Process,
		stdoutBuf: stdoutBuf,
		stderrBuf: stderrBuf,
	}

	// Wait for completion
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exitCode, err := h.Wait(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}

	// Verify poll now returns cached exit code
	code, _ := h.Poll()
	if code == nil || *code != 0 {
		t.Errorf("expected cached exit code 0, got %v", code)
	}
}
