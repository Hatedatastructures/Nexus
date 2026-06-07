package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"nexus-agent/internal/llm"
)

// ───────────────────────── tryFallback (legacy) ─────────────────────────

func TestTryFallback_NilProvider(t *testing.T) {
	a := NewAgent()
	resp, err := a.tryFallback(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Error("expected nil response with nil fallback provider")
	}
}

func TestTryFallback_Success(t *testing.T) {
	p := &mockRouterProvider{
		name: "legacy-fallback",
		resp: &llm.ChatResponse{Content: "legacy-ok"},
	}
	a := NewAgent(WithFallbackProvider(p), WithFallbackModel("fallback-model"))

	req := &llm.ChatRequest{Model: "original"}
	resp, err := a.tryFallback(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "legacy-ok" {
		t.Errorf("content = %q, want legacy-ok", resp.Content)
	}
	if req.Model != "original" {
		t.Errorf("request model should be restored, got %q", req.Model)
	}
}

// ───────────────────────── recoverUnhealthyProviders ─────────────────────────

func TestRecoverUnhealthyProviders_NoRouter(t *testing.T) {
	a := NewAgent()
	a.recoverUnhealthyProviders(context.Background())
}

func TestRecoverUnhealthyProviders_HealthySkipped(t *testing.T) {
	p := &mockRouterProvider{name: "healthy", models: []llm.ModelInfo{{ID: "m"}}}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	a := NewAgent(WithRouter(r))
	a.recoverUnhealthyProviders(context.Background())
}

func TestRecoverUnhealthyProviders_RecentErrorSkipped(t *testing.T) {
	p := &mockRouterProvider{name: "recent-err", models: []llm.ModelInfo{{ID: "m"}}}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	r.MarkHealthy("recent-err", false)

	a := NewAgent(WithRouter(r))
	a.recoverUnhealthyProviders(context.Background())

	_, err := r.GetHealthyProvider()
	if err == nil {
		t.Error("recent error should not be recovered yet")
	}
}

func TestRecoverUnhealthyProviders_SuccessfulRecovery(t *testing.T) {
	p := &mockRouterProvider{name: "old-err", models: []llm.ModelInfo{{ID: "m"}}}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	cfg := &ProviderRouterConfig{HealthInterval: 0, HealthTimeout: 5 * time.Second}
	r := newProviderRouter(entries, cfg)
	defer r.Stop()

	r.MarkHealthy("old-err", false)
	for _, e := range r.GetEntries() {
		e.LastErr.Store(time.Now().Add(-6 * time.Minute))
	}

	a := NewAgent(WithRouter(r))
	a.recoverUnhealthyProviders(context.Background())

	provider, err := r.GetHealthyProvider()
	if err != nil {
		t.Fatalf("expected recovery, got %v", err)
	}
	if provider.Name() != "old-err" {
		t.Errorf("provider = %q, want old-err", provider.Name())
	}
}

func TestRecoverUnhealthyProviders_RecoveryFails(t *testing.T) {
	p := &mockRouterProvider{name: "still-sick", err: fmt.Errorf("connection refused")}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	cfg := &ProviderRouterConfig{HealthInterval: 0, HealthTimeout: 5 * time.Second}
	r := newProviderRouter(entries, cfg)
	defer r.Stop()

	r.MarkHealthy("still-sick", false)
	for _, e := range r.GetEntries() {
		e.LastErr.Store(time.Now().Add(-6 * time.Minute))
	}

	a := NewAgent(WithRouter(r))
	a.recoverUnhealthyProviders(context.Background())

	_, err := r.GetHealthyProvider()
	if err == nil {
		t.Error("provider should still be unhealthy after failed recovery")
	}
}
