package plugin

import (
	"context"
	"testing"

	"nexus-agent/internal/tool"
)

// ─── Plugin interface stubs ───

type stubPlugin struct {
	name    string
	version string
}

func (s *stubPlugin) Name() string    { return s.name }
func (s *stubPlugin) Version() string { return s.version }
func (s *stubPlugin) Initialize(_ context.Context, _ map[string]any) error {
	return nil
}
func (s *stubPlugin) Shutdown(_ context.Context) error { return nil }

type stubTool struct {
	name string
}

func (t *stubTool) Name() string             { return t.name }
func (t *stubTool) Description() string      { return "stub tool" }
func (t *stubTool) Schema() *tool.ToolSchema { return nil }
func (t *stubTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	return `{"output":"ok"}`, nil
}
func (t *stubTool) Toolset() string     { return "test" }
func (t *stubTool) IsAvailable() bool   { return true }
func (t *stubTool) Emoji() string       { return "?" }
func (t *stubTool) MaxResultChars() int { return 0 }

// toolProviderPlugin implements Plugin + ToolProvider.
type toolProviderPlugin struct {
	stubPlugin
	tools []tool.Tool
}

func (tp *toolProviderPlugin) Tools() []tool.Tool { return tp.tools }

// hookProviderPlugin implements Plugin + HookProvider.
type hookProviderPlugin struct {
	stubPlugin
	hooks map[string][]HookHandler
}

func (hp *hookProviderPlugin) Hooks() map[string][]HookHandler { return hp.hooks }

// ─── Plugin interface tests ───

func TestStubPluginImplementsInterface(t *testing.T) {
	t.Parallel()

	var _ Plugin = (*stubPlugin)(nil)
}

func TestToolProviderImplementsInterface(t *testing.T) {
	t.Parallel()

	var _ ToolProvider = (*toolProviderPlugin)(nil)
}

func TestHookProviderImplementsInterface(t *testing.T) {
	t.Parallel()

	var _ HookProvider = (*hookProviderPlugin)(nil)
}

func TestMemoryProviderImplementsInterface(t *testing.T) {
	t.Parallel()

	var _ MemoryProvider = (MemoryProvider)(nil) //nolint:staticcheck
}

// ─── isValidPluginKind tests ───

func TestIsValidPluginKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind  string
		valid bool
	}{
		{"tool", true},
		{"hook", true},
		{"memory", true},
		{"composite", true},
		{"unknown", false},
		{"", false},
		{"Tool", false},
	}

	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()

			got := isValidPluginKind(tc.kind)
			if got != tc.valid {
				t.Errorf("isValidPluginKind(%q) = %v, want %v", tc.kind, got, tc.valid)
			}
		})
	}
}

// ─── isNameChar tests ───

func TestIsNameChar(t *testing.T) {
	t.Parallel()

	validChars := []rune{'a', 'Z', '0', '9', '_', '-'}
	for _, c := range validChars {
		if !isNameChar(c) {
			t.Errorf("isNameChar(%q) = false, want true", c)
		}
	}

	invalidChars := []rune{'.', ' ', '/', ':', '!', '@', '#'}
	for _, c := range invalidChars {
		if isNameChar(c) {
			t.Errorf("isNameChar(%q) = true, want false", c)
		}
	}
}

// ─── PluginKind constants tests ───

func TestPluginKindConstants(t *testing.T) {
	t.Parallel()

	if KindTool != PluginKind("tool") {
		t.Errorf("KindTool = %q", KindTool)
	}
	if KindHook != PluginKind("hook") {
		t.Errorf("KindHook = %q", KindHook)
	}
	if KindMemory != PluginKind("memory") {
		t.Errorf("KindMemory = %q", KindMemory)
	}
	if KindComposite != PluginKind("composite") {
		t.Errorf("KindComposite = %q", KindComposite)
	}
}
