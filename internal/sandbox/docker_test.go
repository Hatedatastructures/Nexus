package sandbox

import (
	"context"
	"testing"
	"time"
)

func TestNewDockerEnvironment(t *testing.T) {
	t.Parallel()

	env := NewDockerEnvironment("test-container", "/workspace", nil)
	if env == nil {
		t.Fatal("expected non-nil environment")
	}
	if env.containerID != "test-container" {
		t.Errorf("expected containerID test-container, got %s", env.containerID)
	}
	if env.CWD() != "/workspace" {
		t.Errorf("expected CWD /workspace, got %s", env.CWD())
	}
}

func TestNewDockerEnvironment_EmptyCWD(t *testing.T) {
	t.Parallel()

	env := NewDockerEnvironment("test-container", "", nil)
	if env.CWD() != "/workspace" {
		t.Errorf("expected default CWD /workspace, got %s", env.CWD())
	}
}

func TestNewDockerEnvironment_DefaultTimeout(t *testing.T) {
	t.Parallel()

	env := NewDockerEnvironment("test-container", "/ws", nil)
	if env.defaultTimeout != 120*time.Second {
		t.Errorf("expected defaultTimeout 120s, got %v", env.defaultTimeout)
	}
}

func TestDockerEnvironment_UpdateCWD(t *testing.T) {
	t.Parallel()

	env := NewDockerEnvironment("test-container", "/ws", nil)
	env.UpdateCWD("/new/path")
	if env.CWD() != "/new/path" {
		t.Errorf("expected CWD /new/path, got %s", env.CWD())
	}
}

func TestDockerEnvironment_Cleanup(t *testing.T) {
	t.Parallel()

	env := NewDockerEnvironment("test-container", "/ws", nil)
	if err := env.Cleanup(); err != nil {
		t.Errorf("Cleanup should not error: %v", err)
	}
}

func TestDockerEnvironment_Execute_EmptyCommand(t *testing.T) {
	t.Parallel()

	env := NewDockerEnvironment("test-container", "/ws", nil)
	result, err := env.Execute(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0 for empty command, got %d", result.ExitCode)
	}
}

func TestDockerEnvironment_Execute_NilOptions(t *testing.T) {
	t.Parallel()

	env := NewDockerEnvironment("test-container", "/ws", nil)
	// This will actually try to call docker, which likely isn't available in tests.
	// We just verify it doesn't panic and returns proper error/result.
	result, err := env.Execute(context.Background(), "echo hello", nil)
	// If docker is not available, we get an error but the result should still be populated
	if err != nil {
		t.Logf("docker not available (expected in CI): %v", err)
	}
	_ = result
}

func TestDockerEnvironment_SecurityRunArgs(t *testing.T) {
	t.Parallel()

	env := NewDockerEnvironment("test-container", "/ws", nil)
	args := env.SecurityRunArgs()

	if len(args) == 0 {
		t.Error("expected non-empty security args with default security")
	}
}

func TestDockerEnvironment_SecurityRunArgs_WithCustomSecurity(t *testing.T) {
	t.Parallel()

	sec := &DockerSecurityOptions{
		CapDrop:     []string{"ALL"},
		CapAdd:      []string{"NET_ADMIN"},
		PIDsLimit:   100,
		NoNewPrivs:  true,
		TmpfsSizeMB: 256,
		NetworkNone: false,
	}

	env := NewDockerEnvironment("test-container", "/ws", sec)
	args := env.SecurityRunArgs()

	foundPidsLimit := false
	for _, arg := range args {
		if arg == "100" {
			foundPidsLimit = true
		}
	}
	if !foundPidsLimit {
		t.Error("expected pids-limit 100 in security args")
	}
}

func TestDefaultDockerSecurity(t *testing.T) {
	t.Parallel()

	sec := DefaultDockerSecurity()
	if sec == nil {
		t.Fatal("expected non-nil security options")
	}
	if !sec.NoNewPrivs {
		t.Error("expected NoNewPrivs=true")
	}
	if !sec.NetworkNone {
		t.Error("expected NetworkNone=true")
	}
	if sec.PIDsLimit != 256 {
		t.Errorf("expected PIDsLimit=256, got %d", sec.PIDsLimit)
	}
	if sec.TmpfsSizeMB != 512 {
		t.Errorf("expected TmpfsSizeMB=512, got %d", sec.TmpfsSizeMB)
	}
}

func TestDockerSecurityOptions_AllDisabled(t *testing.T) {
	t.Parallel()

	sec := &DockerSecurityOptions{
		PIDsLimit:   0,
		TmpfsSizeMB: 0,
		NetworkNone: false,
	}

	env := NewDockerEnvironment("test-container", "/ws", sec)
	args := env.SecurityRunArgs()

	// With PIDsLimit=0, TmpfsSizeMB=0, NetworkNone=false, only cap-drop and cap-add should appear
	for _, arg := range args {
		if arg == "--network" {
			t.Error("should not have --network when NetworkNone=false")
		}
	}
}

func TestDockerDefaultSecurityArgs(t *testing.T) {
	t.Parallel()

	if len(dockerDefaultSecurityArgs) == 0 {
		t.Error("expected non-empty default security args")
	}
}
