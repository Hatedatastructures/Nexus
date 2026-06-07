package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nexus-agent/internal/llm"
	"nexus-agent/internal/tool"
	"nexus-agent/testutil"
)

// ───────────────────────── callLLMWithRetry: no provider ─────────────────────────

func TestRunConversation_NilProvider(t *testing.T) {
	agent := NewAgent(WithModel("test"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, err := agent.RunConversation(ctx, "hi", nil, "")
	if err == nil {
		t.Fatal("expected error with nil provider")
	}
	if !strings.Contains(err.Error(), "LLM") {
		t.Errorf("error = %q, should mention LLM", err.Error())
	}
}

// ───────────────────────── callLLMWithRetry: retry with error ─────────────────────────

func TestRunConversation_RetryableError(t *testing.T) {
	callCount := atomic.Int32{}

	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			n := callCount.Add(1)
			if n <= 2 {
				return nil, fmt.Errorf("500: internal server error")
			}
			return &llm.ChatResponse{
				Content:    "recovered",
				StopReason: llm.StopEndTurn,
				Usage:      &llm.TokenUsage{TotalTokens: 10},
			}, nil
		},
	}

	agent := NewAgent(WithProvider(mock), WithModel("test"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := agent.RunConversation(ctx, "hi", nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Completed {
		t.Error("should be completed")
	}
	if result.FinalResponse != "recovered" {
		t.Errorf("FinalResponse = %q, want recovered", result.FinalResponse)
	}
}

// ───────────────────────── RunConversation: context cancelled during retry ─────────────────────────

func TestRunConversation_ContextCancelledDuringBackoff(t *testing.T) {
	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			return nil, fmt.Errorf("500: internal server error")
		},
	}

	agent := NewAgent(WithProvider(mock), WithModel("test"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := agent.RunConversation(ctx, "hi", nil, "")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// ───────────────────────── RunConversation: non-retryable error ─────────────────────────

func TestRunConversation_NonRetryableError(t *testing.T) {
	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			return nil, fmt.Errorf("401: invalid api key")
		},
	}

	agent := NewAgent(WithProvider(mock), WithModel("test"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, err := agent.RunConversation(ctx, "hi", nil, "")
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

// ───────────────────────── RunConversation: max_tokens stop reason ─────────────────────────

func TestRunConversation_MaxTokensContinuation(t *testing.T) {
	callCount := atomic.Int32{}

	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			n := callCount.Add(1)
			if n == 1 {
				return &llm.ChatResponse{
					Content:    "partial response...",
					StopReason: llm.StopMaxTokens,
					Usage:      &llm.TokenUsage{TotalTokens: 10},
				}, nil
			}
			return &llm.ChatResponse{
				Content:    " continued and done.",
				StopReason: llm.StopEndTurn,
				Usage:      &llm.TokenUsage{TotalTokens: 5},
			}, nil
		},
	}

	agent := NewAgent(WithProvider(mock), WithModel("test"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "tell me a long story", nil, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.Completed {
		t.Error("should be completed")
	}
	if result.APICalls != 2 {
		t.Errorf("APICalls = %d, want 2", result.APICalls)
	}
}

// ───────────────────────── RunConversation: budget exhausted fallback ─────────────────────────

func TestRunConversation_BudgetExhaustedFindsLastAssistant(t *testing.T) {
	const toolName = "unit_budget_fallback_tool"
	mt := &mockTool{name: toolName, description: "budget", result: `{"ok": true}`}
	reg := tool.NewRegistry()
	reg.Register(mt)

	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{
				Content:    "Here is the answer",
				StopReason: llm.StopToolUse,
				ToolCalls: []llm.ToolCall{
					{ID: "bf-1", Name: toolName, Arguments: `{"query":"x"}`},
				},
				Usage: &llm.TokenUsage{TotalTokens: 10},
			}, nil
		},
	}

	agent := NewAgent(
		WithProvider(mock),
		WithModel("test"),
		WithToolRegistry(reg),
		WithMaxIterations(1),
		WithGuardrails(nil),
	)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "do stuff", nil, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.FinalResponse != "Here is the answer" {
		t.Errorf("FinalResponse = %q, want fallback from assistant", result.FinalResponse)
	}
}

// ───────────────────────── RunConversation: unknown stop reason ─────────────────────────

func TestRunConversation_UnknownStopReason(t *testing.T) {
	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{
				Content:    "some response",
				StopReason: "custom_stop",
				Usage:      &llm.TokenUsage{TotalTokens: 10},
			}, nil
		},
	}

	agent := NewAgent(WithProvider(mock), WithModel("test"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "hi", nil, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.Completed {
		t.Error("unknown stop reason should complete")
	}
	if result.FinalResponse != "some response" {
		t.Errorf("FinalResponse = %q, want 'some response'", result.FinalResponse)
	}
}

// ───────────────────────── RunConversation: with history ─────────────────────────

func TestRunConversation_WithHistory(t *testing.T) {
	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
			if len(req.Messages) < 3 {
				t.Errorf("Messages = %d, want at least 3", len(req.Messages))
			}
			return &llm.ChatResponse{
				Content:    "history received",
				StopReason: llm.StopEndTurn,
				Usage:      &llm.TokenUsage{TotalTokens: 15},
			}, nil
		},
	}

	agent := NewAgent(WithProvider(mock), WithModel("test"))
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "prev question"},
		{Role: llm.RoleAssistant, Content: "prev answer"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "new question", history, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.FinalResponse != "history received" {
		t.Errorf("FinalResponse = %q", result.FinalResponse)
	}
}

// ───────────────────────── RunConversation: with token tracking ─────────────────────────

func TestRunConversation_TokenTracking(t *testing.T) {
	mock := &testutil.MockProvider{
		CreateChatCompletionFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{
				Content:    "response",
				StopReason: llm.StopEndTurn,
				Usage: &llm.TokenUsage{
					PromptTokens:     100,
					CompletionTokens: 50,
					TotalTokens:      150,
				},
			}, nil
		},
	}

	agent := NewAgent(WithProvider(mock), WithModel("test"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "hi", nil, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", result.TotalTokens)
	}
}
