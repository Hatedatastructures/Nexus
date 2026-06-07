package testutil

import (
	"context"
	"fmt"
	"testing"

	"nexus-agent/internal/tool"
)

func TestMockToolName(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{}
		if m.Name() != "mock_tool" {
			t.Errorf("Name() = %q, want %q", m.Name(), "mock_tool")
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{NameFunc: func() string { return "custom_tool" }}
		if m.Name() != "custom_tool" {
			t.Errorf("Name() = %q, want %q", m.Name(), "custom_tool")
		}
	})
}

func TestMockToolDescription(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{}
		if m.Description() != "mock tool for testing" {
			t.Errorf("Description() = %q", m.Description())
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{DescriptionFunc: func() string { return "my custom desc" }}
		if m.Description() != "my custom desc" {
			t.Errorf("Description() = %q, want %q", m.Description(), "my custom desc")
		}
	})
}

func TestMockToolSchema(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{}
		schema := m.Schema()
		if schema == nil {
			t.Fatal("Schema() returned nil")
		}
		if schema.Name != "mock_tool" {
			t.Errorf("Schema().Name = %q, want %q", schema.Name, "mock_tool")
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{SchemaFunc: func() *tool.ToolSchema {
			return &tool.ToolSchema{
				Name: "custom",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
				},
			}
		}}
		schema := m.Schema()
		if schema.Name != "custom" {
			t.Errorf("Schema().Name = %q, want %q", schema.Name, "custom")
		}
	})
}

func TestMockToolExecute(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{}
		result, err := m.Execute(context.Background(), map[string]any{"key": "value"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != `{"output": "mock result"}` {
			t.Errorf("Execute() = %q, want default mock result", result)
		}
		if m.ExecuteCalled.Load() != 1 {
			t.Errorf("ExecuteCalled = %d, want 1", m.ExecuteCalled.Load())
		}
		if m.LastArgs["key"] != "value" {
			t.Errorf("LastArgs = %v", m.LastArgs)
		}
	})

	t.Run("custom func", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{
			ExecuteFunc: func(ctx context.Context, args map[string]any) (string, error) {
				return fmt.Sprintf("executed with %v", args), nil
			},
		}
		result, err := m.Execute(context.Background(), map[string]any{"x": 1})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "executed with map[x:1]" {
			t.Errorf("Execute() = %q", result)
		}
	})

	t.Run("custom func with error", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{
			ExecuteFunc: func(ctx context.Context, args map[string]any) (string, error) {
				return "", fmt.Errorf("execution failed")
			},
		}
		_, err := m.Execute(context.Background(), nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("nil args", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{}
		result, err := m.Execute(context.Background(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == "" {
			t.Error("expected non-empty result")
		}
	})
}

func TestMockToolToolset(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{}
		if m.Toolset() != "test" {
			t.Errorf("Toolset() = %q, want %q", m.Toolset(), "test")
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{ToolsetFunc: func() string { return "core" }}
		if m.Toolset() != "core" {
			t.Errorf("Toolset() = %q, want %q", m.Toolset(), "core")
		}
	})
}

func TestMockToolIsAvailable(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{}
		if !m.IsAvailable() {
			t.Error("default IsAvailable() should be true")
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{IsAvailableFunc: func() bool { return false }}
		if m.IsAvailable() {
			t.Error("IsAvailable() should be false")
		}
	})
}

func TestMockToolEmoji(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{}
		if m.Emoji() == "" {
			t.Error("default Emoji() should not be empty")
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{EmojiFunc: func() string { return "hammer" }}
		if m.Emoji() != "hammer" {
			t.Errorf("Emoji() = %q, want %q", m.Emoji(), "hammer")
		}
	})
}

func TestMockToolMaxResultChars(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{}
		if m.MaxResultChars() != 0 {
			t.Errorf("MaxResultChars() = %d, want 0", m.MaxResultChars())
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		m := &MockTool{MaxResultCharsFunc: func() int { return 5000 }}
		if m.MaxResultChars() != 5000 {
			t.Errorf("MaxResultChars() = %d, want 5000", m.MaxResultChars())
		}
	})
}

func TestMockToolExecuteCalledCounter(t *testing.T) {
	t.Parallel()
	m := &MockTool{}
	for i := 0; i < 5; i++ {
		_, _ = m.Execute(context.Background(), nil)
	}
	if m.ExecuteCalled.Load() != 5 {
		t.Errorf("ExecuteCalled = %d, want 5", m.ExecuteCalled.Load())
	}
}
