package agent

import (
	"os"
	"path/filepath"
	"testing"

	"nexus-agent/internal/llm"
)

func TestValidateTrajectoryPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"empty path", "", true},
		{"parent traversal", "../etc/passwd", true},
		{"absolute path", filepath.Join(os.TempDir(), "traj.jsonl"), true},
		{"valid relative", "trajectories/session1.jsonl", false},
		{"simple filename", "traj.jsonl", false},
		{"nested relative", "data/traj/output.jsonl", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTrajectoryPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTrajectoryPath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestSaveTrajectory(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "You are helpful."},
		{Role: llm.RoleUser, Content: "Hello"},
		{Role: llm.RoleAssistant, Content: "Hi there!"},
	}

	err = SaveTrajectory("traj.jsonl", msgs, "test-model", true)
	if err != nil {
		t.Fatalf("SaveTrajectory: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "traj.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("trajectory file is empty")
	}
}

func TestSaveTrajectory_InvalidPath(t *testing.T) {
	err := SaveTrajectory("", nil, "model", false)
	if err == nil {
		t.Fatal("expected error for empty path")
	}

	err = SaveTrajectory("/absolute/path.jsonl", nil, "model", false)
	if err == nil {
		t.Fatal("expected error for absolute path")
	}

	err = SaveTrajectory("../traversal.jsonl", nil, "model", false)
	if err == nil {
		t.Fatal("expected error for parent traversal")
	}
}

func TestSaveTrajectory_WithToolCalls(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "read the file"},
		{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
			{ID: "tc1", Name: "read_file", Arguments: `{"path":"/tmp/a.txt"}`},
		}},
		{Role: llm.RoleTool, Content: "file contents here"},
	}

	err = SaveTrajectory("traj_tools.jsonl", msgs, "test-model", false)
	if err != nil {
		t.Fatalf("SaveTrajectory: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "traj_tools.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("trajectory file is empty")
	}
}

func TestSaveTrajectoryBatch(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	entries := []TrajectoryEntry{
		{
			Conversations: []TrajectoryTurn{
				{From: "human", Value: "hello"},
				{From: "gpt", Value: "hi"},
			},
			Timestamp: "2025-01-01T00:00:00Z",
			Model:     "test-model",
			Completed: true,
		},
	}

	err = SaveTrajectoryBatch("batch.jsonl", entries)
	if err != nil {
		t.Fatalf("SaveTrajectoryBatch: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "batch.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("batch file is empty")
	}
}

func TestSaveTrajectoryBatch_InvalidPath(t *testing.T) {
	err := SaveTrajectoryBatch("", nil)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestConvertToTrajectory(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system prompt"},
		{Role: llm.RoleUser, Content: "user msg"},
		{Role: llm.RoleAssistant, Content: "assistant reply"},
	}

	entry := convertToTrajectory(msgs, "test-model", true)

	if len(entry.Conversations) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(entry.Conversations))
	}
	if entry.Conversations[0].From != "system" {
		t.Errorf("turn 0: expected from=system, got %s", entry.Conversations[0].From)
	}
	if entry.Conversations[1].From != "human" {
		t.Errorf("turn 1: expected from=human, got %s", entry.Conversations[1].From)
	}
	if entry.Conversations[2].From != "gpt" {
		t.Errorf("turn 2: expected from=gpt, got %s", entry.Conversations[2].From)
	}
	if entry.Model != "test-model" {
		t.Errorf("model: expected test-model, got %s", entry.Model)
	}
	if !entry.Completed {
		t.Error("expected completed=true")
	}
}

func TestConvertToTrajectory_WithReasoning(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Content: "answer", ReasoningContent: "thinking..."},
	}

	entry := convertToTrajectory(msgs, "model", true)
	if len(entry.Conversations) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(entry.Conversations))
	}
	if entry.Conversations[0].From != "gpt" {
		t.Errorf("expected from=gpt, got %s", entry.Conversations[0].From)
	}
}

func TestConvertToTrajectory_ToolCallsWithContent(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Content: "I will read the file.", ToolCalls: []llm.ToolCall{
			{ID: "tc1", Name: "read_file", Arguments: `{"path":"a.txt"}`},
		}},
	}

	entry := convertToTrajectory(msgs, "model", true)
	// Should produce: text turn + tool turn = 2 turns
	if len(entry.Conversations) != 2 {
		t.Fatalf("expected 2 turns (text + tool), got %d", len(entry.Conversations))
	}
	if entry.Conversations[0].Value != "I will read the file." {
		t.Errorf("text turn value: %q", entry.Conversations[0].Value)
	}
	if entry.Conversations[1].ToolUse != "read_file" {
		t.Errorf("tool turn tool_use: %q", entry.Conversations[1].ToolUse)
	}
}

func TestConvertRole(t *testing.T) {
	tests := []struct {
		role llm.MessageRole
		want string
	}{
		{llm.RoleSystem, "system"},
		{llm.RoleUser, "human"},
		{llm.RoleAssistant, "gpt"},
		{llm.RoleTool, "tool"},
		{llm.MessageRole("unknown"), "unknown"},
	}
	for _, tt := range tests {
		got := convertRole(tt.role)
		if got != tt.want {
			t.Errorf("convertRole(%v) = %q, want %q", tt.role, got, tt.want)
		}
	}
}

func TestConvertScratchpadToThink(t *testing.T) {
	if got := ConvertScratchpadToThink(""); got != "" {
		t.Errorf("empty: got %q", got)
	}
	if got := ConvertScratchpadToThink("my reasoning"); got == "" || got == "my reasoning" {
		t.Errorf("normal: got %q", got)
	}
}

func TestHasIncompleteScratchpad(t *testing.T) {
	if HasIncompleteScratchpad("") {
		t.Error("empty should be false")
	}
	if !HasIncompleteScratchpad("<think>\nstill thinking") {
		t.Error("incomplete should be true")
	}
}
