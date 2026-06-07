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

// ───────────────────────── Streaming Tests ─────────────────────────

func TestRunConversation_StreamingText(t *testing.T) {
	t.Parallel()

	var streamedParts []string
	streamCb := func(delta string) { streamedParts = append(streamedParts, delta) }

	mock := &testutil.MockProvider{
		CreateChatCompletionStreamFunc: func(ctx context.Context, _ *llm.ChatRequest) (<-chan *llm.StreamDelta, error) {
			ch := make(chan *llm.StreamDelta, 4)
			go func() {
				defer close(ch)
				ch <- &llm.StreamDelta{Content: "Hello"}
				ch <- &llm.StreamDelta{Content: " world"}
				ch <- &llm.StreamDelta{Content: "!"}
				ch <- &llm.StreamDelta{Done: true, Usage: &llm.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}
			}()
			return ch, nil
		},
	}

	agent := NewAgent(
		WithProvider(mock),
		WithModel("test-model"),
		WithStreamCallback(streamCb),
	)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "hi", nil, "")
	if err != nil {
		t.Fatalf("RunConversation error: %v", err)
	}
	if !result.Completed {
		t.Error("result.Completed should be true")
	}
	if result.FinalResponse != "Hello world!" {
		t.Errorf("FinalResponse = %q, want %q", result.FinalResponse, "Hello world!")
	}
	if len(streamedParts) != 3 {
		t.Errorf("streamedParts = %d, want 3", len(streamedParts))
	}
}

func TestRunConversation_StreamingWithToolCalls(t *testing.T) {
	t.Parallel()

	const toolName = "conv_st_stream_search"
	mt := &mockTool{name: toolName, description: "search", result: `{"ok": true}`}
	reg := tool.NewRegistry()
	reg.Register(mt)

	callCount := atomic.Int32{}

	mock := &testutil.MockProvider{
		CreateChatCompletionStreamFunc: func(ctx context.Context, _ *llm.ChatRequest) (<-chan *llm.StreamDelta, error) {
			n := callCount.Add(1)
			ch := make(chan *llm.StreamDelta, 2)
			go func() {
				defer close(ch)
				if n == 1 {
					ch <- &llm.StreamDelta{
						Done: true,
						ToolCalls: []llm.ToolCall{
							{ID: "sc-1", Name: toolName, Arguments: `{"query": "x"}`},
						},
					}
				} else {
					ch <- &llm.StreamDelta{Content: "done"}
					ch <- &llm.StreamDelta{Done: true}
				}
			}()
			return ch, nil
		},
	}

	agent := NewAgent(
		WithProvider(mock),
		WithModel("test-model"),
		WithStreamCallback(func(string) {}),
		WithToolRegistry(reg),
	)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	result, err := agent.RunConversation(ctx, "search", nil, "")
	if err != nil {
		t.Fatalf("RunConversation error: %v", err)
	}
	if !result.Completed {
		t.Error("result.Completed should be true")
	}
	if result.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", result.ToolCalls)
	}
	if mt.executed.Load() != 1 {
		t.Errorf("tool executed = %d, want 1", mt.executed.Load())
	}
}

func TestRunConversation_StreamingErrorInDelta(t *testing.T) {
	t.Parallel()

	mock := &testutil.MockProvider{
		CreateChatCompletionStreamFunc: func(ctx context.Context, _ *llm.ChatRequest) (<-chan *llm.StreamDelta, error) {
			ch := make(chan *llm.StreamDelta, 2)
			go func() {
				defer close(ch)
				ch <- &llm.StreamDelta{Content: "partial"}
				ch <- &llm.StreamDelta{Error: fmt.Errorf("stream boom")}
			}()
			return ch, nil
		},
	}

	agent := NewAgent(
		WithProvider(mock),
		WithModel("test-model"),
		WithStreamCallback(func(string) {}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, err := agent.RunConversation(ctx, "hi", nil, "")
	if err == nil {
		t.Fatal("expected error from stream delta")
	}
	if !strings.Contains(err.Error(), "stream boom") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "stream boom")
	}
}

func TestRunConversation_StreamingContextCancelled(t *testing.T) {
	t.Parallel()

	mock := &testutil.MockProvider{
		CreateChatCompletionStreamFunc: func(ctx context.Context, _ *llm.ChatRequest) (<-chan *llm.StreamDelta, error) {
			ch := make(chan *llm.StreamDelta, 1)
			go func() {
				defer close(ch)
				select {
				case <-ctx.Done():
				case <-time.After(3 * time.Second):
				}
			}()
			return ch, nil
		},
	}

	agent := NewAgent(
		WithProvider(mock),
		WithModel("test-model"),
		WithStreamCallback(func(string) {}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := agent.RunConversation(ctx, "hi", nil, "")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestRunConversation_StreamingProviderStreamError(t *testing.T) {
	t.Parallel()

	mock := &testutil.MockProvider{
		StreamError: fmt.Errorf("cannot open stream"),
	}

	agent := NewAgent(
		WithProvider(mock),
		WithModel("test-model"),
		WithStreamCallback(func(string) {}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, err := agent.RunConversation(ctx, "hi", nil, "")
	if err == nil {
		t.Fatal("expected error from stream provider")
	}
	if !strings.Contains(err.Error(), "cannot open stream") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "cannot open stream")
	}
}
