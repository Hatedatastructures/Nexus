package memory

import (
	"context"
	"testing"

	"nexus-agent/internal/llm"
)

// mockProvider implements Provider for testing Manager.
type mockProvider struct {
	name           string
	initErr        error
	prefetchResult string
	prefetchErr    error
	syncErr        error
	shutdownErr    error
	promptBlock    string
	toolSchemas    []llm.ToolSchema
	toolResult     string
	toolErr        error
}

func (m *mockProvider) Name() string                                 { return m.name }
func (m *mockProvider) Initialize(_ context.Context, _ string) error { return m.initErr }
func (m *mockProvider) SystemPromptBlock() string                    { return m.promptBlock }
func (m *mockProvider) Prefetch(_ context.Context, _ string) (string, error) {
	return m.prefetchResult, m.prefetchErr
}
func (m *mockProvider) QueuePrefetch(_ context.Context, _ string)     {}
func (m *mockProvider) SyncTurn(_ context.Context, _, _ string) error { return m.syncErr }
func (m *mockProvider) GetToolSchemas() []llm.ToolSchema              { return m.toolSchemas }
func (m *mockProvider) HandleToolCall(_ context.Context, _ string, _ map[string]any) (string, error) {
	return m.toolResult, m.toolErr
}
func (m *mockProvider) Shutdown(_ context.Context) error                       { return m.shutdownErr }
func (m *mockProvider) OnTurnStart(_ context.Context, _ int, _ string) error   { return nil }
func (m *mockProvider) OnSessionEnd(_ context.Context, _ []llm.Message) error  { return nil }
func (m *mockProvider) OnPreCompress(_ context.Context, _ []llm.Message) error { return nil }
func (m *mockProvider) OnDelegation(_ context.Context, _, _, _ string) error   { return nil }

// ---- NewManager ----

func TestNewManager(t *testing.T) {
	t.Parallel()

	t.Run("with nil builtin", func(t *testing.T) {
		t.Parallel()
		m := NewManager(nil)
		if m == nil {
			t.Fatal("expected non-nil Manager")
		}
		if len(m.toolProviders) != 0 {
			t.Errorf("expected empty toolProviders, got %d", len(m.toolProviders))
		}
	})

	t.Run("indexes builtin tool schemas", func(t *testing.T) {
		t.Parallel()
		bp := &mockProvider{
			name: "builtin",
			toolSchemas: []llm.ToolSchema{
				{Name: "memory"},
				{Name: "search"},
			},
		}
		m := NewManager(bp)
		if m.toolProviders["memory"] != bp {
			t.Error("expected 'memory' mapped to builtin")
		}
		if m.toolProviders["search"] != bp {
			t.Error("expected 'search' mapped to builtin")
		}
	})
}

// ---- SetExternal ----

func TestManager_SetExternal(t *testing.T) {
	t.Parallel()

	t.Run("adds external tool schemas", func(t *testing.T) {
		t.Parallel()
		bp := &mockProvider{name: "builtin"}
		m := NewManager(bp)

		ep := &mockProvider{
			name: "external",
			toolSchemas: []llm.ToolSchema{
				{Name: "external_memory"},
			},
		}
		m.SetExternal(ep)

		if m.toolProviders["external_memory"] != ep {
			t.Error("expected 'external_memory' mapped to external provider")
		}
	})

	t.Run("does not overwrite builtin tools on conflict", func(t *testing.T) {
		t.Parallel()
		bp := &mockProvider{
			name:        "builtin",
			toolSchemas: []llm.ToolSchema{{Name: "memory"}},
		}
		m := NewManager(bp)

		ep := &mockProvider{
			name:        "external",
			toolSchemas: []llm.ToolSchema{{Name: "memory"}},
		}
		m.SetExternal(ep)

		if m.toolProviders["memory"] != bp {
			t.Error("expected builtin to keep priority for 'memory' tool")
		}
	})

	t.Run("nil external is safe", func(t *testing.T) {
		t.Parallel()
		m := NewManager(&mockProvider{name: "builtin"})
		m.SetExternal(nil)
	})
}
