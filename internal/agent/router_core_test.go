package agent

import (
	"context"
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
