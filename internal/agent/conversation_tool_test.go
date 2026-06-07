package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"nexus-agent/internal/approval"
	"nexus-agent/internal/llm"
	"nexus-agent/internal/tool"
	"nexus-agent/testutil"
)

// ───────────────────────── executeParallel via RunConversation ─────────────────────────

func TestRunConversation_ParallelToolExecution(t *testing.T) {
	// Use names from the parallelSafe map so executeParallel is triggered.
	const toolName1 = "file_read"
	const toolName2 = "file_search"

	mt1 := &mockTool{name: toolName1, description: "read file", result: `{"content": "file data"}`}
	mt2 := &mockTool{name: toolName2, description: "search", result: `{"matches": []}`}
	reg := tool.NewRegistry()
	reg.Register(mt1)
	reg.Register(mt2)

	callCount := atomic.Int32{}

	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			n := callCount.Add(1)
			if n == 1 {
				return &llm.ChatResponse{
					ID:         "resp-par",
					Content:    "Searching...",
					StopReason: llm.StopToolUse,
					ToolCalls: []llm.ToolCall{
						{ID: "pc-1", Name: toolName1, Arguments: `{"query": "a.go"}`},
						{ID: "pc-2", Name: toolName2, Arguments: `{"query": "*.go"}`},
					},
					Usage: &llm.TokenUsage{TotalTokens: 20},
				}, nil
			}
			return &llm.ChatResponse{
				ID:         "resp-par-final",
				Content:    "Found files.",
				StopReason: llm.StopEndTurn,
				Usage:      &llm.TokenUsage{TotalTokens: 10},
			}, nil
		},
	}

	agent := NewAgent(
		WithProvider(mock),
		WithModel("test"),
		WithToolRegistry(reg),
	)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "search", nil, "")
	if err != nil {
		t.Fatalf("RunConversation error: %v", err)
	}
	if !result.Completed {
		t.Error("result should be completed")
	}
	if mt1.executed.Load() != 1 {
		t.Errorf("tool1 executed = %d, want 1", mt1.executed.Load())
	}
	if mt2.executed.Load() != 1 {
		t.Errorf("tool2 executed = %d, want 1", mt2.executed.Load())
	}
	if result.ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2", result.ToolCalls)
	}
}

// ───────────────────────── executeSequential: approval rejection ─────────────────────────
//
// Use real approval.Checker: CheckTool only rejects when toolName == "terminal"
// and the command matches a hardBlocked pattern (e.g. "rm -rf /").

func TestRunConversation_SequentialApprovalRejected(t *testing.T) {
	mt := &mockTool{name: "terminal", description: "run terminal command", result: `{"ok": true}`}
	reg := tool.NewRegistry()
	reg.Register(mt)

	callCount := atomic.Int32{}

	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			n := callCount.Add(1)
			if n == 1 {
				return &llm.ChatResponse{
					ID:         "resp-ap",
					Content:    "Will run command",
					StopReason: llm.StopToolUse,
					ToolCalls: []llm.ToolCall{
						{
							ID:        "ac-1",
							Name:      "terminal",
							Arguments: `{"command": "rm -rf / --no-preserve-root"}`,
						},
					},
					Usage: &llm.TokenUsage{TotalTokens: 10},
				}, nil
			}
			return &llm.ChatResponse{
				ID:         "resp-ap-final",
				Content:    "Understood",
				StopReason: llm.StopEndTurn,
				Usage:      &llm.TokenUsage{TotalTokens: 5},
			}, nil
		},
	}

	checker := approval.NewChecker("always", nil, nil)

	agent := NewAgent(
		WithProvider(mock),
		WithModel("test"),
		WithToolRegistry(reg),
		WithApprovalChecker(checker),
	)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "run dangerous command", nil, "")
	if err != nil {
		t.Fatalf("RunConversation error: %v", err)
	}
	if !result.Completed {
		t.Error("result should be completed")
	}
	// The tool should NOT have been executed since "rm -rf /" is hardBlocked
	if mt.executed.Load() != 0 {
		t.Errorf("tool executed = %d, want 0 (blocked by approval)", mt.executed.Load())
	}
}

// ───────────────────────── toolCallback via executeSequential ─────────────────────────

func TestRunConversation_ToolCallback(t *testing.T) {
	const toolName = "unit_cb_tool"
	mt := &mockTool{name: toolName, description: "callback test", result: `{"ok": true}`}
	reg := tool.NewRegistry()
	reg.Register(mt)

	callCount := atomic.Int32{}
	var callbackName string
	callbackArgs := map[string]any{}

	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			n := callCount.Add(1)
			if n == 1 {
				return &llm.ChatResponse{
					StopReason: llm.StopToolUse,
					ToolCalls: []llm.ToolCall{
						{ID: "cb-1", Name: toolName, Arguments: `{"query": "q"}`},
					},
					Usage: &llm.TokenUsage{TotalTokens: 10},
				}, nil
			}
			return &llm.ChatResponse{
				Content:    "done",
				StopReason: llm.StopEndTurn,
				Usage:      &llm.TokenUsage{TotalTokens: 5},
			}, nil
		},
	}

	agent := NewAgent(
		WithProvider(mock),
		WithModel("test"),
		WithToolRegistry(reg),
		WithToolCallback(func(name string, args map[string]any) {
			callbackName = name
			callbackArgs = args
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "test callback", nil, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.Completed {
		t.Error("should be completed")
	}
	if callbackName != toolName {
		t.Errorf("callback name = %q, want %q", callbackName, toolName)
	}
	if callbackArgs["query"] != "q" {
		t.Errorf("callback args = %v, want query=q", callbackArgs)
	}
}

// ───────────────────────── guardrails integration ─────────────────────────

func TestRunConversation_GuardrailsBlockAll(t *testing.T) {
	const toolName = "unit_gr_block_tool"
	mt := &mockTool{name: toolName, description: "gr test", result: `{"ok": true}`}
	reg := tool.NewRegistry()
	reg.Register(mt)

	callCount := atomic.Int32{}

	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			n := callCount.Add(1)
			if n <= 2 {
				return &llm.ChatResponse{
					StopReason: llm.StopToolUse,
					ToolCalls: []llm.ToolCall{
						{ID: "gr-1", Name: toolName, Arguments: `{"query": "x"}`},
					},
					Usage: &llm.TokenUsage{TotalTokens: 10},
				}, nil
			}
			return &llm.ChatResponse{
				Content:    "ok fine",
				StopReason: llm.StopEndTurn,
				Usage:      &llm.TokenUsage{TotalTokens: 5},
			}, nil
		},
	}

	// Guardrails with max 1 consecutive duplicate
	gr := NewToolCallGuardrails().WithMaxConsecutiveDuplicates(1)

	agent := NewAgent(
		WithProvider(mock),
		WithModel("test"),
		WithToolRegistry(reg),
		WithGuardrails(gr),
	)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "test guardrails", nil, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.Completed {
		t.Error("should complete eventually")
	}
}
