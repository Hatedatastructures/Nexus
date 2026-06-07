package agent

import (
	"context"
	"testing"

	"nexus-agent/internal/state"
)

// ───────────────────────────── FormatTerminal ─────────────────────────────

func TestFormatTerminal_Empty(t *testing.T) {
	store := setupInsightsStore(t)
	engine := NewInsightsEngine(store)

	out, err := engine.FormatTerminal(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("output should not be empty")
	}
	if len(out) < 50 {
		t.Errorf("output too short: %d chars", len(out))
	}
}

func TestFormatTerminal_WithData(t *testing.T) {
	store := setupInsightsStore(t)
	engine := NewInsightsEngine(store)

	seedSession(t, store, &state.Session{
		ID: "s1", Model: "gpt-4", Source: "cli", Title: uniqueTitle(t, "ft"),
		MessageCount: 10, InputTokens: 100, OutputTokens: 200,
		EstimatedCostUSD: 0.05, APICallCount: 3,
	})

	out, err := engine.FormatTerminal(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("output should not be empty")
	}
}

// ───────────────────────────── FormatGateway ─────────────────────────────

func TestFormatGateway_NilStore(t *testing.T) {
	engine := NewInsightsEngine(nil)

	out, err := engine.FormatGateway(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("output should not be empty")
	}
}

func TestFormatGateway_WithData(t *testing.T) {
	store := setupInsightsStore(t)
	engine := NewInsightsEngine(store)

	seedSession(t, store, &state.Session{
		ID: "s1", Model: "gpt-4", Source: "cli", Title: uniqueTitle(t, "gw1"),
		MessageCount: 10, ToolCallCount: 3, InputTokens: 100, OutputTokens: 200,
		EstimatedCostUSD: 0.05,
	})
	seedSession(t, store, &state.Session{
		ID: "s2", Model: "claude-3", Source: "telegram", Title: uniqueTitle(t, "gw2"),
		MessageCount: 5, ToolCallCount: 1, InputTokens: 50, OutputTokens: 80,
		EstimatedCostUSD: 0.02,
	})

	out, err := engine.FormatGateway(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("output should not be empty")
	}
}

// ───────────────────────────── GetSessionStats ─────────────────────────────

func TestGetSessionStats_NilStore(t *testing.T) {
	engine := NewInsightsEngine(nil)
	_, err := engine.GetSessionStats(context.Background(), "s1")
	if err == nil {
		t.Error("expected error with nil store")
	}
}

func TestGetSessionStats_Found(t *testing.T) {
	store := setupInsightsStore(t)
	engine := NewInsightsEngine(store)

	seedSession(t, store, &state.Session{
		ID: "s1", Model: "gpt-4", Source: "cli", Title: uniqueTitle(t, "ss"),
		MessageCount: 10, ToolCallCount: 3, EstimatedCostUSD: 0.05,
	})

	stats, err := engine.GetSessionStats(context.Background(), "s1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.ID != "s1" {
		t.Errorf("ID = %q, want s1", stats.ID)
	}
	if stats.Model != "gpt-4" {
		t.Errorf("Model = %q, want gpt-4", stats.Model)
	}
	if stats.MessageCount != 10 {
		t.Errorf("MessageCount = %d, want 10", stats.MessageCount)
	}
	if stats.ToolCallCount != 3 {
		t.Errorf("ToolCallCount = %d, want 3", stats.ToolCallCount)
	}
	if stats.EstimatedCostUSD != 0.05 {
		t.Errorf("EstimatedCostUSD = %f, want 0.05", stats.EstimatedCostUSD)
	}
}

func TestGetSessionStats_NotFound(t *testing.T) {
	store := setupInsightsStore(t)
	engine := NewInsightsEngine(store)

	_, err := engine.GetSessionStats(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}
