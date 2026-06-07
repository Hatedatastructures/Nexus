package agent

import (
	"context"
	"strings"
	"testing"

	ictx "nexus-agent/internal/context"
	"nexus-agent/internal/llm"
	"nexus-agent/internal/tool"
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
	reg := tool.NewRegistry()
	reg.Register(&mockTool{name: "unit_test_tool", description: "a tool"})
	a := &AIAgent{model: "m", registry: reg}
	req := a.buildAPIRequest(nil, "")
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
	reg := tool.NewRegistry()
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
	reg := tool.NewRegistry()
	reg.Register(&mockTool{name: "file_write", description: "w", result: `ok`})
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
