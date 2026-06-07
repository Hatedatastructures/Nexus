package batch

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultBatchConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultBatchConfig()
	if cfg.MaxWorkers != 4 {
		t.Errorf("expected MaxWorkers=4, got %d", cfg.MaxWorkers)
	}
}

func TestNewBatchRunner_DefaultWorkers(t *testing.T) {
	t.Parallel()

	cfg := DefaultBatchConfig()
	cfg.MaxWorkers = 0 // should be set to 4

	runner := NewBatchRunner(cfg, func(_ context.Context, _ Prompt) (Trajectory, error) {
		return Trajectory{}, nil
	}, t.TempDir())

	if runner.cfg.MaxWorkers != 4 {
		t.Errorf("expected default MaxWorkers=4, got %d", runner.cfg.MaxWorkers)
	}
}

func TestBatchRunner_Run_Empty(t *testing.T) {
	t.Parallel()

	runner := NewBatchRunner(DefaultBatchConfig(), nil, t.TempDir())
	result, err := runner.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected total=0, got %d", result.Total)
	}
	if result.Completed != 0 {
		t.Errorf("expected completed=0, got %d", result.Completed)
	}
}

func TestBatchRunner_Run_SinglePrompt(t *testing.T) {
	t.Parallel()

	worker := func(_ context.Context, p Prompt) (Trajectory, error) {
		return Trajectory{
			Prompt:    p.Text,
			Response:  "done: " + p.Text,
			Completed: true,
		}, nil
	}

	runner := NewBatchRunner(DefaultBatchConfig(), worker, t.TempDir())
	result, err := runner.Run(context.Background(), []Prompt{{Text: "hello"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("expected total=1, got %d", result.Total)
	}
	if result.Completed != 1 {
		t.Errorf("expected completed=1, got %d", result.Completed)
	}
	if result.Failed != 0 {
		t.Errorf("expected failed=0, got %d", result.Failed)
	}
	if result.OutputFile == "" {
		t.Error("expected output file path")
	}
}

func TestBatchRunner_Run_WorkerError(t *testing.T) {
	t.Parallel()

	worker := func(_ context.Context, _ Prompt) (Trajectory, error) {
		return Trajectory{}, context.DeadlineExceeded
	}

	runner := NewBatchRunner(DefaultBatchConfig(), worker, t.TempDir())
	result, err := runner.Run(context.Background(), []Prompt{{Text: "fail-me"}})
	if err != nil {
		// runner itself should not return error for worker failures
		t.Fatalf("unexpected runner error: %v", err)
	}
	if result.Failed != 1 {
		t.Errorf("expected failed=1, got %d", result.Failed)
	}
}

func TestBatchRunner_Run_CancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	worker := func(_ context.Context, _ Prompt) (Trajectory, error) {
		return Trajectory{Prompt: "x", Response: "y"}, nil
	}

	runner := NewBatchRunner(DefaultBatchConfig(), worker, t.TempDir())
	// Should complete quickly due to cancelled context
	_, _ = runner.Run(ctx, []Prompt{{Text: "cancel-test"}})
}

func TestBatchRunner_Run_MultiplePrompts(t *testing.T) {
	t.Parallel()

	worker := func(_ context.Context, p Prompt) (Trajectory, error) {
		return Trajectory{
			Prompt:    p.Text,
			Response:  "processed",
			Completed: true,
		}, nil
	}

	cfg := DefaultBatchConfig()
	cfg.MaxWorkers = 2

	runner := NewBatchRunner(cfg, worker, t.TempDir())
	prompts := []Prompt{
		{Text: "p1"},
		{Text: "p2"},
		{Text: "p3"},
		{Text: "p4"},
		{Text: "p5"},
	}

	result, err := runner.Run(context.Background(), prompts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 5 {
		t.Errorf("expected total=5, got %d", result.Total)
	}
	if result.Completed != 5 {
		t.Errorf("expected completed=5, got %d", result.Completed)
	}
}

func TestWriteAndReadTrajectoriesJSONL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	trajectories := []Trajectory{
		{Prompt: "q1", Response: "a1", Completed: true, Tokens: 100},
		{Prompt: "q2", Response: "a2", Completed: false, Error: "timeout"},
	}

	if err := WriteTrajectoriesJSONL(path, trajectories); err != nil {
		t.Fatalf("WriteTrajectoriesJSONL failed: %v", err)
	}

	loaded, err := ReadTrajectoriesJSONL(path)
	if err != nil {
		t.Fatalf("ReadTrajectoriesJSONL failed: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 trajectories, got %d", len(loaded))
	}
	if loaded[0].Prompt != "q1" {
		t.Errorf("expected prompt q1, got %s", loaded[0].Prompt)
	}
	if loaded[1].Error != "timeout" {
		t.Errorf("expected error 'timeout', got %s", loaded[1].Error)
	}
}

func TestWriteTrajectoriesJSONL_Empty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")

	if err := WriteTrajectoriesJSONL(path, nil); err != nil {
		t.Fatalf("WriteTrajectoriesJSONL failed: %v", err)
	}

	loaded, err := ReadTrajectoriesJSONL(path)
	if err != nil {
		t.Fatalf("ReadTrajectoriesJSONL failed: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected 0 trajectories, got %d", len(loaded))
	}
}

func TestReadTrajectoriesJSONL_NotExist(t *testing.T) {
	t.Parallel()

	_, err := ReadTrajectoriesJSONL("/nonexistent/path/file.jsonl")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestReadTrajectoriesJSONL_InvalidLines(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "mixed.jsonl")

	f, _ := os.Create(path)
	w := bufio.NewWriter(f)
	_, _ = w.WriteString(`{"prompt":"valid","response":"ok","completed":true}` + "\n")
	_, _ = w.WriteString("invalid json line\n")
	_, _ = w.WriteString(`{"prompt":"also-valid","response":"ok2","completed":true}` + "\n")
	_ = w.Flush()
	_ = f.Close()

	loaded, err := ReadTrajectoriesJSONL(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("expected 2 valid trajectories (skipping bad line), got %d", len(loaded))
	}
}

func TestMergeJSONL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create two batch files
	batch1 := filepath.Join(dir, "batch_001.jsonl")
	f1, _ := os.Create(batch1)
	w1 := bufio.NewWriter(f1)
	_, _ = w1.WriteString(`{"prompt":"p1","response":"r1","completed":true}` + "\n")
	_ = w1.Flush()
	_ = f1.Close()

	batch2 := filepath.Join(dir, "batch_002.jsonl")
	f2, _ := os.Create(batch2)
	w2 := bufio.NewWriter(f2)
	_, _ = w2.WriteString(`{"prompt":"p2","response":"r2","completed":true}` + "\n")
	_ = w2.Flush()
	_ = f2.Close()

	outputPath, total, err := MergeJSONL(dir)
	if err != nil {
		t.Fatalf("MergeJSONL failed: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 merged entries, got %d", total)
	}
	if outputPath != filepath.Join(dir, "trajectories.jsonl") {
		t.Errorf("unexpected output path: %s", outputPath)
	}
}

func TestMergeJSONL_EmptyDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, total, err := MergeJSONL(dir)
	if err != nil {
		t.Fatalf("MergeJSONL failed: %v", err)
	}
	if total != 0 {
		t.Errorf("expected 0 for empty dir, got %d", total)
	}
}

func TestMergeJSONL_FiltersBadEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	batchFile := filepath.Join(dir, "batch_100.jsonl")
	f, _ := os.Create(batchFile)
	w := bufio.NewWriter(f)
	_, _ = w.WriteString(`{"prompt":"","response":"empty prompt","completed":true}` + "\n") // empty prompt, filtered
	_, _ = w.WriteString(`{"prompt":"valid","response":"ok","completed":true}` + "\n")      // valid
	_, _ = w.WriteString("corrupt line\n")                                                  // corrupt, filtered
	_ = w.Flush()
	_ = f.Close()

	_, total, err := MergeJSONL(dir)
	if err != nil {
		t.Fatalf("MergeJSONL failed: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 valid entry after filtering, got %d", total)
	}
}

func TestTrajectory_JSONSerialization(t *testing.T) {
	t.Parallel()

	traj := Trajectory{
		Prompt:    "test prompt",
		Response:  "test response",
		Model:     "gpt-4",
		ToolCalls: 3,
		Tokens:    150,
		Duration:  5 * time.Second,
		Completed: true,
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(traj)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed Trajectory
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.Prompt != "test prompt" {
		t.Errorf("expected prompt 'test prompt', got %s", parsed.Prompt)
	}
	if parsed.ToolCalls != 3 {
		t.Errorf("expected 3 tool calls, got %d", parsed.ToolCalls)
	}
}

func TestBatchResult_JSONSerialization(t *testing.T) {
	t.Parallel()

	result := BatchResult{
		Total:      10,
		Completed:  8,
		Failed:     1,
		Skipped:    1,
		Duration:   30 * time.Second,
		OutputFile: "/tmp/output.jsonl",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed BatchResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.Total != 10 {
		t.Errorf("expected total=10, got %d", parsed.Total)
	}
	if parsed.OutputFile != "/tmp/output.jsonl" {
		t.Errorf("unexpected output file: %s", parsed.OutputFile)
	}
}

func TestPrompt_JSONSerialization(t *testing.T) {
	t.Parallel()

	p := Prompt{
		Text:      "fix bug",
		Model:     "gpt-4",
		Container: "docker://python:3.11",
		Metadata:  map[string]string{"task_id": "123"},
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed Prompt
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.Text != "fix bug" {
		t.Errorf("expected text 'fix bug', got %s", parsed.Text)
	}
	if parsed.Metadata["task_id"] != "123" {
		t.Error("metadata should be preserved")
	}
}
