package agent

import (
	"context"
	"fmt"
	"testing"

	"nexus-agent/internal/llm"
)

// ───────────────────────── 构造函数和配置 ─────────────────────────

func TestDefaultAuxiliaryClientConfig(t *testing.T) {
	cfg := DefaultAuxiliaryClientConfig()
	if cfg.RetryCount != 2 {
		t.Errorf("RetryCount = %d, want 2", cfg.RetryCount)
	}
}

func TestNewAuxiliaryClient(t *testing.T) {
	p := &mockRouterProvider{name: "test"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClient(r)
	if aux == nil {
		t.Fatal("aux is nil")
	}
	if aux.retryCount != 2 {
		t.Errorf("retryCount = %d, want 2", aux.retryCount)
	}
}

func TestNewAuxiliaryClientWithConfig_Nil(t *testing.T) {
	p := &mockRouterProvider{name: "test"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, nil)
	if aux == nil {
		t.Fatal("aux is nil")
	}
	if aux.retryCount != 2 {
		t.Errorf("nil config should use default retryCount 2, got %d", aux.retryCount)
	}
}

func TestNewAuxiliaryClientWithConfig_Custom(t *testing.T) {
	p := &mockRouterProvider{name: "test"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 5})
	if aux.retryCount != 5 {
		t.Errorf("retryCount = %d, want 5", aux.retryCount)
	}
}

// ───────────────────────── ChatCompletion ─────────────────────────

func TestAuxiliaryClient_ChatCompletion_Success(t *testing.T) {
	p := &mockRouterProvider{
		name: "ok",
		resp: &llm.ChatResponse{Content: "hello"},
	}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	resp, err := aux.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("content = %q, want hello", resp.Content)
	}
}

func TestAuxiliaryClient_ChatCompletion_FallbackRateLimit(t *testing.T) {
	p1 := &mockRouterProvider{name: "limited", err: fmt.Errorf("429 too many requests: rate limit exceeded")}
	p2 := &mockRouterProvider{name: "ok", resp: &llm.ChatResponse{Content: "fallback"}}

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
	if resp.Content != "fallback" {
		t.Errorf("content = %q, want fallback", resp.Content)
	}
}

func TestAuxiliaryClient_ChatCompletion_AbortOnAuth(t *testing.T) {
	p1 := &mockRouterProvider{name: "auth-fail", err: fmt.Errorf("401 unauthorized: invalid api key")}
	p2 := &mockRouterProvider{name: "should-not-reach", resp: &llm.ChatResponse{Content: "oops"}}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 1},
		{Provider: p2, Model: "m2", Priority: 2},
	}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	_, err := aux.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error for auth failure")
	}
}

func TestAuxiliaryClient_ChatCompletion_AbortOnContextOverflow(t *testing.T) {
	p1 := &mockRouterProvider{name: "overflow", err: fmt.Errorf("400 context length exceeded")}
	p2 := &mockRouterProvider{name: "should-not-reach", resp: &llm.ChatResponse{Content: "oops"}}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 1},
		{Provider: p2, Model: "m2", Priority: 2},
	}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	_, err := aux.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("context overflow should abort")
	}
}

func TestAuxiliaryClient_ChatCompletion_BillingImmediateFallback(t *testing.T) {
	p1 := &mockRouterProvider{name: "broke", err: fmt.Errorf("402 insufficient credits: please top up")}
	p2 := &mockRouterProvider{name: "ok", resp: &llm.ChatResponse{Content: "billing-fallback"}}

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
	if resp.Content != "billing-fallback" {
		t.Errorf("content = %q, want billing-fallback", resp.Content)
	}
}

func TestAuxiliaryClient_ChatCompletion_AllFail(t *testing.T) {
	p := &mockRouterProvider{name: "dead", err: fmt.Errorf("429 rate limit")}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	_, err := aux.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

func TestAuxiliaryClient_ChatCompletion_AllUnhealthy(t *testing.T) {
	p := &mockRouterProvider{name: "dead"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()
	r.MarkHealthy("dead", false)

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	_, err := aux.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error when all providers unhealthy")
	}
}

func TestAuxiliaryClient_ChatCompletion_RequestNotMutated(t *testing.T) {
	p := &mockRouterProvider{name: "ok", resp: &llm.ChatResponse{Content: "ok"}}
	entries := []*ProviderEntry{{Provider: p, Model: "router-model", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	origReq := &llm.ChatRequest{Model: "original"}
	_, err := aux.ChatCompletion(context.Background(), origReq)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if origReq.Model != "original" {
		t.Errorf("original request model mutated: got %q, want original", origReq.Model)
	}
}

// ───────────────────────── ChatCompletionStream ─────────────────────────

func TestAuxiliaryClient_ChatCompletionStream_Success(t *testing.T) {
	ch := make(chan *llm.StreamDelta, 1)
	ch <- &llm.StreamDelta{Content: "stream-ok", Done: true}
	close(ch)

	p := &mockRouterProvider{name: "stream-ok", stream: ch}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	stream, err := aux.ChatCompletionStream(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	delta := <-stream
	if delta.Content != "stream-ok" {
		t.Errorf("delta content = %q, want stream-ok", delta.Content)
	}
}

func TestAuxiliaryClient_ChatCompletionStream_Fallback(t *testing.T) {
	p1 := &mockRouterProvider{name: "fail", err: fmt.Errorf("429 rate limit exceeded")}
	ch := make(chan *llm.StreamDelta, 1)
	ch <- &llm.StreamDelta{Content: "stream-fallback", Done: true}
	close(ch)
	p2 := &mockRouterProvider{name: "ok", stream: ch}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 1},
		{Provider: p2, Model: "m2", Priority: 2},
	}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	stream, err := aux.ChatCompletionStream(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	delta := <-stream
	if delta.Content != "stream-fallback" {
		t.Errorf("delta content = %q, want stream-fallback", delta.Content)
	}
}

func TestAuxiliaryClient_ChatCompletionStream_AbortOnAuth(t *testing.T) {
	p1 := &mockRouterProvider{name: "auth", err: fmt.Errorf("401 unauthorized")}
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
		t.Fatal("expected error for auth failure in stream")
	}
}

func TestAuxiliaryClient_ChatCompletionStream_AllFail(t *testing.T) {
	p := &mockRouterProvider{name: "dead", err: fmt.Errorf("429 rate limit")}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	_, err := aux.ChatCompletionStream(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error when all providers fail in stream")
	}
}

func TestAuxiliaryClient_ChatCompletionStream_AllUnhealthy(t *testing.T) {
	p := &mockRouterProvider{name: "dead"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()
	r.MarkHealthy("dead", false)

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	_, err := aux.ChatCompletionStream(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error when all unhealthy in stream")
	}
}
