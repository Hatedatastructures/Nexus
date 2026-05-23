package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// ───────────────────────── 测试辅助 ─────────────────────────

// testTool is a lightweight mock implementing the Tool interface for tests.
type testTool struct {
	name        string
	description string
	execFn      func(ctx context.Context, args map[string]any) (string, error)
}

func (t *testTool) Name() string       { return t.name }
func (t *testTool) Description() string { return t.description }
func (t *testTool) Schema() *ToolSchema {
	return &ToolSchema{Name: t.name, Parameters: map[string]any{"type": "object"}}
}
func (t *testTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if t.execFn != nil {
		return t.execFn(ctx, args)
	}
	return `{"output": "test"}`, nil
}
func (t *testTool) Toolset() string     { return "test" }
func (t *testTool) IsAvailable() bool   { return true }
func (t *testTool) Emoji() string       { return "T" }
func (t *testTool) MaxResultChars() int { return 0 }

// newTestRegistry returns a fresh, isolated Registry for each test.
func newTestRegistry() *Registry {
	return &Registry{
		tools:         make(map[string]*ToolEntry),
		toolsets:      make(map[string][]string),
		aliases:       make(map[string]string),
		toolsetChecks: make(map[string]func() bool),
	}
}

// ───────────────────────── 测试用例 ─────────────────────────

func TestRegistry_RegisterAndDispatch(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	r.Register(&testTool{
		name:        "echo",
		description: "echoes back the input",
		execFn: func(_ context.Context, args map[string]any) (string, error) {
			msg, _ := args["message"].(string)
			return ToolResult(map[string]any{"output": msg}), nil
		},
	})

	result, err := r.Dispatch(context.Background(), "echo", map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("Dispatch returned unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if got, _ := parsed["output"].(string); got != "hello" {
		t.Errorf("expected output \"hello\", got %q", got)
	}
}

func TestRegistry_DispatchUnknownTool(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	result, err := r.Dispatch(context.Background(), "nonexistent", nil)
	if err != nil {
		t.Fatalf("Dispatch returned unexpected Go error: %v", err)
	}

	var parsed map[string]string
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if parsed["error"] == "" {
		t.Errorf("expected an error field in result, got: %s", result)
	}
}

func TestRegistry_GetDefinitions(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	r.Register(&testTool{name: "alpha", description: "first tool"})
	r.Register(&testTool{name: "beta", description: "second tool"})

	defs := r.GetDefinitions(nil) // empty slice → return all
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(defs))
	}

	names := make(map[string]bool)
	for _, d := range defs {
		names[d.Name] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("missing tool schemas: got names %v", names)
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	r.Register(&testTool{
		name:        "dup",
		description: "original",
		execFn: func(_ context.Context, _ map[string]any) (string, error) {
			return `{"output": "original"}`, nil
		},
	})

	r.Register(&testTool{
		name:        "dup",
		description: "replacement",
		execFn: func(_ context.Context, _ map[string]any) (string, error) {
			return `{"output": "replacement"}`, nil
		},
	})

	result, err := r.Dispatch(context.Background(), "dup", nil)
	if err != nil {
		t.Fatalf("Dispatch returned unexpected error: %v", err)
	}

	var parsed map[string]string
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if parsed["output"] != "replacement" {
		t.Errorf("expected replacement tool to be active, got output %q", parsed["output"])
	}

	// The entry stored should reflect the last-registered description.
	entry := r.GetEntry("dup")
	if entry == nil {
		t.Fatal("GetEntry returned nil for registered tool")
	}
	if entry.Tool.Description() != "replacement" {
		t.Errorf("expected description \"replacement\", got %q", entry.Tool.Description())
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Concurrent writers: register tools.
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			name := fmt.Sprintf("tool_%d", id)
			r.Register(&testTool{
				name:        name,
				description: name,
				execFn: func(_ context.Context, _ map[string]any) (string, error) {
					return `{"output": "concurrent"}`, nil
				},
			})
		}(i)
	}

	// Concurrent readers: dispatch / list.
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			// Dispatch to any name (may or may not exist yet); must not panic.
			_, _ = r.Dispatch(context.Background(), fmt.Sprintf("tool_%d", id%25), nil)
			_ = r.ListTools()
			_ = r.GetDefinitions(nil)
		}(i)
	}

	wg.Wait()

	// After all goroutines finish, verify all tools are present.
	names := r.ListTools()
	if len(names) != goroutines {
		t.Errorf("expected %d registered tools, got %d", goroutines, len(names))
	}
}
