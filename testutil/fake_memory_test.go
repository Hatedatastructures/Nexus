package testutil

import (
	"context"
	"fmt"
	"testing"

	"nexus-agent/internal/llm"
)

func TestFakeMemoryProviderName(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{}
		if f.Name() != "fake_memory" {
			t.Errorf("Name() = %q, want %q", f.Name(), "fake_memory")
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{ProviderName: "vector_db"}
		if f.Name() != "vector_db" {
			t.Errorf("Name() = %q, want %q", f.Name(), "vector_db")
		}
	})
}

func TestFakeMemoryProviderInitialize(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{}
		err := f.Initialize(context.Background(), "session-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !f.Initialized {
			t.Error("Initialized should be true")
		}
		if f.SessionID != "session-123" {
			t.Errorf("SessionID = %q, want %q", f.SessionID, "session-123")
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{InitError: fmt.Errorf("init failed")}
		err := f.Initialize(context.Background(), "session-1")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestFakeMemoryProviderSystemPromptBlock(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{}
		if f.SystemPromptBlock() == "" {
			t.Error("default SystemPromptBlock should not be empty")
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{SystemPrompt: "custom prompt"}
		if f.SystemPromptBlock() != "custom prompt" {
			t.Errorf("SystemPromptBlock() = %q, want %q", f.SystemPromptBlock(), "custom prompt")
		}
	})
}

func TestFakeMemoryProviderPrefetch(t *testing.T) {
	t.Parallel()

	t.Run("records query", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{}
		_, _ = f.Prefetch(context.Background(), "what is Go?")
		_, _ = f.Prefetch(context.Background(), "how to test?")

		if len(f.PrefetchedQueries) != 2 {
			t.Fatalf("PrefetchedQueries len = %d, want 2", len(f.PrefetchedQueries))
		}
		if f.PrefetchedQueries[0] != "what is Go?" {
			t.Errorf("PrefetchedQueries[0] = %q", f.PrefetchedQueries[0])
		}
	})

	t.Run("returns result", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{PrefetchResult: "recalled memory"}
		result, err := f.Prefetch(context.Background(), "query")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "recalled memory" {
			t.Errorf("result = %q, want %q", result, "recalled memory")
		}
	})

	t.Run("returns error", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{PrefetchError: fmt.Errorf("prefetch failed")}
		_, err := f.Prefetch(context.Background(), "query")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestFakeMemoryProviderSyncTurn(t *testing.T) {
	t.Parallel()

	t.Run("records turn", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{}
		err := f.SyncTurn(context.Background(), "user msg", "assistant msg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(f.SyncedTurns) != 1 {
			t.Fatalf("SyncedTurns len = %d, want 1", len(f.SyncedTurns))
		}
		if f.SyncedTurns[0].UserContent != "user msg" {
			t.Errorf("UserContent = %q", f.SyncedTurns[0].UserContent)
		}
		if f.SyncedTurns[0].AssistantContent != "assistant msg" {
			t.Errorf("AssistantContent = %q", f.SyncedTurns[0].AssistantContent)
		}
	})

	t.Run("sync error", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{SyncTurnError: fmt.Errorf("sync failed")}
		err := f.SyncTurn(context.Background(), "u", "a")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestFakeMemoryProviderGetToolSchemas(t *testing.T) {
	t.Parallel()

	t.Run("default empty", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{}
		schemas := f.GetToolSchemas()
		if len(schemas) != 0 {
			t.Errorf("expected empty schemas, got %d", len(schemas))
		}
	})

	t.Run("preset schemas", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{
			ToolSchemas: []llm.ToolSchema{
				{Name: "search_memory"},
				{Name: "store_memory"},
			},
		}
		schemas := f.GetToolSchemas()
		if len(schemas) != 2 {
			t.Fatalf("expected 2 schemas, got %d", len(schemas))
		}
		if schemas[0].Name != "search_memory" {
			t.Errorf("schemas[0].Name = %q", schemas[0].Name)
		}
	})
}

func TestFakeMemoryProviderHandleToolCall(t *testing.T) {
	t.Parallel()

	t.Run("records call", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{}
		args := map[string]any{"query": "test"}
		result, err := f.HandleToolCall(context.Background(), "search", args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(f.ToolCalls) != 1 {
			t.Fatalf("ToolCalls len = %d, want 1", len(f.ToolCalls))
		}
		if f.ToolCalls[0].ToolName != "search" {
			t.Errorf("ToolName = %q", f.ToolCalls[0].ToolName)
		}
		if result == "" {
			t.Error("expected non-empty result")
		}
	})

	t.Run("custom func", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{
			HandleToolCallFunc: func(ctx context.Context, toolName string, args map[string]any) (string, error) {
				return "custom result", nil
			},
		}
		result, err := f.HandleToolCall(context.Background(), "tool", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "custom result" {
			t.Errorf("result = %q, want %q", result, "custom result")
		}
	})

	t.Run("preset error", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{HandleToolCallError: fmt.Errorf("tool error")}
		_, err := f.HandleToolCall(context.Background(), "tool", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("preset result", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{HandleToolCallResult: "preset result"}
		result, err := f.HandleToolCall(context.Background(), "tool", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "preset result" {
			t.Errorf("result = %q, want %q", result, "preset result")
		}
	})
}

func TestFakeMemoryProviderShutdown(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{}
		err := f.Shutdown(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !f.ShutdownCalled {
			t.Error("ShutdownCalled should be true")
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{ShutdownError: fmt.Errorf("shutdown failed")}
		err := f.Shutdown(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestFakeMemoryProviderReset(t *testing.T) {
	t.Parallel()
	f := &FakeMemoryProvider{}
	_ = f.Initialize(context.Background(), "s1")
	_, _ = f.Prefetch(context.Background(), "q1")
	_ = f.SyncTurn(context.Background(), "u", "a")
	_, _ = f.HandleToolCall(context.Background(), "t", nil)
	_ = f.Shutdown(context.Background())

	f.Reset()
	if f.Initialized {
		t.Error("Initialized should be false")
	}
	if f.SessionID != "" {
		t.Error("SessionID should be empty")
	}
	if len(f.PrefetchedQueries) != 0 {
		t.Error("PrefetchedQueries should be empty")
	}
	if len(f.SyncedTurns) != 0 {
		t.Error("SyncedTurns should be empty")
	}
	if len(f.ToolCalls) != 0 {
		t.Error("ToolCalls should be empty")
	}
	if f.ShutdownCalled {
		t.Error("ShutdownCalled should be false")
	}
}

func TestFakeMemoryProviderLastPrefetchQuery(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{}
		_, err := f.LastPrefetchQuery()
		if err == nil {
			t.Fatal("expected error when no queries")
		}
	})

	t.Run("returns last", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{}
		_, _ = f.Prefetch(context.Background(), "first")
		_, _ = f.Prefetch(context.Background(), "last")
		last, err := f.LastPrefetchQuery()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if last != "last" {
			t.Errorf("LastPrefetchQuery() = %q, want %q", last, "last")
		}
	})
}

func TestFakeMemoryProviderLastToolCall(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{}
		_, err := f.LastToolCall()
		if err == nil {
			t.Fatal("expected error when no tool calls")
		}
	})

	t.Run("returns last", func(t *testing.T) {
		t.Parallel()
		f := &FakeMemoryProvider{}
		_, _ = f.HandleToolCall(context.Background(), "tool_a", nil)
		_, _ = f.HandleToolCall(context.Background(), "tool_b", map[string]any{"key": "val"})
		tc, err := f.LastToolCall()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tc.ToolName != "tool_b" {
			t.Errorf("ToolName = %q, want %q", tc.ToolName, "tool_b")
		}
	})
}
