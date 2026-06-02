package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"nexus-agent/internal/llm"
)

// ───────────────────────── mock provider ─────────────────────────

type mockRouterProvider struct {
	name    string
	resp    *llm.ChatResponse
	stream  <-chan *llm.StreamDelta
	err     error
	models  []llm.ModelInfo
	modelCh chan struct{}
}

func (m *mockRouterProvider) CreateChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return m.resp, m.err
}

func (m *mockRouterProvider) CreateChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan *llm.StreamDelta, error) {
	return m.stream, m.err
}

func (m *mockRouterProvider) ListModels(_ context.Context) ([]llm.ModelInfo, error) {
	if m.modelCh != nil {
		<-m.modelCh
	}
	return m.models, m.err
}

func (m *mockRouterProvider) Name() string { return m.name }

// ───────────────────────── NewProviderRouter ─────────────────────────

func TestNewProviderRouter_Empty(t *testing.T) {
	r := NewProviderRouter(nil)
	if r == nil {
		t.Fatal("router is nil")
	}
	r.Stop()
}

func TestNewProviderRouter_Sorting(t *testing.T) {
	p1 := &mockRouterProvider{name: "low"}
	p2 := &mockRouterProvider{name: "high"}
	p3 := &mockRouterProvider{name: "mid"}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 10},
		{Provider: p2, Model: "m2", Priority: 1},
		{Provider: p3, Model: "m3", Priority: 5},
	}

	r := NewProviderRouter(entries)
	defer r.Stop()

	ordered := r.GetEntries()
	if ordered[0].Provider.Name() != "high" {
		t.Errorf("first should be high priority, got %s", ordered[0].Provider.Name())
	}
	if ordered[1].Provider.Name() != "mid" {
		t.Errorf("second should be mid priority, got %s", ordered[1].Provider.Name())
	}
	if ordered[2].Provider.Name() != "low" {
		t.Errorf("third should be low priority, got %s", ordered[2].Provider.Name())
	}
}

func TestNewProviderRouter_InitialHealthy(t *testing.T) {
	p := &mockRouterProvider{name: "test"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}

	r := NewProviderRouter(entries)
	defer r.Stop()

	for _, e := range r.GetEntries() {
		if !e.Healthy.Load() {
			t.Error("entries should start healthy")
		}
	}
}

// ───────────────────────── NewProviderRouterWithConfig ─────────────────────────

func TestNewProviderRouterWithConfig(t *testing.T) {
	p := &mockRouterProvider{name: "cfg-test"}
	cfgEntries := []*ProviderRouterConfigEntry{
		{Provider: p, Model: "model-a", Priority: 1},
	}

	cfg := &ProviderRouterConfig{
		HealthInterval: 0,
		HealthTimeout:  5 * time.Second,
	}

	r := NewProviderRouterWithConfig(cfgEntries, cfg)
	defer r.Stop()

	entries := r.GetEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Model != "model-a" {
		t.Errorf("model = %q, want model-a", entries[0].Model)
	}
}

func TestNewProviderRouterWithConfig_NilConfig(t *testing.T) {
	p := &mockRouterProvider{name: "nil-cfg"}
	cfgEntries := []*ProviderRouterConfigEntry{
		{Provider: p, Model: "m", Priority: 1},
	}

	r := NewProviderRouterWithConfig(cfgEntries, nil)
	defer r.Stop()

	if r.healthInterval != 5*time.Minute {
		t.Errorf("expected default health interval, got %v", r.healthInterval)
	}
}

func TestDefaultProviderRouterConfig(t *testing.T) {
	cfg := DefaultProviderRouterConfig()
	if cfg.HealthInterval != 5*time.Minute {
		t.Errorf("HealthInterval = %v, want 5m", cfg.HealthInterval)
	}
	if cfg.HealthTimeout != 30*time.Second {
		t.Errorf("HealthTimeout = %v, want 30s", cfg.HealthTimeout)
	}
}

// ───────────────────────── ChatCompletion ─────────────────────────

func TestProviderRouter_ChatCompletion_Success(t *testing.T) {
	p := &mockRouterProvider{
		name: "ok-provider",
		resp: &llm.ChatResponse{Content: "hello"},
	}
	entries := []*ProviderEntry{{Provider: p, Model: "gpt-4", Priority: 1}}

	r := NewProviderRouter(entries)
	defer r.Stop()

	resp, err := r.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("content = %q, want hello", resp.Content)
	}
}

func TestProviderRouter_ChatCompletion_Fallback(t *testing.T) {
	p1 := &mockRouterProvider{name: "fail", err: fmt.Errorf("429 too many requests: rate limit exceeded")}
	p2 := &mockRouterProvider{name: "ok", resp: &llm.ChatResponse{Content: "fallback"}}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 1},
		{Provider: p2, Model: "m2", Priority: 2},
	}

	r := NewProviderRouter(entries)
	defer r.Stop()

	resp, err := r.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Content != "fallback" {
		t.Errorf("content = %q, want fallback", resp.Content)
	}
}

func TestProviderRouter_ChatCompletion_UnrecoverableError(t *testing.T) {
	p := &mockRouterProvider{
		name: "auth-fail",
		err:  fmt.Errorf("401 unauthorized: invalid api key"),
	}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}

	r := NewProviderRouter(entries)
	defer r.Stop()

	_, err := r.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error for auth failure")
	}
}

func TestProviderRouter_ChatCompletion_ContextOverflowNoFallback(t *testing.T) {
	p1 := &mockRouterProvider{
		name: "overflow",
		err:  fmt.Errorf("400 bad request: context length exceeded"),
	}
	p2 := &mockRouterProvider{
		name: "should-not-reach",
		resp: &llm.ChatResponse{Content: "oops"},
	}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 1},
		{Provider: p2, Model: "m2", Priority: 2},
	}

	r := NewProviderRouter(entries)
	defer r.Stop()

	_, err := r.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("context overflow should not fallback, should return error")
	}
}

func TestProviderRouter_ChatCompletion_AllUnhealthy(t *testing.T) {
	p := &mockRouterProvider{name: "dead"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}

	r := NewProviderRouter(entries)
	defer r.Stop()

	r.MarkHealthy("dead", false)

	_, err := r.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error when all providers unhealthy")
	}
}

// ───────────────────────── ChatCompletionStream ─────────────────────────

func TestProviderRouter_ChatCompletionStream_Success(t *testing.T) {
	ch := make(chan *llm.StreamDelta, 1)
	ch <- &llm.StreamDelta{Content: "stream-data", Done: true}
	close(ch)

	p := &mockRouterProvider{name: "stream-ok", stream: ch}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}

	r := NewProviderRouter(entries)
	defer r.Stop()

	stream, err := r.ChatCompletionStream(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	delta := <-stream
	if delta.Content != "stream-data" {
		t.Errorf("delta content = %q, want stream-data", delta.Content)
	}
}

func TestProviderRouter_ChatCompletionStream_Fallback(t *testing.T) {
	p1 := &mockRouterProvider{name: "fail", err: fmt.Errorf("429 too many requests: rate limit exceeded")}
	ch := make(chan *llm.StreamDelta, 1)
	ch <- &llm.StreamDelta{Content: "ok", Done: true}
	close(ch)
	p2 := &mockRouterProvider{name: "ok", stream: ch}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 1},
		{Provider: p2, Model: "m2", Priority: 2},
	}

	r := NewProviderRouter(entries)
	defer r.Stop()

	stream, err := r.ChatCompletionStream(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatCompletionStream fallback: %v", err)
	}
	delta := <-stream
	if delta.Content != "ok" {
		t.Errorf("delta content = %q, want ok", delta.Content)
	}
}

func TestProviderRouter_ChatCompletionStream_AllFail(t *testing.T) {
	p := &mockRouterProvider{name: "fail", err: fmt.Errorf("500 error")}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}

	r := NewProviderRouter(entries)
	defer r.Stop()

	_, err := r.ChatCompletionStream(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error when stream fails")
	}
}

// ───────────────────────── MarkHealthy / GetHealthyProvider ─────────────────────────

func TestProviderRouter_MarkHealthy(t *testing.T) {
	p := &mockRouterProvider{name: "test"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}

	r := NewProviderRouter(entries)
	defer r.Stop()

	r.MarkHealthy("test", false)

	_, err := r.GetHealthyProvider()
	if err == nil {
		t.Error("expected error when no healthy provider")
	}

	r.MarkHealthy("test", true)
	provider, err := r.GetHealthyProvider()
	if err != nil {
		t.Fatalf("GetHealthyProvider: %v", err)
	}
	if provider.Name() != "test" {
		t.Errorf("provider name = %q, want test", provider.Name())
	}
}

func TestProviderRouter_MarkHealthy_UnknownProvider(t *testing.T) {
	p := &mockRouterProvider{name: "known"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}

	r := NewProviderRouter(entries)
	defer r.Stop()

	r.MarkHealthy("unknown", false)
}

func TestProviderRouter_GetHealthyProvider_Multiple(t *testing.T) {
	p1 := &mockRouterProvider{name: "p1"}
	p2 := &mockRouterProvider{name: "p2"}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 1},
		{Provider: p2, Model: "m2", Priority: 2},
	}

	r := NewProviderRouter(entries)
	defer r.Stop()

	provider, err := r.GetHealthyProvider()
	if err != nil {
		t.Fatalf("GetHealthyProvider: %v", err)
	}
	if provider.Name() != "p1" {
		t.Errorf("should return highest priority, got %s", provider.Name())
	}
}

func TestProviderRouter_GetEntries(t *testing.T) {
	p1 := &mockRouterProvider{name: "p1"}
	p2 := &mockRouterProvider{name: "p2"}

	entries := []*ProviderEntry{
		{Provider: p1, Model: "m1", Priority: 1},
		{Provider: p2, Model: "m2", Priority: 2},
	}

	r := NewProviderRouter(entries)
	defer r.Stop()

	result := r.GetEntries()
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
}

// ───────────────────────── Stop ─────────────────────────

func TestProviderRouter_Stop_Idempotent(t *testing.T) {
	p := &mockRouterProvider{name: "test"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	cfg := &ProviderRouterConfig{HealthInterval: 0, HealthTimeout: time.Second}
	r := newProviderRouter(entries, cfg)

	r.Stop()
	r.Stop()
}

// ───────────────────────── shouldFallback ─────────────────────────

func TestShouldFallback_Nil(t *testing.T) {
	r := &ProviderRouter{}
	if r.shouldFallback(nil) {
		t.Error("nil error should not fallback")
	}
}

func TestShouldFallback_RateLimit(t *testing.T) {
	r := &ProviderRouter{}
	err := fmt.Errorf("429 too many requests: rate limit exceeded")
	if !r.shouldFallback(err) {
		t.Error("rate limit should fallback")
	}
}

func TestShouldFallback_ContextOverflow(t *testing.T) {
	r := &ProviderRouter{}
	err := fmt.Errorf("400 context length exceeded")
	if r.shouldFallback(err) {
		t.Error("context overflow should not fallback")
	}
}

func TestShouldFallback_FormatError(t *testing.T) {
	r := &ProviderRouter{}
	err := fmt.Errorf("400 bad request: invalid_request")
	if r.shouldFallback(err) {
		t.Error("format error should not fallback")
	}
}

func TestShouldFallback_Auth(t *testing.T) {
	r := &ProviderRouter{}
	err := fmt.Errorf("401 unauthorized: invalid api key")
	if !r.shouldFallback(err) {
		t.Error("auth error should fallback")
	}
}

// ───────────────────────── RetryDelay ─────────────────────────

func TestRetryDelay(t *testing.T) {
	tests := []struct {
		code int
		want time.Duration
	}{
		{429, 10 * time.Second},
		{500, 5 * time.Second},
		{502, 5 * time.Second},
		{503, 15 * time.Second},
		{529, 15 * time.Second},
		{200, 0},
		{400, 0},
		{404, 0},
	}
	for _, tt := range tests {
		got := RetryDelay(tt.code)
		if got != tt.want {
			t.Errorf("RetryDelay(%d) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

// ───────────────────────── ExponentialBackoff ─────────────────────────

func TestExponentialBackoff_Basic(t *testing.T) {
	base := time.Second
	max := 30 * time.Second

	d0 := ExponentialBackoff(0, base, max)
	if d0 != base {
		t.Errorf("attempt 0: got %v, want %v", d0, base)
	}

	d1 := ExponentialBackoff(1, base, max)
	if d1 != 2*base {
		t.Errorf("attempt 1: got %v, want %v", d1, 2*base)
	}

	d2 := ExponentialBackoff(2, base, max)
	if d2 != 4*base {
		t.Errorf("attempt 2: got %v, want %v", d2, 4*base)
	}
}

func TestExponentialBackoff_MaxCap(t *testing.T) {
	base := time.Second
	max := 5 * time.Second

	got := ExponentialBackoff(10, base, max)
	if got != max {
		t.Errorf("should be capped at max, got %v", got)
	}
}

func TestExponentialBackoff_ZeroMax(t *testing.T) {
	got := ExponentialBackoff(5, time.Second, 0)
	if got != 30*time.Second {
		t.Errorf("zero max should use default 30s, got %v", got)
	}
}

func TestExponentialBackoff_NegativeMax(t *testing.T) {
	got := ExponentialBackoff(5, time.Second, -1)
	if got != 30*time.Second {
		t.Errorf("negative max should use default 30s, got %v", got)
	}
}

// ───────────────────────── noHealthyProviderError ─────────────────────────

func TestNoHealthyProviderError(t *testing.T) {
	err := &noHealthyProviderError{}
	if err.Error() == "" {
		t.Error("error message should not be empty")
	}
}

// ───────────────────────── healthCheckLoop (integration) ─────────────────────────

func TestProviderRouter_HealthCheckRestores(t *testing.T) {
	modelCh := make(chan struct{})
	p := &mockRouterProvider{
		name:    "sick",
		models:  []llm.ModelInfo{{ID: "m1"}},
		modelCh: modelCh,
	}

	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	cfg := &ProviderRouterConfig{
		HealthInterval: 50 * time.Millisecond,
		HealthTimeout:  2 * time.Second,
	}

	r := newProviderRouter(entries, cfg)
	defer r.Stop()

	r.MarkHealthy("sick", false)
	close(modelCh)

	time.Sleep(200 * time.Millisecond)

	provider, err := r.GetHealthyProvider()
	if err != nil {
		t.Fatalf("health check should have restored provider: %v", err)
	}
	if provider.Name() != "sick" {
		t.Errorf("provider name = %q, want sick", provider.Name())
	}
}

func TestProviderRouter_HealthCheck_Fails(t *testing.T) {
	p := &mockRouterProvider{
		name: "still-sick",
		err:  errors.New("connection refused"),
	}

	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	cfg := &ProviderRouterConfig{
		HealthInterval: 50 * time.Millisecond,
		HealthTimeout:  2 * time.Second,
	}

	r := newProviderRouter(entries, cfg)
	defer r.Stop()

	r.MarkHealthy("still-sick", false)
	time.Sleep(200 * time.Millisecond)

	_, err := r.GetHealthyProvider()
	if err == nil {
		t.Error("provider should still be unhealthy")
	}
}

func TestProviderRouter_RequestCopyNotMutated(t *testing.T) {
	p := &mockRouterProvider{name: "model-check"}
	p.resp = &llm.ChatResponse{Content: "ok"}

	entries := []*ProviderEntry{{Provider: p, Model: "router-model", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	origReq := &llm.ChatRequest{Model: "original"}
	_, err := r.ChatCompletion(context.Background(), origReq)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if origReq.Model != "original" {
		t.Errorf("original request model mutated: got %q, want original", origReq.Model)
	}
}
