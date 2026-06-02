package agent

import (
	"context"
	"fmt"
	"testing"

	"nexus-agent/internal/state"
)

// setupInsightsStore 创建一个带有 schema 的 SQLite Store (在 t.TempDir() 中)。
func setupInsightsStore(t *testing.T) *state.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := state.NewStore(dir + "/test.db")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := state.RunMigrations(context.Background(), store.DB()); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return store
}

// seedSession 向 store 插入一条会话记录。
// CreateSession 只写入基础字段，统计字段需要通过 UpdateSession 补充。
// 每个测试的 session 必须有不同的 title（title 有 UNIQUE 约束）。
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

// uniqueTitle 为每个测试的 session 生成唯一 title，避免 UNIQUE 约束冲突。
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
	if m.TotalInputTokens != 150 {
		t.Errorf("TotalInputTokens = %d, want 150", m.TotalInputTokens)
	}
	if m.TotalOutputTokens != 280 {
		t.Errorf("TotalOutputTokens = %d, want 280", m.TotalOutputTokens)
	}
	if m.TotalAPICalls != 7 {
		t.Errorf("TotalAPICalls = %d, want 7", m.TotalAPICalls)
	}
	if m.EstimatedCostUSD != 0.07 {
		t.Errorf("EstimatedCostUSD = %f, want 0.07", m.EstimatedCostUSD)
	}
	if m.AvgMessagesPerSession != 7.5 {
		t.Errorf("AvgMessagesPerSession = %f, want 7.5", m.AvgMessagesPerSession)
	}
}

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
	if gpt4.InputTokens != 150 {
		t.Errorf("gpt-4 InputTokens = %d, want 150", gpt4.InputTokens)
	}
	if gpt4.OutputTokens != 300 {
		t.Errorf("gpt-4 OutputTokens = %d, want 300", gpt4.OutputTokens)
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
	if stats.Title != uniqueTitle(t, "ss") {
		t.Errorf("Title = %q, want %q", stats.Title, uniqueTitle(t, "ss"))
	}
	if stats.Model != "gpt-4" {
		t.Errorf("Model = %q, want gpt-4", stats.Model)
	}
	if stats.Platform != "cli" {
		t.Errorf("Platform = %q, want cli", stats.Platform)
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
