package tool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNewCheckpointManager_DefaultDir(t *testing.T) {
	t.Parallel()
	mgr := NewCheckpointManager("")
	if mgr.BaseDir == "" {
		t.Error("expected non-empty BaseDir when empty string passed")
	}
}

func TestNewCheckpointManager_CustomDir(t *testing.T) {
	t.Parallel()
	mgr := NewCheckpointManager("/tmp/test-checkpoints")
	if mgr.BaseDir != "/tmp/test-checkpoints" {
		t.Errorf("BaseDir = %q, want %q", mgr.BaseDir, "/tmp/test-checkpoints")
	}
}

func TestValidateCommitHash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		hash    string
		wantErr bool
	}{
		{"empty", "", true},
		{"short_valid", "abc1", false},
		{"full_sha", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", false},
		{"invalid_chars", "xyz!", true},
		{"too_short", "ab", true},
		{"spaces", "a b c", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateCommitHash(tt.hash)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateCommitHash(%q) error = %v, wantErr %v", tt.hash, err, tt.wantErr)
			}
		})
	}
}

func TestSanitizeRepoName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"colons_replaced", "C:/Users/test", "C__Users_test"},
		{"slashes_replaced", "/home/user/project", "_home_user_project"},
		{"spaces_replaced", "my project", "my_project"},
		{"dots_replaced", "/path/../hidden", "_path___hidden"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := sanitizeRepoName(tt.input)
			if result != tt.expect {
				t.Errorf("sanitizeRepoName(%q) = %q, want %q", tt.input, result, tt.expect)
			}
		})
	}
}

func TestCheckpointTool_Basics(t *testing.T) {
	t.Parallel()
	tool := &CheckpointTool{}

	if tool.Name() != "checkpoint" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "checkpoint")
	}
	if tool.Toolset() != "security" {
		t.Errorf("Toolset() = %q, want %q", tool.Toolset(), "security")
	}
	if tool.MaxResultChars() != 50000 {
		t.Errorf("MaxResultChars() = %d, want 50000", tool.MaxResultChars())
	}

	schema := tool.Schema()
	if schema.Name != "checkpoint" {
		t.Errorf("Schema().Name = %q, want %q", schema.Name, "checkpoint")
	}
}

func TestCheckpointTool_IsAvailable(t *testing.T) {
	tool := &CheckpointTool{}
	// Just check it doesn't panic; availability depends on git being installed
	_ = tool.IsAvailable()
}

func TestCheckpointTool_Execute_MissingAction(t *testing.T) {
	t.Parallel()
	tool := &CheckpointTool{}
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{"dir": "/tmp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &parsed); jsonErr != nil {
		t.Fatalf("parse error: %v", jsonErr)
	}
	if parsed["error"] == nil {
		t.Error("expected error for missing action")
	}
}

func TestCheckpointTool_Execute_MissingDir(t *testing.T) {
	t.Parallel()
	tool := &CheckpointTool{}
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &parsed); jsonErr != nil {
		t.Fatalf("parse error: %v", jsonErr)
	}
	if parsed["error"] == nil {
		t.Error("expected error for missing dir")
	}
}

func TestCheckpointTool_Execute_UnknownAction(t *testing.T) {
	t.Parallel()
	tool := &CheckpointTool{}
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{"action": "unknown", "dir": "/tmp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &parsed); jsonErr != nil {
		t.Fatalf("parse error: %v", jsonErr)
	}
	if parsed["error"] == nil {
		t.Error("expected error for unknown action")
	}
}

func TestCheckpointTool_Execute_Create(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Git needs author/committer identity; the tool sets GIT_CONFIG_GLOBAL=/dev/null
	// which blocks reading the user's global config, so we must provide identity
	// via environment variables.
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@test.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@test.com")

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &CheckpointTool{}
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{
		"action": "create",
		"dir":    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &parsed); jsonErr != nil {
		t.Fatalf("parse error: %v", jsonErr)
	}
	if parsed["error"] != nil {
		t.Fatalf("unexpected error in result: %v", parsed["error"])
	}
	if parsed["success"] != true {
		t.Errorf("expected success=true, got %v", parsed["success"])
	}
}

func TestCheckpointTool_Execute_Restore_MissingCommit(t *testing.T) {
	t.Parallel()
	tool := &CheckpointTool{}
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{
		"action": "restore",
		"dir":    "/tmp",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &parsed); jsonErr != nil {
		t.Fatalf("parse error: %v", jsonErr)
	}
	if parsed["error"] == nil {
		t.Error("expected error for missing commit")
	}
}

func TestGitEnv(t *testing.T) {
	env := gitEnv()
	foundGlobal := false
	foundNoSystem := false
	for _, e := range env {
		if e == "GIT_CONFIG_GLOBAL=/dev/null" {
			foundGlobal = true
		}
		if e == "GIT_CONFIG_NOSYSTEM=1" {
			foundNoSystem = true
		}
	}
	if !foundGlobal {
		t.Error("gitEnv should contain GIT_CONFIG_GLOBAL=/dev/null")
	}
	if !foundNoSystem {
		t.Error("gitEnv should contain GIT_CONFIG_NOSYSTEM=1")
	}
}
