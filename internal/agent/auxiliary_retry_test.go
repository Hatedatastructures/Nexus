package agent

import (
	"context"
	"fmt"
	"testing"

	"nexus-agent/internal/llm"
)

// ───────────────────────── tryProvider ─────────────────────────

func TestAuxiliaryClient_TryProvider_Success(t *testing.T) {
	p := &mockRouterProvider{name: "ok", resp: &llm.ChatResponse{Content: "direct"}}
	entry := &ProviderEntry{Provider: p, Model: "m", Priority: 1}

	r := NewProviderRouter([]*ProviderEntry{entry})
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 2})
	resp, err := aux.tryProvider(context.Background(), entry, &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("tryProvider: %v", err)
	}
	if resp.Content != "direct" {
		t.Errorf("content = %q, want direct", resp.Content)
	}
}

func TestAuxiliaryClient_TryProvider_AbortOnAuth(t *testing.T) {
	p := &mockRouterProvider{name: "auth-fail", err: fmt.Errorf("401 unauthorized")}
	entry := &ProviderEntry{Provider: p, Model: "m", Priority: 1}

	r := NewProviderRouter([]*ProviderEntry{entry})
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 2})
	_, err := aux.tryProvider(context.Background(), entry, &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error for auth failure")
	}
}

func TestAuxiliaryClient_TryProvider_ImmediateFallbackOnBilling(t *testing.T) {
	p := &mockRouterProvider{name: "billing", err: fmt.Errorf("402 insufficient credits")}
	entry := &ProviderEntry{Provider: p, Model: "m", Priority: 1}

	r := NewProviderRouter([]*ProviderEntry{entry})
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 2})
	_, err := aux.tryProvider(context.Background(), entry, &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error for billing failure")
	}
}

func TestAuxiliaryClient_TryProvider_RetriesExhausted(t *testing.T) {
	p := &mockRouterProvider{name: "flaky", err: fmt.Errorf("500 internal server error")}
	entry := &ProviderEntry{Provider: p, Model: "m", Priority: 1}

	r := NewProviderRouter([]*ProviderEntry{entry})
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 1})
	_, err := aux.tryProvider(context.Background(), entry, &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
}

func TestAuxiliaryClient_TryProvider_ContextCancelled(t *testing.T) {
	p := &mockRouterProvider{name: "slow", err: fmt.Errorf("500 internal server error")}
	entry := &ProviderEntry{Provider: p, Model: "m", Priority: 1}

	r := NewProviderRouter([]*ProviderEntry{entry})
	defer r.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 3})
	_, err := aux.tryProvider(ctx, entry, &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestAuxiliaryClient_TryProvider_RequestNotMutated(t *testing.T) {
	p := &mockRouterProvider{name: "ok", resp: &llm.ChatResponse{Content: "ok"}}
	entry := &ProviderEntry{Provider: p, Model: "router-model", Priority: 1}

	r := NewProviderRouter([]*ProviderEntry{entry})
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	origReq := &llm.ChatRequest{Model: "original"}
	_, err := aux.tryProvider(context.Background(), entry, origReq)
	if err != nil {
		t.Fatalf("tryProvider: %v", err)
	}
	if origReq.Model != "original" {
		t.Errorf("original request model mutated: got %q, want original", origReq.Model)
	}
}

// ───────────────────────── isRetry path ─────────────────────────

func TestAuxiliaryClient_ChatCompletion_RetryPathUnhealthy(t *testing.T) {
	p := &mockRouterProvider{
		name: "sick-but-ok",
		resp: &llm.ChatResponse{Content: "recovered"},
	}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()
	r.MarkHealthy("sick-but-ok", false)

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	resp, err := aux.chatCompletionWithStrategy(context.Background(), &llm.ChatRequest{}, true)
	if err != nil {
		t.Fatalf("retry path should try unhealthy provider: %v", err)
	}
	if resp.Content != "recovered" {
		t.Errorf("content = %q, want recovered", resp.Content)
	}
}

func TestAuxiliaryClient_ChatCompletionStream_RetryPathUnhealthy(t *testing.T) {
	ch := make(chan *llm.StreamDelta, 1)
	ch <- &llm.StreamDelta{Content: "stream-recovered", Done: true}
	close(ch)

	p := &mockRouterProvider{name: "sick-stream", stream: ch}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()
	r.MarkHealthy("sick-stream", false)

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	stream, err := aux.chatCompletionStreamWithStrategy(context.Background(), &llm.ChatRequest{}, true)
	if err != nil {
		t.Fatalf("stream retry path should try unhealthy provider: %v", err)
	}
	delta := <-stream
	if delta.Content != "stream-recovered" {
		t.Errorf("delta content = %q, want stream-recovered", delta.Content)
	}
}

func TestAuxiliaryClient_ChatCompletion_ModelNotFoundFallback(t *testing.T) {
	p1 := &mockRouterProvider{name: "wrong-model", err: fmt.Errorf("404 model not found")}
	p2 := &mockRouterProvider{name: "ok", resp: &llm.ChatResponse{Content: "model-fallback"}}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 1},
		{Provider: p2, Model: "m2", Priority: 2},
	}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	resp, err := aux.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Content != "model-fallback" {
		t.Errorf("content = %q, want model-fallback", resp.Content)
	}
}

func TestAuxiliaryClient_ChatCompletion_FormatErrorAbort(t *testing.T) {
	p1 := &mockRouterProvider{name: "format-err", err: fmt.Errorf("400 bad request: invalid_request")}
	p2 := &mockRouterProvider{name: "should-not-reach", resp: &llm.ChatResponse{Content: "nope"}}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 1},
		{Provider: p2, Model: "m2", Priority: 2},
	}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	_, err := aux.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("format error should abort")
	}
}

func TestAuxiliaryClient_ChatCompletion_ServerErrorFallback(t *testing.T) {
	p1 := &mockRouterProvider{name: "500", err: fmt.Errorf("500 internal server error")}
	p2 := &mockRouterProvider{name: "ok", resp: &llm.ChatResponse{Content: "server-fallback"}}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 1},
		{Provider: p2, Model: "m2", Priority: 2},
	}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	resp, err := aux.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Content != "server-fallback" {
		t.Errorf("content = %q, want server-fallback", resp.Content)
	}
}

func TestAuxiliaryClient_ChatCompletionStream_FormatErrorAbort(t *testing.T) {
	p1 := &mockRouterProvider{name: "fmt", err: fmt.Errorf("400 invalid_request")}
	p2 := &mockRouterProvider{name: "should-not-reach", resp: &llm.ChatResponse{Content: "nope"}}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 1},
		{Provider: p2, Model: "m2", Priority: 2},
	}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	_, err := aux.ChatCompletionStream(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("format error should abort in stream")
	}
}
