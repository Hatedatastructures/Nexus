package agent

import (
	"context"
	"testing"

	"nexus-agent/internal/state"
)

// ───────────────────────────── GetModelBreakdown ─────────────────────────────

func TestGetModelBreakdown_NilStore(t *testing.T) {
	engine := NewInsightsEngine(nil)
	results, err := engine.GetModelBreakdown(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("results should be nil for nil store, got %v", results)
	}
}

func TestGetModelBreakdown_EmptyModel(t *testing.T) {
	store := setupInsightsStore(t)
	engine := NewInsightsEngine(store)

	seedSession(t, store, &state.Session{ID: "s1", Title: uniqueTitle(t, "em"), MessageCount: 5})

	results, err := engine.GetModelBreakdown(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Model != "unknown" {
		t.Errorf("Model = %q, want unknown", results[0].Model)
	}
}

func TestGetModelBreakdown_Multiple(t *testing.T) {
	store := setupInsightsStore(t)
	engine := NewInsightsEngine(store)

	seedSession(t, store, &state.Session{ID: "s1", Model: "gpt-4", Title: uniqueTitle(t, "mb1"), MessageCount: 10, InputTokens: 100, OutputTokens: 200, EstimatedCostUSD: 0.05})
	seedSession(t, store, &state.Session{ID: "s2", Model: "gpt-4", Title: uniqueTitle(t, "mb2"), MessageCount: 5, InputTokens: 50, OutputTokens: 100, EstimatedCostUSD: 0.02})
	seedSession(t, store, &state.Session{ID: "s3", Model: "claude-3", Title: uniqueTitle(t, "mb3"), MessageCount: 8, InputTokens: 80, OutputTokens: 160, EstimatedCostUSD: 0.03})

	results, err := engine.GetModelBreakdown(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}

	var gpt4 *ModelBreakdown
	for i := range results {
		if results[i].Model == "gpt-4" {
			gpt4 = &results[i]
			break
		}
	}
	if gpt4 == nil {
		t.Fatal("gpt-4 not found in results")
	}
	if gpt4.Sessions != 2 {
		t.Errorf("gpt-4 Sessions = %d, want 2", gpt4.Sessions)
	}
	if gpt4.Messages != 15 {
		t.Errorf("gpt-4 Messages = %d, want 15", gpt4.Messages)
	}
}

// ───────────────────────────── GetPlatformBreakdown ─────────────────────────────

func TestGetPlatformBreakdown_NilStore(t *testing.T) {
	engine := NewInsightsEngine(nil)
	results, err := engine.GetPlatformBreakdown(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("results should be nil for nil store, got %v", results)
	}
}

func TestGetPlatformBreakdown_EmptySource(t *testing.T) {
	store := setupInsightsStore(t)
	engine := NewInsightsEngine(store)

	seedSession(t, store, &state.Session{ID: "s1", Title: uniqueTitle(t, "es"), MessageCount: 5})

	results, err := engine.GetPlatformBreakdown(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Platform != "local" {
		t.Errorf("Platform = %q, want local", results[0].Platform)
	}
}

func TestGetPlatformBreakdown_Multiple(t *testing.T) {
	store := setupInsightsStore(t)
	engine := NewInsightsEngine(store)

	seedSession(t, store, &state.Session{ID: "s1", Source: "cli", Title: uniqueTitle(t, "pb1"), MessageCount: 10})
	seedSession(t, store, &state.Session{ID: "s2", Source: "telegram", Title: uniqueTitle(t, "pb2"), MessageCount: 5})
	seedSession(t, store, &state.Session{ID: "s3", Source: "cli", Title: uniqueTitle(t, "pb3"), MessageCount: 3})

	results, err := engine.GetPlatformBreakdown(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}

	var cliEntry *PlatformBreakdown
	for i := range results {
		if results[i].Platform == "cli" {
			cliEntry = &results[i]
			break
		}
	}
	if cliEntry == nil {
		t.Fatal("cli platform not found")
	}
	if cliEntry.Sessions != 2 {
		t.Errorf("cli Sessions = %d, want 2", cliEntry.Sessions)
	}
	if cliEntry.Messages != 13 {
		t.Errorf("cli Messages = %d, want 13", cliEntry.Messages)
	}
}
