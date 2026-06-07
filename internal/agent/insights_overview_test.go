package agent

import (
	"context"
	"fmt"
	"testing"

	"nexus-agent/internal/state"
)

// setupInsightsStore creates a SQLite Store with schema in t.TempDir().
func setupInsightsStore(t *testing.T) *state.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := state.NewStore(dir + "/test.db")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := state.RunMigrations(context.Background(), store.DB()); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return store
}

// seedSession inserts a session record into the store.
func seedSession(t *testing.T, store *state.Store, sess *state.Session) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.UpdateSession(ctx, sess); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
}

// uniqueTitle generates a unique title for each test's session.
func uniqueTitle(t *testing.T, base string) string {
	t.Helper()
	return fmt.Sprintf("%s-%s", base, t.Name())
}

// ───────────────────────────── NewInsightsEngine ─────────────────────────────

func TestNewInsightsEngine_WithStore(t *testing.T) {
	store := setupInsightsStore(t)
	engine := NewInsightsEngine(store)
	if engine == nil {
		t.Fatal("engine should not be nil")
	}
	if engine.store != store {
		t.Error("store mismatch")
	}
}

func TestNewInsightsEngine_NilStore(t *testing.T) {
	engine := NewInsightsEngine(nil)
	if engine == nil {
		t.Fatal("engine should not be nil even with nil store")
	}
}

// ───────────────────────────── GetOverview ─────────────────────────────

func TestGetOverview_NilStore(t *testing.T) {
	engine := NewInsightsEngine(nil)
	m, err := engine.GetOverview(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0", m.TotalSessions)
	}
}

func TestGetOverview_Empty(t *testing.T) {
	store := setupInsightsStore(t)
	engine := NewInsightsEngine(store)

	m, err := engine.GetOverview(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0", m.TotalSessions)
	}
	if m.AvgMessagesPerSession != 0 {
		t.Errorf("AvgMessagesPerSession = %f, want 0", m.AvgMessagesPerSession)
	}
}

func TestGetOverview_WithData(t *testing.T) {
	store := setupInsightsStore(t)
	engine := NewInsightsEngine(store)

	seedSession(t, store, &state.Session{
		ID: "s1", Model: "gpt-4", Source: "cli", Title: uniqueTitle(t, "ov1"),
		MessageCount: 10, ToolCallCount: 3, InputTokens: 100, OutputTokens: 200,
		EstimatedCostUSD: 0.05, APICallCount: 5,
	})
	seedSession(t, store, &state.Session{
		ID: "s2", Model: "claude-3", Source: "telegram", Title: uniqueTitle(t, "ov2"),
		MessageCount: 5, ToolCallCount: 1, InputTokens: 50, OutputTokens: 80,
		EstimatedCostUSD: 0.02, APICallCount: 2,
	})

	m, err := engine.GetOverview(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.TotalSessions != 2 {
		t.Errorf("TotalSessions = %d, want 2", m.TotalSessions)
	}
	if m.TotalMessages != 15 {
		t.Errorf("TotalMessages = %d, want 15", m.TotalMessages)
	}
	if m.TotalToolCalls != 4 {
		t.Errorf("TotalToolCalls = %d, want 4", m.TotalToolCalls)
	}
	if m.AvgMessagesPerSession != 7.5 {
		t.Errorf("AvgMessagesPerSession = %f, want 7.5", m.AvgMessagesPerSession)
	}
}
