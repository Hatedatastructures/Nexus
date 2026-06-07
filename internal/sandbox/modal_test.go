package sandbox

import (
	"context"
	"testing"
)

func TestNewModalEnvironment(t *testing.T) {
	t.Parallel()

	env := NewModalEnvironment()
	if env == nil {
		t.Fatal("expected non-nil environment")
	}
	if env.baseURL != "https://api.modal.com/v1" {
		t.Errorf("expected default baseURL, got %s", env.baseURL)
	}
	if env.appName != "nexus-sandbox" {
		t.Errorf("expected appName nexus-sandbox, got %s", env.appName)
	}
	if env.CWD() != "/root" {
		t.Errorf("expected default CWD /root, got %s", env.CWD())
	}
}

func TestModalEnvironment_UpdateCWD(t *testing.T) {
	t.Parallel()

	env := NewModalEnvironment()
	env.UpdateCWD("/workspace")
	if env.CWD() != "/workspace" {
		t.Errorf("expected CWD /workspace, got %s", env.CWD())
	}
}

func TestModalEnvironment_Execute_NoToken(t *testing.T) {
	t.Parallel()

	env := NewModalEnvironment()
	// Clear token if set
	env.token = ""

	_, err := env.Execute(context.TODO(), "echo hello", nil)
	if err == nil {
		t.Error("expected error when MODAL_TOKEN is not set")
	}
}

func TestModalEnvironment_ExecuteBackground_NotSupported(t *testing.T) {
	t.Parallel()

	env := NewModalEnvironment()
	_, err := env.ExecuteBackground(context.TODO(), "echo hello", nil)
	if err == nil {
		t.Error("expected error: Modal sandbox does not support background execution")
	}
}

func TestModalEnvironment_Cleanup_NoSandbox(t *testing.T) {
	t.Parallel()

	env := NewModalEnvironment()
	env.token = "test-token"

	if err := env.Cleanup(); err != nil {
		t.Errorf("Cleanup with no active sandbox should not error: %v", err)
	}
}
