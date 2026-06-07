package mcp

import (
	"context"
	"testing"
)

func TestNewMCPClient(t *testing.T) {
	t.Parallel()

	client := NewMCPClient()
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.connected {
		t.Error("new client should not be connected")
	}
	if client.requestID != 0 {
		t.Errorf("expected initial requestID 0, got %d", client.requestID)
	}
	if client.pendingReqs == nil {
		t.Error("pendingReqs should be initialized")
	}
}

func TestMCPClient_Disconnect_NotConnected(t *testing.T) {
	t.Parallel()

	client := NewMCPClient()
	err := client.Disconnect()
	if err != nil {
		t.Errorf("Disconnect on unconnected client should return nil, got %v", err)
	}
}

func TestMCPClient_CallTool_NotConnected(t *testing.T) {
	t.Parallel()

	client := NewMCPClient()
	_, err := client.CallTool(context.TODO(), "test-tool", nil)
	if err == nil {
		t.Error("expected error when calling tool on unconnected client")
	}
}

func TestValidateMCPCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		wantErr bool
	}{
		{"empty", "", true},
		{"relative path", "./server.sh", true},
		{"just filename", "server", true},
		{"nonexistent absolute", "/nonexistent/path/to/binary", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMCPCommand(tt.command)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMCPCommand(%q) error = %v, wantErr %v", tt.command, err, tt.wantErr)
			}
		})
	}
}

func TestValidateMCPCommand_DirectoryPath(t *testing.T) {
	t.Parallel()

	// Use temp dir as a path that exists but is a directory
	dir := t.TempDir()
	err := validateMCPCommand(dir)
	if err == nil {
		t.Error("expected error for directory path")
	}
}
