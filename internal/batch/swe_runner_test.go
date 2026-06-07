package batch

import (
	"context"
	"encoding/json"
	"testing"

	"nexus-agent/internal/llm"
)

func TestSWETask_JSONSerialization(t *testing.T) {
	t.Parallel()

	task := SWETask{
		ID:         "swe-001",
		Problem:    "Fix the off-by-one error in parser",
		RepoPath:   "/tmp/repo",
		BaseCommit: "abc123",
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed SWETask
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.ID != "swe-001" {
		t.Errorf("expected ID swe-001, got %s", parsed.ID)
	}
	if parsed.RepoPath != "/tmp/repo" {
		t.Errorf("expected RepoPath /tmp/repo, got %s", parsed.RepoPath)
	}
}

func TestSWERunner_ExecuteToolCall_EmptyCommand(t *testing.T) {
	t.Parallel()

	r := &SWERunner{env: nil}
	tc := llm.ToolCall{Name: "terminal", Arguments: `{"command": ""}`}
	result := r.executeToolCall(context.TODO(), tc)
	if result == "" {
		t.Error("expected non-empty error result")
	}
}

func TestSWERunner_ExecuteToolCall_NilEnv(t *testing.T) {
	t.Parallel()

	r := &SWERunner{env: nil}
	tc := llm.ToolCall{Name: "terminal", Arguments: `{"command": "ls"}`}
	result := r.executeToolCall(context.TODO(), tc)
	if result == "" {
		t.Error("expected non-empty error result for nil env")
	}
}

func TestSWERunner_ExecuteToolCall_InvalidJSON(t *testing.T) {
	t.Parallel()

	r := &SWERunner{env: nil}
	tc := llm.ToolCall{Name: "terminal", Arguments: "not json"}
	result := r.executeToolCall(context.TODO(), tc)
	if result == "" {
		t.Error("expected non-empty error result for invalid JSON")
	}
}

func TestSWERunner_NewSWERunner(t *testing.T) {
	t.Parallel()

	r := NewSWERunner(nil, nil)
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
}

func TestConvertToTrajectory_Empty(t *testing.T) {
	t.Parallel()

	turns := convertToTrajectory(nil, "gpt-4", true)
	if turns != nil {
		t.Errorf("expected nil for nil messages, got %v", turns)
	}
}

func TestTrajectoryTurn_JSONSerialization(t *testing.T) {
	t.Parallel()

	turn := TrajectoryTurn{
		From:    "human",
		Value:   "Hello world",
		ToolUse: "",
	}

	data, err := json.Marshal(turn)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed TrajectoryTurn
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.From != "human" {
		t.Errorf("expected From=human, got %s", parsed.From)
	}
}

func TestSWEMaxIterations(t *testing.T) {
	t.Parallel()

	if sweMaxIterations != 30 {
		t.Errorf("expected sweMaxIterations=30, got %d", sweMaxIterations)
	}
}

func TestSWEFinalOutputMarker(t *testing.T) {
	t.Parallel()

	if sweFinalOutputMarker != "MINI_SWE_AGENT_FINAL_OUTPUT" {
		t.Errorf("unexpected marker: %s", sweFinalOutputMarker)
	}
}
