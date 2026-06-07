package agent

import (
	"context"
	"fmt"
	"testing"

	"nexus-agent/internal/llm"
)

// ───────────────────────── tryFallbackChain ─────────────────────────

func TestTryFallbackChain_ShouldNotFallback(t *testing.T) {
	a := NewAgent()
	err := fmt.Errorf("400 bad request: invalid_request")
	_, gotErr := a.tryFallbackChain(context.Background(), err, &llm.ChatRequest{})
	if gotErr != err {
		t.Errorf("expected original error for non-fallback, got %v", gotErr)
	}
}

func TestTryFallbackChain_NoRouterNoChain(t *testing.T) {
	a := NewAgent()
	err := fmt.Errorf("429 rate limit exceeded")
	_, gotErr := a.tryFallbackChain(context.Background(), err, &llm.ChatRequest{})
	if gotErr != err {
		t.Errorf("expected original error when no router/chain, got %v", gotErr)
	}
}

func TestTryFallbackChain_WithRouter(t *testing.T) {
	p := &mockRouterProvider{
		name: "fallback-provider",
		resp: &llm.ChatResponse{Content: "recovered"},
	}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	a := NewAgent(WithRouter(r))
	err := fmt.Errorf("429 rate limit exceeded")

	resp, gotErr := a.tryFallbackChain(context.Background(), err, &llm.ChatRequest{Model: "orig"})
	if gotErr != nil {
		t.Fatalf("expected success via router, got %v", gotErr)
	}
	if resp.Content != "recovered" {
		t.Errorf("content = %q, want recovered", resp.Content)
	}
}

func TestTryFallbackChain_RouterFallsThroughToChain(t *testing.T) {
	p1 := &mockRouterProvider{name: "router-fail", err: fmt.Errorf("500 error")}
	r := NewProviderRouter([]*ProviderEntry{{Provider: p1, Model: "m1", Priority: 1}})
	defer r.Stop()

	p2 := &mockRouterProvider{name: "chain-ok", resp: &llm.ChatResponse{Content: "chain"}}
	chainEntries := []*FallbackEntry{{Provider: "chain-ok", Model: "m2", Priority: 1}}
	providerMap := map[string]llm.Provider{"chain-ok": p2}
	fc := NewFallbackChain(chainEntries, providerMap)

	a := NewAgent(WithRouter(r), WithFallbackChain(fc))
	err := fmt.Errorf("429 rate limit exceeded")

	resp, gotErr := a.tryFallbackChain(context.Background(), err, &llm.ChatRequest{Model: "orig"})
	if gotErr != nil {
		t.Fatalf("expected success via fallback chain, got %v", gotErr)
	}
	if resp.Content != "chain" {
		t.Errorf("content = %q, want chain", resp.Content)
	}
}

func TestTryFallbackChain_ChainOnly(t *testing.T) {
	p := &mockRouterProvider{
		name: "chain-provider",
		resp: &llm.ChatResponse{Content: "ok"},
	}
	entries := []*FallbackEntry{{Provider: "chain-provider", Model: "m", Priority: 1}}
	providerMap := map[string]llm.Provider{"chain-provider": p}
	fc := NewFallbackChain(entries, providerMap)

	a := NewAgent(WithFallbackChain(fc))
	err := fmt.Errorf("500 internal server error")

	resp, gotErr := a.tryFallbackChain(context.Background(), err, &llm.ChatRequest{Model: "orig"})
	if gotErr != nil {
		t.Fatalf("expected success, got %v", gotErr)
	}
	if resp.Content != "ok" {
		t.Errorf("content = %q, want ok", resp.Content)
	}
}

func TestTryFallbackChain_ChainAllFail(t *testing.T) {
	p1 := &mockRouterProvider{name: "fail1", err: fmt.Errorf("500 error")}
	p2 := &mockRouterProvider{name: "fail2", err: fmt.Errorf("503 overloaded")}

	entries := []*FallbackEntry{
		{Provider: "fail1", Model: "m1", Priority: 1},
		{Provider: "fail2", Model: "m2", Priority: 2},
	}
	providerMap := map[string]llm.Provider{"fail1": p1, "fail2": p2}
	fc := NewFallbackChain(entries, providerMap)

	a := NewAgent(WithFallbackChain(fc))
	err := fmt.Errorf("500 internal server error")

	_, gotErr := a.tryFallbackChain(context.Background(), err, &llm.ChatRequest{})
	if gotErr == nil {
		t.Fatal("expected error when all chain entries fail")
	}
}

func TestTryFallbackChain_ChainBillingAbort(t *testing.T) {
	p := &mockRouterProvider{
		name: "billing-provider",
		err:  fmt.Errorf("402 insufficient credits: payment required"),
	}
	entries := []*FallbackEntry{{Provider: "billing-provider", Model: "m", Priority: 1}}
	providerMap := map[string]llm.Provider{"billing-provider": p}
	fc := NewFallbackChain(entries, providerMap)

	a := NewAgent(WithFallbackChain(fc))
	err := fmt.Errorf("500 internal server error")

	_, gotErr := a.tryFallbackChain(context.Background(), err, &llm.ChatRequest{})
	if gotErr == nil {
		t.Fatal("expected error for billing abort")
	}
}

func TestTryFallbackChain_RequestNotMutated(t *testing.T) {
	p := &mockRouterProvider{
		name: "chain-ok",
		resp: &llm.ChatResponse{Content: "ok"},
	}
	entries := []*FallbackEntry{{Provider: "chain-ok", Model: "fallback-model", Priority: 1}}
	providerMap := map[string]llm.Provider{"chain-ok": p}
	fc := NewFallbackChain(entries, providerMap)

	a := NewAgent(WithFallbackChain(fc))
	req := &llm.ChatRequest{
		Model:    "original",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Metadata: map[string]any{"key": "val"},
	}

	_, gotErr := a.tryFallbackChain(context.Background(), fmt.Errorf("429 rate limit"), req)
	if gotErr != nil {
		t.Fatalf("unexpected error: %v", gotErr)
	}
	if req.Model != "original" {
		t.Errorf("request model mutated: got %q, want original", req.Model)
	}
}

func TestTryFallbackChain_ContextCancelled(t *testing.T) {
	p := &mockRouterProvider{
		name: "slow",
		err:  fmt.Errorf("500 error"),
	}
	entries := []*FallbackEntry{{Provider: "slow", Model: "m", Priority: 1}}
	providerMap := map[string]llm.Provider{"slow": p}
	fc := NewFallbackChain(entries, providerMap)

	a := NewAgent(WithFallbackChain(fc))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, gotErr := a.tryFallbackChain(ctx, fmt.Errorf("429 rate limit"), &llm.ChatRequest{})
	if gotErr == nil {
		t.Fatal("expected error with cancelled context")
	}
}
