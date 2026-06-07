package agent

import (
	"fmt"
	"testing"

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
