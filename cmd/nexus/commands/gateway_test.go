package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGatewayCommandName(t *testing.T) {
	t.Parallel()
	c := &GatewayCommand{}
	if c.Name() != "gateway" {
		t.Errorf("GatewayCommand.Name() = %q, want %q", c.Name(), "gateway")
	}
}

func TestGatewayCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &GatewayCommand{}
	if c.Synopsis() == "" {
		t.Error("GatewayCommand.Synopsis() returned empty string")
	}
}

func TestGatewayCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("gateway")
	if !ok {
		t.Fatal("gateway command not registered")
	}
	if _, isGateway := cmd.(*GatewayCommand); !isGateway {
		t.Errorf("expected *GatewayCommand, got %T", cmd)
	}
}

func TestGatewayReadPID(t *testing.T) {
	t.Parallel()
	c := &GatewayCommand{}

	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "gateway.pid")

	// Test missing file
	_, err := c.readPID(pidFile)
	if err == nil {
		t.Error("expected error for missing PID file")
	}

	// Test valid PID file
	if err := os.WriteFile(pidFile, []byte("12345"), 0644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}
	pid, err := c.readPID(pidFile)
	if err != nil {
		t.Fatalf("readPID() error: %v", err)
	}
	if pid != 12345 {
		t.Errorf("readPID() = %d, want 12345", pid)
	}

	// Test PID with whitespace
	if err := os.WriteFile(pidFile, []byte("  999  \n"), 0644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}
	pid, err = c.readPID(pidFile)
	if err != nil {
		t.Fatalf("readPID() error: %v", err)
	}
	if pid != 999 {
		t.Errorf("readPID() = %d, want 999", pid)
	}

	// Test invalid PID file
	if err := os.WriteFile(pidFile, []byte("not-a-number"), 0644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}
	_, err = c.readPID(pidFile)
	if err == nil {
		t.Error("expected error for invalid PID content")
	}
}

func TestGatewayGetPIDFile(t *testing.T) {
	t.Parallel()
	c := &GatewayCommand{}
	pidFile := c.getPIDFile()
	if pidFile == "" {
		t.Error("getPIDFile() returned empty string")
	}
	if filepath.Base(pidFile) != "gateway.pid" {
		t.Errorf("getPIDFile() = %q, expected base name gateway.pid", pidFile)
	}
}
