package batch

import (
	"context"
	"testing"
)

func TestContentHash(t *testing.T) {
	t.Parallel()

	h1 := contentHash("hello world")
	h2 := contentHash("hello world")
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}

	h3 := contentHash("different input")
	if h1 == h3 {
		t.Error("different inputs should produce different hashes")
	}
}

func TestContentHash_Empty(t *testing.T) {
	t.Parallel()

	h := contentHash("")
	if h == "" {
		t.Error("empty string should still produce a hash")
	}
}

func TestContentHash_Length(t *testing.T) {
	t.Parallel()

	h := contentHash("test")
	// Should be first 8 bytes = 16 hex chars
	if len(h) != 16 {
		t.Errorf("expected 16 char hash, got %d", len(h))
	}
}

func TestTruncateStr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is a long string", 5, "this ..."},
		{"", 5, ""},
		{"abc", 0, "..."},
	}

	for _, tt := range tests {
		got := truncateStr(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestSummarizeSimple(t *testing.T) {
	t.Parallel()

	turns := []TrajectoryTurn{
		{From: "human", Value: "Please fix the bug in main.go"},
		{From: "gpt", Value: "I will analyze the file and fix the bug."},
		{From: "system", Value: "Tool terminal was called", ToolUse: "terminal"},
	}

	summary := summarizeSimple(turns)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if !contains(summary, "terminal") {
		t.Error("summary should mention terminal tool call")
	}
	if !contains(summary, "Please fix") {
		t.Error("summary should contain user message excerpt")
	}
}

func TestSummarizeSimple_LongContent(t *testing.T) {
	t.Parallel()

	longStr := ""
	for i := 0; i < 200; i++ {
		longStr += "x"
	}

	turns := []TrajectoryTurn{
		{From: "human", Value: longStr},
	}

	summary := summarizeSimple(turns)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	// Should be truncated
	if contains(summary, longStr) {
		t.Error("long content should be truncated in summary")
	}
}

func TestSummarizeSimple_Empty(t *testing.T) {
	t.Parallel()

	summary := summarizeSimple(nil)
	if summary != "" {
		t.Errorf("expected empty summary for nil turns, got %q", summary)
	}

	summary = summarizeSimple([]TrajectoryTurn{})
	if summary != "" {
		t.Errorf("expected empty summary for empty turns, got %q", summary)
	}
}

func TestSummarizeSimple_SystemTurn(t *testing.T) {
	t.Parallel()

	// System turns without ToolUse should not be included in the summary
	turns := []TrajectoryTurn{
		{From: "system", Value: "initializing..."},
	}
	summary := summarizeSimple(turns)
	if summary != "" {
		t.Errorf("system turn without ToolUse should not appear, got %q", summary)
	}
}

func TestDefaultCompressionConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultCompressionConfig()
	if cfg.ProtectFirstN != 4 {
		t.Errorf("expected ProtectFirstN=4, got %d", cfg.ProtectFirstN)
	}
	if cfg.ProtectLastN != 2 {
		t.Errorf("expected ProtectLastN=2, got %d", cfg.ProtectLastN)
	}
	if cfg.MaxSummaryLen != 2000 {
		t.Errorf("expected MaxSummaryLen=2000, got %d", cfg.MaxSummaryLen)
	}
	if cfg.Concurrency != 3 {
		t.Errorf("expected Concurrency=3, got %d", cfg.Concurrency)
	}
}

func TestNewCompressor_DefaultConcurrency(t *testing.T) {
	t.Parallel()

	cfg := CompressionConfig{Concurrency: 0}
	c := NewCompressor(nil, cfg)
	if c == nil {
		t.Fatal("expected non-nil compressor")
	}
	if c.cfg.Concurrency != 3 {
		t.Errorf("expected default concurrency 3, got %d", c.cfg.Concurrency)
	}
}

func TestCompressTrajectory_TooShort(t *testing.T) {
	t.Parallel()

	cfg := DefaultCompressionConfig()
	c := NewCompressor(nil, cfg)

	// 6 turns = 4 (first) + 2 (last), nothing to compress
	turns := make([]TrajectoryTurn, 6)
	for i := range turns {
		turns[i] = TrajectoryTurn{From: "human", Value: "turn"}
	}

	result, err := c.CompressTrajectory(context.TODO(), turns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 6 {
		t.Errorf("expected 6 turns unchanged, got %d", len(result))
	}
}

func TestCompressTrajectory_WithNilProvider(t *testing.T) {
	t.Parallel()

	cfg := CompressionConfig{
		ProtectFirstN: 1,
		ProtectLastN:  1,
		MaxSummaryLen: 2000,
		Concurrency:   1,
	}
	c := NewCompressor(nil, cfg)

	turns := []TrajectoryTurn{
		{From: "system", Value: "system prompt"},
		{From: "human", Value: "user message"},
		{From: "gpt", Value: "assistant reply"},
		{From: "system", Value: "tool used", ToolUse: "terminal"},
		{From: "human", Value: "follow-up"},
		{From: "system", Value: "final turn"},
	}

	result, err := c.CompressTrajectory(context.TODO(), turns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be: 1 protected + 1 summary + 1 protected = 3
	if len(result) != 3 {
		t.Errorf("expected 3 turns after compression, got %d", len(result))
	}
	if result[0].Value != "system prompt" {
		t.Errorf("first turn should be protected, got %s", result[0].Value)
	}
	if result[2].Value != "final turn" {
		t.Errorf("last turn should be protected, got %s", result[2].Value)
	}
	// Middle should be summary
	if result[1].From != "system" {
		t.Errorf("summary turn should be from 'system', got %s", result[1].From)
	}
}

func TestCompressBatch_Empty(t *testing.T) {
	t.Parallel()

	cfg := DefaultCompressionConfig()
	c := NewCompressor(nil, cfg)

	results, err := c.CompressBatch(context.TODO(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// helper
func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return len(sub) == 0
}
