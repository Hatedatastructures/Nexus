package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"nexus-agent/internal/llm"
)

// ───────────────────────── NewFallbackChain ─────────────────────────

func TestNewFallbackChain_Empty(t *testing.T) {
	fc := NewFallbackChain(nil, nil)
	if fc == nil {
		t.Fatal("expected non-nil FallbackChain")
	}
	if len(fc.entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(fc.entries))
	}
}

func TestNewFallbackChain_SortsByPriority(t *testing.T) {
	p1 := &mockRouterProvider{name: "low"}
	p2 := &mockRouterProvider{name: "high"}
	p3 := &mockRouterProvider{name: "mid"}

	entries := []*FallbackEntry{
		{Provider: "low", Model: "m1", Priority: 10},
		{Provider: "high", Model: "m2", Priority: 1},
		{Provider: "mid", Model: "m3", Priority: 5},
	}
	providerMap := map[string]llm.Provider{
		"low":  p1,
		"high": p2,
		"mid":  p3,
	}

	fc := NewFallbackChain(entries, providerMap)
	if len(fc.entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(fc.entries))
	}
	if fc.entries[0].provider.Name() != "high" {
		t.Errorf("first should be high priority, got %s", fc.entries[0].provider.Name())
	}
	if fc.entries[1].provider.Name() != "mid" {
		t.Errorf("second should be mid priority, got %s", fc.entries[1].provider.Name())
	}
	if fc.entries[2].provider.Name() != "low" {
		t.Errorf("third should be low priority, got %s", fc.entries[2].provider.Name())
	}
}

func TestNewFallbackChain_SkipsUnknownProvider(t *testing.T) {
	p := &mockRouterProvider{name: "known"}
	entries := []*FallbackEntry{
		{Provider: "known", Model: "m1", Priority: 1},
		{Provider: "unknown", Model: "m2", Priority: 2},
	}
	providerMap := map[string]llm.Provider{
		"known": p,
	}

	fc := NewFallbackChain(entries, providerMap)
	if len(fc.entries) != 1 {
		t.Fatalf("expected 1 entry (unknown skipped), got %d", len(fc.entries))
	}
	if fc.entries[0].provider.Name() != "known" {
		t.Errorf("expected known provider, got %s", fc.entries[0].provider.Name())
	}
}

// ───────────────────────── shouldFallback ─────────────────────────

func TestFallbackShouldFallback_NilError(t *testing.T) {
	if shouldFallback(nil) {
		t.Error("nil error should not fallback")
	}
}

func TestFallbackShouldFallback_BillingError(t *testing.T) {
	err := fmt.Errorf("402 insufficient credits: payment required")
	if shouldFallback(err) {
		t.Error("billing error should not fallback")
	}
}

func TestFallbackShouldFallback_ContextOverflow(t *testing.T) {
	err := fmt.Errorf("400 context length exceeded")
	if shouldFallback(err) {
		t.Error("context overflow should not fallback")
	}
}

func TestFallbackShouldFallback_FormatError(t *testing.T) {
	err := fmt.Errorf("400 bad request: invalid_request")
	if shouldFallback(err) {
		t.Error("format error should not fallback")
	}
}

func TestFallbackShouldFallback_RateLimit(t *testing.T) {
	err := fmt.Errorf("429 too many requests: rate limit exceeded")
	if !shouldFallback(err) {
		t.Error("rate limit should fallback")
	}
}

func TestFallbackShouldFallback_ServerError(t *testing.T) {
	err := fmt.Errorf("500 internal server error")
	if !shouldFallback(err) {
		t.Error("server error should fallback")
	}
}

func TestFallbackShouldFallback_AuthError(t *testing.T) {
	err := fmt.Errorf("401 unauthorized: invalid api key")
	if !shouldFallback(err) {
		t.Error("auth error should fallback")
	}
}

func TestFallbackShouldFallback_Overloaded(t *testing.T) {
	err := fmt.Errorf("503 service overloaded")
	if !shouldFallback(err) {
		t.Error("overloaded should fallback")
	}
}

// ───────────────────────── isBillingError ─────────────────────────

func TestIsBillingError_Nil(t *testing.T) {
	if isBillingError(nil) {
		t.Error("nil error should not be billing")
	}
}

func TestIsBillingError_True(t *testing.T) {
	err := fmt.Errorf("402 insufficient credits: payment required")
	if !isBillingError(err) {
		t.Error("should detect billing error")
	}
}

func TestIsBillingError_False(t *testing.T) {
	err := fmt.Errorf("429 rate limit exceeded")
	if isBillingError(err) {
		t.Error("rate limit is not billing error")
	}
}

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
