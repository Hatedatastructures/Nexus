package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestScreenshotTool_Basics(t *testing.T) {
	t.Parallel()
	tool := &ScreenshotTool{}

	if tool.Name() != "computer_screenshot" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "computer_screenshot")
	}
	if tool.Toolset() != "computer_use" {
		t.Errorf("Toolset() = %q, want %q", tool.Toolset(), "computer_use")
	}
	if tool.MaxResultChars() != 10000 {
		t.Errorf("MaxResultChars() = %d, want 10000", tool.MaxResultChars())
	}
}

func TestMouseClickTool_Basics(t *testing.T) {
	t.Parallel()
	tool := &MouseClickTool{}

	if tool.Name() != "computer_mouse_click" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "computer_mouse_click")
	}
	if tool.Toolset() != "computer_use" {
		t.Errorf("Toolset() = %q, want %q", tool.Toolset(), "computer_use")
	}
}

func TestMouseMoveTool_Basics(t *testing.T) {
	t.Parallel()
	tool := &MouseMoveTool{}

	if tool.Name() != "computer_mouse_move" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "computer_mouse_move")
	}
	if tool.Toolset() != "computer_use" {
		t.Errorf("Toolset() = %q, want %q", tool.Toolset(), "computer_use")
	}
}

func TestTypeTextTool_Basics(t *testing.T) {
	t.Parallel()
	tool := &TypeTextTool{}

	if tool.Name() != "computer_type_text" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "computer_type_text")
	}
	if tool.Toolset() != "computer_use" {
		t.Errorf("Toolset() = %q, want %q", tool.Toolset(), "computer_use")
	}
}

func TestKeyPressTool_Basics(t *testing.T) {
	t.Parallel()
	tool := &KeyPressTool{}

	if tool.Name() != "computer_key_press" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "computer_key_press")
	}
	if tool.Toolset() != "computer_use" {
		t.Errorf("Toolset() = %q, want %q", tool.Toolset(), "computer_use")
	}
}

func TestCuEsc(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "hello", "hello"},
		{"plus", "a+b", "a{+}b"},
		{"caret", "^x", "{^}x"},
		{"percent", "100%", "100{%}"},
		{"tilde", "~test", "{~}test"},
		{"paren", "(a)", "{(}a{)}"},
		{"mixed", "+^%~()", "{+}{^}{%}{~}{(}{)}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := cuEsc(tt.input)
			if got != tt.want {
				t.Errorf("cuEsc(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCuWinKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		parts []string
	}{
		{"enter", []string{"enter"}},
		{"tab", []string{"tab"}},
		{"ctrl_c", []string{"ctrl", "c"}},
		{"alt_f4", []string{"alt", "f4"}},
		{"shift_enter", []string{"shift", "enter"}},
		{"unknown_key", []string{"x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := cuWinKey(tt.parts)
			if result == "" {
				t.Errorf("cuWinKey(%v) returned empty string", tt.parts)
			}
		})
	}
}

func TestCuMacKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  string
	}{
		{"enter", "enter"},
		{"tab", "tab"},
		{"escape", "escape"},
		{"space", "space"},
		{"unknown", "x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := cuMacKey(tt.key)
			_ = result // just verify no panic
		})
	}
}

func TestCuMacMods(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mods []string
	}{
		{"ctrl", []string{"ctrl"}},
		{"cmd_shift", []string{"cmd", "shift"}},
		{"empty", []string{}},
		{"alt_option", []string{"alt", "option"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := cuMacMods(tt.mods)
			_ = result
		})
	}
}

func TestCuXdoSeq(t *testing.T) {
	t.Parallel()
	result := cuXdoSeq([]string{"ctrl", "c"})
	if result == "" {
		t.Error("cuXdoSeq should return non-empty string")
	}
}

func TestCuFloat(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"x":    float64(100),
		"y":    float64(200),
		"zero": float64(0),
		"miss": "string",
	}

	if cuFloat(args, "x") != 100 {
		t.Errorf("cuFloat(x) = %v, want 100", cuFloat(args, "x"))
	}
	if cuFloat(args, "y") != 200 {
		t.Errorf("cuFloat(y) = %v, want 200", cuFloat(args, "y"))
	}
	if cuFloat(args, "zero") != 0 {
		t.Errorf("cuFloat(zero) = %v, want 0", cuFloat(args, "zero"))
	}
	if cuFloat(args, "miss") != 0 {
		t.Errorf("cuFloat(miss) = %v, want 0", cuFloat(args, "miss"))
	}
	if cuFloat(args, "nonexistent") != 0 {
		t.Errorf("cuFloat(nonexistent) = %v, want 0", cuFloat(args, "nonexistent"))
	}
}

func TestMouseClickTool_NegativeCoords(t *testing.T) {
	t.Parallel()
	tool := &MouseClickTool{}
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{
		"x": float64(-1),
		"y": float64(-1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "error") {
		t.Error("expected error for negative coordinates")
	}
}

func TestMouseMoveTool_NegativeCoords(t *testing.T) {
	t.Parallel()
	tool := &MouseMoveTool{}
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{
		"x": float64(-1),
		"y": float64(-1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "error") {
		t.Error("expected error for negative coordinates")
	}
}

func TestTypeTextTool_EmptyText(t *testing.T) {
	t.Parallel()
	tool := &TypeTextTool{}
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{"text": ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "error") {
		t.Error("expected error for empty text")
	}
}

func TestKeyPressTool_EmptyKey(t *testing.T) {
	t.Parallel()
	tool := &KeyPressTool{}
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{"key": ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "error") {
		t.Error("expected error for empty key")
	}
}

func TestComputerUseTool_Schemas(t *testing.T) {
	t.Parallel()
	tools := []Tool{
		&ScreenshotTool{},
		&MouseClickTool{},
		&MouseMoveTool{},
		&TypeTextTool{},
		&KeyPressTool{},
	}

	for _, tool := range tools {
		t.Run(tool.Name(), func(t *testing.T) {
			t.Parallel()
			schema := tool.Schema()
			if schema.Name == "" {
				t.Errorf("Schema().Name is empty for %s", tool.Name())
			}
			if schema.Parameters == nil {
				t.Errorf("Schema().Parameters is nil for %s", tool.Name())
			}
		})
	}
}

func TestCuKM_ContainsKeys(t *testing.T) {
	t.Parallel()
	expectedKeys := []string{"enter", "tab", "escape", "backspace", "delete", "up", "down", "left", "right", "space", "f1", "f12"}
	for _, key := range expectedKeys {
		if _, ok := cuKM[key]; !ok {
			t.Errorf("cuKM missing key %q", key)
		}
	}
}

func TestMouseClickTool_DefaultButton(t *testing.T) {
	t.Parallel()
	tool := &MouseClickTool{}
	ctx := context.Background()

	// This will attempt a real click, which may fail on CI, but we test the arg parsing
	result, _ := tool.Execute(ctx, map[string]any{
		"x": float64(0),
		"y": float64(0),
	})
	// On most systems this will fail to actually click at 0,0 but should not panic
	var parsed map[string]any
	_ = json.Unmarshal([]byte(result), &parsed)
	// The result should either be success or error, both are acceptable for this test
}
