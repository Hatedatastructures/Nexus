package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"nexus-agent/internal/llm"
	"nexus-agent/internal/tool"
	"nexus-agent/testutil"
)

// mockTool implements tool.Tool for testing purposes.
type mockTool struct {
	name        string
	description string
	executed    atomic.Int32
	result      string
}

func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Description() string  { return m.description }
func (m *mockTool) Schema() *tool.ToolSchema {
	return &tool.ToolSchema{
		Name:        m.name,
		Description: m.description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		},
	}
}
func (m *mockTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	m.executed.Add(1)
	return m.result, nil
}
func (m *mockTool) Toolset() string     { return "test" }
func (m *mockTool) IsAvailable() bool   { return true }
func (m *mockTool) Emoji() string       { return "T" }
func (m *mockTool) MaxResultChars() int { return 0 }

// testTimeout is a safety-net timeout for all conversation tests.
const testTimeout = 5 * time.Second

// ─────────────────────────────────────────────────────────────────────────────

func TestRunConversation_BasicText(t *testing.T) {
	t.Parallel()

	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{
				ID:         "resp-1",
				Content:    "Hello! How can I help you today?",
				StopReason: llm.StopEndTurn,
				Usage: &llm.TokenUsage{
					PromptTokens:     20,
					CompletionTokens: 10,
					TotalTokens:      30,
				},
			}, nil
		},
	}

	agent := NewAgent(
		WithProvider(mock),
		WithModel("test-model"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "hello", nil, "")
	if err != nil {
		t.Fatalf("RunConversation returned error: %v", err)
	}

	if !result.Completed {
		t.Error("result.Completed should be true")
	}
	if result.FinalResponse == "" {
		t.Error("result.FinalResponse should not be empty")
	}
	if result.FinalResponse != "Hello! How can I help you today?" {
		t.Errorf("unexpected FinalResponse: got %q", result.FinalResponse)
	}
	if result.APICalls != 1 {
		t.Errorf("expected 1 API call, got %d", result.APICalls)
	}
	if result.TotalTokens != 30 {
		t.Errorf("expected 30 total tokens, got %d", result.TotalTokens)
	}
}

func TestRunConversation_ToolExecution(t *testing.T) {
	t.Parallel()

	// Register a mock tool with a unique name to avoid collisions with the
	// global registry.  We use the global registry because tool.Registry
	// fields are unexported and cannot be constructed from outside the
	// tool package.
	const toolName = "conv_test_search_files"
	mt := &mockTool{
		name:        toolName,
		description: "Search files in the workspace",
		result:      `{"matches": ["main.go", "utils.go"]}`,
	}
	tool.GetRegistry().Register(mt)

	callCount := atomic.Int32{}

	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			n := callCount.Add(1)

			// First call: model requests tool use.
			if n == 1 {
				return &llm.ChatResponse{
					ID:         "resp-tool",
					Content:    "Let me search for that.",
					StopReason: llm.StopToolUse,
					ToolCalls: []llm.ToolCall{
						{
							ID:        "call-1",
							Name:      toolName,
							Arguments: `{"query": "*.go"}`,
						},
					},
					Usage: &llm.TokenUsage{
						PromptTokens:     30,
						CompletionTokens: 15,
						TotalTokens:      45,
					},
				}, nil
			}

			// Second call: model produces final text after receiving tool result.
			return &llm.ChatResponse{
				ID:         "resp-final",
				Content:    "I found 2 files matching your query.",
				StopReason: llm.StopEndTurn,
				Usage: &llm.TokenUsage{
					PromptTokens:     50,
					CompletionTokens: 20,
					TotalTokens:      70,
				},
			}, nil
		},
	}

	agent := NewAgent(
		WithProvider(mock),
		WithModel("test-model"),
		WithToolRegistry(tool.GetRegistry()),
	)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "find go files", nil, "")
	if err != nil {
		t.Fatalf("RunConversation returned error: %v", err)
	}

	if !result.Completed {
		t.Error("result.Completed should be true")
	}
	if result.FinalResponse == "" {
		t.Error("result.FinalResponse should not be empty")
	}
	if result.ToolCalls != 1 {
		t.Errorf("expected 1 tool call, got %d", result.ToolCalls)
	}
	if mt.executed.Load() != 1 {
		t.Errorf("expected mock tool to be executed once, got %d", mt.executed.Load())
	}
	if result.APICalls < 2 {
		t.Errorf("expected at least 2 API calls (tool_use + final), got %d", result.APICalls)
	}
}

func TestRunConversation_MaxIterationsBudget(t *testing.T) {
	t.Parallel()

	const toolName = "conv_test_loop_search"

	// Register a mock tool so dispatch succeeds.
	mt := &mockTool{
		name:        toolName,
		description: "Search files",
		result:      `{"matches": []}`,
	}
	tool.GetRegistry().Register(mt)

	// Provider always returns tool_use, never terminating on its own.
	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{
				ID:         "resp-loop",
				Content:    "Calling tool again...",
				StopReason: llm.StopToolUse,
				ToolCalls: []llm.ToolCall{
					{
						ID:        "call-loop",
						Name:      toolName,
						Arguments: `{"query": "*.go"}`,
					},
				},
				Usage: &llm.TokenUsage{
					PromptTokens:     10,
					CompletionTokens: 5,
					TotalTokens:      15,
				},
			}, nil
		},
	}

	// Use a very small iteration budget (3).
	// Disable guardrails so that the budget mechanism is tested in isolation.
	const maxIter = 3
	agent := NewAgent(
		WithProvider(mock),
		WithModel("test-model"),
		WithToolRegistry(tool.GetRegistry()),
		WithMaxIterations(maxIter),
		WithGuardrails(nil),
	)

	// Use a context with a short timeout as a safety net.
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "keep searching", nil, "")
	if err != nil {
		t.Fatalf("RunConversation returned error: %v", err)
	}

	// The conversation must stop (not loop forever).
	// Completed will be false since the model never returned end_turn.
	if result.Completed {
		t.Error("result.Completed should be false when budget is exhausted")
	}
	// Verify it made exactly maxIter iterations (API calls).
	if result.APICalls != maxIter {
		t.Errorf("expected %d API calls, got %d", maxIter, result.APICalls)
	}
	// Verify the tool was called the expected number of times.
	if mt.executed.Load() != int32(maxIter) {
		t.Errorf("expected tool to be called %d times, got %d", maxIter, mt.executed.Load())
	}
}
