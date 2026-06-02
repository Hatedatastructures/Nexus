package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nexus-agent/internal/approval"
	ictx "nexus-agent/internal/context"
	"nexus-agent/internal/llm"
	"nexus-agent/internal/tool"
	"nexus-agent/testutil"
)

// ───────────────────────── shouldParallelize ─────────────────────────

func TestShouldParallelize_EmptyList(t *testing.T) {
	a := &AIAgent{}
	if a.shouldParallelize(nil) {
		t.Error("empty list should not parallelize")
	}
}

func TestShouldParallelize_SingleTool(t *testing.T) {
	a := &AIAgent{}
	calls := []llm.ToolCall{{Name: "file_read"}}
	if a.shouldParallelize(calls) {
		t.Error("single tool should not parallelize")
	}
}

func TestShouldParallelize_AllSafe(t *testing.T) {
	a := &AIAgent{}
	calls := []llm.ToolCall{
		{Name: "file_read"},
		{Name: "file_search"},
		{Name: "list_directory"},
	}
	if !a.shouldParallelize(calls) {
		t.Error("all parallel-safe tools should parallelize")
	}
}

func TestShouldParallelize_MixedUnsafe(t *testing.T) {
	a := &AIAgent{}
	calls := []llm.ToolCall{
		{Name: "file_read"},
		{Name: "file_write"},
	}
	if a.shouldParallelize(calls) {
		t.Error("mixed tools with unsafe should not parallelize")
	}
}

func TestShouldParallelize_AllUnsafe(t *testing.T) {
	a := &AIAgent{}
	calls := []llm.ToolCall{
		{Name: "file_write"},
		{Name: "file_edit"},
	}
	if a.shouldParallelize(calls) {
		t.Error("all unsafe tools should not parallelize")
	}
}

func TestShouldParallelize_WebSearch(t *testing.T) {
	a := &AIAgent{}
	calls := []llm.ToolCall{
		{Name: "web_search"},
		{Name: "web_extract"},
	}
	if !a.shouldParallelize(calls) {
		t.Error("web_search + web_extract should parallelize")
	}
}

// ───────────────────────── parseToolArguments ─────────────────────────

func TestParseToolArguments_Empty(t *testing.T) {
	args, err := parseToolArguments("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(args) != 0 {
		t.Errorf("expected empty map, got %d entries", len(args))
	}
}

func TestParseToolArguments_ValidJSON(t *testing.T) {
	args, err := parseToolArguments(`{"query": "test", "limit": 10}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args["query"] != "test" {
		t.Errorf("query = %v, want test", args["query"])
	}
}

func TestParseToolArguments_InvalidJSON(t *testing.T) {
	_, err := parseToolArguments(`{invalid}`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ───────────────────────── isFileWriteTool ─────────────────────────

func TestIsFileWriteTool(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		want     bool
	}{
		{"file_write", "file_write", true},
		{"file_edit", "file_edit", true},
		{"patch", "patch", true},
		{"read_file", "read_file", false},
		{"search_files", "search_files", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isFileWriteTool(tt.toolName); got != tt.want {
				t.Errorf("isFileWriteTool(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}

// ───────────────────────── estimateTokensRough ─────────────────────────

func TestEstimateTokensRough(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "Hello world"},
		{Role: llm.RoleAssistant, Content: "Hi there"},
	}
	tokens := estimateTokensRough(msgs)
	if tokens <= 0 {
		t.Error("estimateTokensRough should return positive value")
	}
}

func TestEstimateTokensRough_Empty(t *testing.T) {
	tokens := estimateTokensRough(nil)
	if tokens != 0 {
		t.Errorf("empty messages should be 0 tokens, got %d", tokens)
	}
}

// ───────────────────────── buildSystemPrompt ─────────────────────────

func TestBuildSystemPrompt_Cached(t *testing.T) {
	a := &AIAgent{cachedSystemPrompt: "cached prompt"}
	prompt, err := a.buildSystemPrompt(context.Background(), "ignored")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prompt != "cached prompt" {
		t.Errorf("prompt = %q, want cached prompt", prompt)
	}
}

func TestBuildSystemPrompt_NilBuilder_Fallback(t *testing.T) {
	a := &AIAgent{}
	prompt, err := a.buildSystemPrompt(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prompt == "" {
		t.Error("fallback prompt should not be empty")
	}
	if !strings.Contains(prompt, "AI") {
		t.Errorf("fallback prompt should contain AI, got %q", prompt)
	}
}

func TestBuildSystemPrompt_NilBuilder_CustomMessage(t *testing.T) {
	a := &AIAgent{}
	prompt, err := a.buildSystemPrompt(context.Background(), "custom system msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prompt != "custom system msg" {
		t.Errorf("prompt = %q, want custom system msg", prompt)
	}
}

func TestBuildSystemPrompt_CachesResult(t *testing.T) {
	a := &AIAgent{}
	prompt1, _ := a.buildSystemPrompt(context.Background(), "test cache")
	prompt2, _ := a.buildSystemPrompt(context.Background(), "different")
	if prompt1 != prompt2 {
		t.Error("second call should return cached prompt")
	}
	if a.cachedSystemPrompt != "test cache" {
		t.Errorf("cachedSystemPrompt = %q, want 'test cache'", a.cachedSystemPrompt)
	}
}

// ───────────────────────── invalidateSystemPrompt ─────────────────────────

func TestInvalidateSystemPrompt(t *testing.T) {
	a := &AIAgent{cachedSystemPrompt: "old"}
	a.invalidateSystemPrompt()
	if a.cachedSystemPrompt != "" {
		t.Errorf("cachedSystemPrompt = %q, want empty", a.cachedSystemPrompt)
	}
}

// ───────────────────────── buildAPIRequest ─────────────────────────

func TestBuildAPIRequest_Basic(t *testing.T) {
	a := &AIAgent{model: "gpt-4", maxTokens: 4096}
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}
	req := a.buildAPIRequest(msgs, "sys")
	if req.Model != "gpt-4" {
		t.Errorf("Model = %q, want gpt-4", req.Model)
	}
	if req.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", req.MaxTokens)
	}
	if len(req.Messages) != 1 {
		t.Errorf("Messages = %d, want 1", len(req.Messages))
	}
}

func TestBuildAPIRequest_WithTools(t *testing.T) {
	reg := tool.GetRegistry()
	reg.Register(&mockTool{name: "unit_test_tool", description: "a tool"})
	a := &AIAgent{model: "m", registry: reg}
	req := a.buildAPIRequest(nil, "")
	// Global registry accumulates tools from other tests; just verify ours is present.
	found := false
	for _, tc := range req.Tools {
		if tc.Name == "unit_test_tool" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("unit_test_tool not found in %d tools", len(req.Tools))
	}
}

func TestBuildAPIRequest_WithReasoning(t *testing.T) {
	a := &AIAgent{
		model: "claude-3",
		reasoningCfg: &ReasoningConfig{
			Enabled:      true,
			BudgetTokens: 8000,
			Effort:       "high",
		},
	}
	req := a.buildAPIRequest(nil, "")
	if req.Metadata == nil {
		t.Error("Metadata should not be nil with reasoning enabled")
	}
}

func TestBuildAPIRequest_ReasoningZeroBudget(t *testing.T) {
	a := &AIAgent{
		model: "claude-3",
		reasoningCfg: &ReasoningConfig{
			Enabled:      true,
			BudgetTokens: 0,
		},
	}
	req := a.buildAPIRequest(nil, "")
	if req.Metadata == nil {
		t.Error("Metadata should not be nil even with zero budget")
	}
}

func TestBuildAPIRequest_NoReasoning(t *testing.T) {
	a := &AIAgent{model: "gpt-4"}
	req := a.buildAPIRequest(nil, "")
	if req.Metadata != nil {
		t.Error("Metadata should be nil without reasoning config")
	}
}

// ───────────────────────── dispatchTool ─────────────────────────

func TestDispatchTool_NilRegistry(t *testing.T) {
	a := &AIAgent{}
	result, err := a.dispatchTool(context.Background(), "any", nil)
	if err == nil {
		t.Error("expected error with nil registry")
	}
	if !strings.Contains(result, "未初始化") {
		t.Errorf("result = %q, should contain 未初始化", result)
	}
}

func TestDispatchTool_Success(t *testing.T) {
	reg := tool.GetRegistry()
	reg.Register(&mockTool{name: "unit_dispatch_tool", description: "d", result: `{"ok": true}`})
	a := &AIAgent{registry: reg}

	result, err := a.dispatchTool(context.Background(), "unit_dispatch_tool", map[string]any{"query": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "ok") {
		t.Errorf("result = %q, should contain ok", result)
	}
}

func TestDispatchTool_FileWriteBlocked(t *testing.T) {
	reg := tool.GetRegistry()
	reg.Register(&mockTool{name: "file_write", description: "w", result: `ok`})
	t.Cleanup(func() { reg.Register(&tool.FileWriteTool{}) })
	fs := NewFileSafetyChecker()
	a := &AIAgent{registry: reg, fileSafety: fs}

	result, err := a.dispatchTool(context.Background(), "file_write", map[string]any{
		"path":    ".env",
		"content": "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "拦截") {
		t.Errorf("file write to .env should be blocked, got: %s", result)
	}
}

// ───────────────────────── executeToolCalls (nil) ─────────────────────────

func TestExecuteToolCalls_Empty(t *testing.T) {
	a := &AIAgent{}
	result := a.executeToolCalls(context.Background(), nil)
	if result != nil {
		t.Error("expected nil for empty tool calls")
	}
}

// ───────────────────────── executeParallel via RunConversation ─────────────────────────

func TestRunConversation_ParallelToolExecution(t *testing.T) {
	// Use names from the parallelSafe map so executeParallel is triggered.
	// Save and restore the real tools to avoid polluting the global registry.
	const toolName1 = "file_read"
	const toolName2 = "file_search"

	origRead := tool.GetRegistry().GetEntry(toolName1)
	origSearch := tool.GetRegistry().GetEntry(toolName2)
	t.Cleanup(func() {
		if origRead != nil {
			tool.GetRegistry().Register(origRead.Tool)
		}
		if origSearch != nil {
			tool.GetRegistry().Register(origSearch.Tool)
		}
	})

	mt1 := &mockTool{name: toolName1, description: "read file", result: `{"content": "file data"}`}
	mt2 := &mockTool{name: toolName2, description: "search", result: `{"matches": []}`}
	tool.GetRegistry().Register(mt1)
	tool.GetRegistry().Register(mt2)

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
		WithToolRegistry(tool.GetRegistry()),
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
	tool.GetRegistry().Register(mt)
	t.Cleanup(func() { tool.GetRegistry().Register(tool.NewTerminalTool()) })

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
		WithToolRegistry(tool.GetRegistry()),
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
	tool.GetRegistry().Register(mt)

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
		WithToolRegistry(tool.GetRegistry()),
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
	tool.GetRegistry().Register(mt)

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
		WithToolRegistry(tool.GetRegistry()),
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

// ───────────────────────── preflightCompress ─────────────────────────

func TestPreflightCompress_NilCompressor(t *testing.T) {
	a := &AIAgent{}
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "test"}}
	outMsgs, outPrompt := a.preflightCompress(context.Background(), msgs, "sys")
	if len(outMsgs) != 1 {
		t.Error("should return messages unchanged with nil compressor")
	}
	if outPrompt != "sys" {
		t.Error("should return prompt unchanged")
	}
}

func TestPreflightCompress_TooFewMessages(t *testing.T) {
	// Real compressor: only 3 messages (< minForCompress=7), so compression is skipped
	a := &AIAgent{
		compressor: ictx.NewCompressor(3, 20000),
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "hello"},
	}
	outMsgs, _ := a.preflightCompress(context.Background(), msgs, "sys")
	if len(outMsgs) != 3 {
		t.Error("should skip compression for few messages")
	}
}

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
	tool.GetRegistry().Register(mt)

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
		WithToolRegistry(tool.GetRegistry()),
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
