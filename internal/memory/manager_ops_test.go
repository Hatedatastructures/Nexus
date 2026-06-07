package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"nexus-agent/internal/llm"
)

// errBoom is a sentinel error used in tests.
var errBoom = fmt.Errorf("boom")

// ---- SystemPromptBlock ----

func TestManager_SystemPromptBlock(t *testing.T) {
	t.Parallel()

	t.Run("combines builtin and external blocks", func(t *testing.T) {
		t.Parallel()
		bp := &mockProvider{name: "builtin", promptBlock: "builtin-prompt"}
		m := NewManager(bp)
		m.SetExternal(&mockProvider{name: "external", promptBlock: "external-prompt"})

		result := m.SystemPromptBlock()
		if !strings.Contains(result, "builtin-prompt") {
			t.Error("expected builtin-prompt in result")
		}
		if !strings.Contains(result, "external-prompt") {
			t.Error("expected external-prompt in result")
		}
	})

	t.Run("nil builtin returns only external", func(t *testing.T) {
		t.Parallel()
		m := NewManager(nil)
		m.SetExternal(&mockProvider{name: "external", promptBlock: "ext"})

		result := m.SystemPromptBlock()
		if !strings.Contains(result, "ext") {
			t.Error("expected ext in result")
		}
	})

	t.Run("no providers returns empty", func(t *testing.T) {
		t.Parallel()
		m := NewManager(nil)
		if m.SystemPromptBlock() != "" {
			t.Error("expected empty string with no providers")
		}
	})
}

// ---- PrefetchAll ----

func TestManager_PrefetchAll(t *testing.T) {
	t.Parallel()

	t.Run("merges results from all providers", func(t *testing.T) {
		t.Parallel()
		bp := &mockProvider{name: "builtin", prefetchResult: "result-a"}
		m := NewManager(bp)
		m.SetExternal(&mockProvider{name: "external", prefetchResult: "result-b"})

		result, err := m.PrefetchAll(context.Background(), "query")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "result-a") {
			t.Error("expected result-a")
		}
		if !strings.Contains(result, "result-b") {
			t.Error("expected result-b")
		}
	})

	t.Run("continues when one provider fails", func(t *testing.T) {
		t.Parallel()
		bp := &mockProvider{name: "builtin", prefetchResult: "ok"}
		m := NewManager(bp)
		m.SetExternal(&mockProvider{name: "external", prefetchErr: errBoom})

		result, err := m.PrefetchAll(context.Background(), "query")
		if err != nil {
			t.Fatalf("should not return error for provider failure: %v", err)
		}
		if !strings.Contains(result, "ok") {
			t.Error("expected result from working provider")
		}
	})

	t.Run("skips empty results", func(t *testing.T) {
		t.Parallel()
		bp := &mockProvider{name: "builtin", prefetchResult: "   "}
		m := NewManager(bp)

		result, err := m.PrefetchAll(context.Background(), "query")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "" {
			t.Errorf("expected empty, got %q", result)
		}
	})
}

// ---- SyncAll ----

func TestManager_SyncAll(t *testing.T) {
	t.Parallel()

	t.Run("succeeds when all providers succeed", func(t *testing.T) {
		t.Parallel()
		m := NewManager(&mockProvider{name: "builtin"})
		m.SetExternal(&mockProvider{name: "external"})

		if err := m.SyncAll(context.Background(), "u", "a"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("returns error when provider fails", func(t *testing.T) {
		t.Parallel()
		m := NewManager(&mockProvider{name: "builtin", syncErr: errBoom})
		m.SetExternal(&mockProvider{name: "external"})

		err := m.SyncAll(context.Background(), "u", "a")
		if err == nil {
			t.Fatal("expected error when a provider fails")
		}
		if !strings.Contains(err.Error(), "builtin") {
			t.Error("error should mention failing provider")
		}
	})
}

// ---- GetToolSchemas ----

func TestManager_GetToolSchemas(t *testing.T) {
	t.Parallel()

	t.Run("deduplicates tool names", func(t *testing.T) {
		t.Parallel()
		bp := &mockProvider{
			name:        "builtin",
			toolSchemas: []llm.ToolSchema{{Name: "memory"}},
		}
		m := NewManager(bp)
		m.SetExternal(&mockProvider{
			name:        "external",
			toolSchemas: []llm.ToolSchema{{Name: "memory"}, {Name: "ext_tool"}},
		})

		schemas := m.GetToolSchemas()
		names := make(map[string]int)
		for _, s := range schemas {
			names[s.Name]++
		}
		if names["memory"] != 1 {
			t.Errorf("expected 'memory' exactly once, got %d", names["memory"])
		}
		if names["ext_tool"] != 1 {
			t.Errorf("expected 'ext_tool' exactly once, got %d", names["ext_tool"])
		}
	})

	t.Run("returns empty when no providers", func(t *testing.T) {
		t.Parallel()
		m := NewManager(nil)
		schemas := m.GetToolSchemas()
		if len(schemas) != 0 {
			t.Errorf("expected 0 schemas, got %d", len(schemas))
		}
	})
}

// ---- HandleToolCall ----

func TestManager_HandleToolCall(t *testing.T) {
	t.Parallel()

	t.Run("routes to correct provider", func(t *testing.T) {
		t.Parallel()
		bp := &mockProvider{
			name:        "builtin",
			toolSchemas: []llm.ToolSchema{{Name: "memory"}},
			toolResult:  `{"success":true,"data":"ok"}`,
		}
		m := NewManager(bp)

		result, err := m.HandleToolCall(context.Background(), "memory", map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "ok") {
			t.Errorf("expected provider result passed through, got %q", result)
		}
	})

	t.Run("returns error JSON for unknown tool", func(t *testing.T) {
		t.Parallel()
		m := NewManager(&mockProvider{name: "builtin"})

		result, err := m.HandleToolCall(context.Background(), "nonexistent", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("result is not valid JSON: %v", err)
		}
		if parsed["success"] == true {
			t.Error("expected success=false for unknown tool")
		}
	})

	t.Run("returns error JSON when provider fails", func(t *testing.T) {
		t.Parallel()
		bp := &mockProvider{
			name:    "builtin",
			toolErr: errBoom,
		}
		m := NewManager(bp)

		result, err := m.HandleToolCall(context.Background(), "memory", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("result is not valid JSON: %v", err)
		}
		if parsed["success"] == true {
			t.Error("expected success=false on provider error")
		}
	})
}

// ---- InitializeAll ----

func TestManager_InitializeAll(t *testing.T) {
	t.Parallel()

	t.Run("succeeds when all providers initialize", func(t *testing.T) {
		t.Parallel()
		m := NewManager(&mockProvider{name: "builtin"})
		m.SetExternal(&mockProvider{name: "external"})

		if err := m.InitializeAll(context.Background(), "session-1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fails fast on first provider error", func(t *testing.T) {
		t.Parallel()
		m := NewManager(&mockProvider{name: "builtin", initErr: errBoom})

		err := m.InitializeAll(context.Background(), "session-1")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "builtin") {
			t.Error("error should mention provider name")
		}
	})
}

// ---- ShutdownAll ----

func TestManager_ShutdownAll(t *testing.T) {
	t.Parallel()

	t.Run("always returns nil even on provider failure", func(t *testing.T) {
		t.Parallel()
		m := NewManager(&mockProvider{name: "builtin", shutdownErr: errBoom})

		if err := m.ShutdownAll(context.Background()); err != nil {
			t.Fatalf("ShutdownAll should not propagate errors: %v", err)
		}
	})

	t.Run("shuts down all providers", func(t *testing.T) {
		t.Parallel()
		m := NewManager(&mockProvider{name: "builtin"})
		m.SetExternal(&mockProvider{name: "external"})

		if err := m.ShutdownAll(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// ---- Concurrent access ----

func TestManager_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	m := NewManager(&mockProvider{name: "builtin", promptBlock: "prompt"})
	m.SetExternal(&mockProvider{name: "external", promptBlock: "ext"})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.SystemPromptBlock()
			_, _ = m.PrefetchAll(context.Background(), "query")
			_ = m.GetToolSchemas()
		}()
	}
	wg.Wait()
}
