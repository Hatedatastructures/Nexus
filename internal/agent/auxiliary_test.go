package agent

import (
	"context"
	"errors"
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

// ───────────────────────── classifyErrorAction ─────────────────────────

func TestClassifyErrorAction_Nil(t *testing.T) {
	aux := &AuxiliaryClient{}
	if aux.classifyErrorAction(nil) != actionRetry {
		t.Error("nil error should return actionRetry")
	}
}

func TestClassifyErrorAction_Billing(t *testing.T) {
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("402 insufficient credits")
	if aux.classifyErrorAction(err) != actionImmediateFallback {
		t.Error("billing error should return actionImmediateFallback")
	}
}

func TestClassifyErrorAction_Auth(t *testing.T) {
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("401 unauthorized: invalid api key")
	if aux.classifyErrorAction(err) != actionAbort {
		t.Error("auth error should return actionAbort")
	}
}

func TestClassifyErrorAction_RateLimit(t *testing.T) {
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("429 rate limit exceeded")
	if aux.classifyErrorAction(err) != actionRetryThenFallback {
		t.Error("rate limit should return actionRetryThenFallback")
	}
}

func TestClassifyErrorAction_ServerError(t *testing.T) {
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("500 internal server error")
	if aux.classifyErrorAction(err) != actionRetryThenFallback {
		t.Error("server error should return actionRetryThenFallback")
	}
}

func TestClassifyErrorAction_Overloaded(t *testing.T) {
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("503 service unavailable")
	if aux.classifyErrorAction(err) != actionRetryThenFallback {
		t.Error("overloaded should return actionRetryThenFallback")
	}
}

func TestClassifyErrorAction_ContextOverflow(t *testing.T) {
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("400 context length exceeded")
	if aux.classifyErrorAction(err) != actionAbort {
		t.Error("context overflow should return actionAbort")
	}
}

func TestClassifyErrorAction_FormatError(t *testing.T) {
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("400 bad request: invalid_request")
	if aux.classifyErrorAction(err) != actionAbort {
		t.Error("format error should return actionAbort")
	}
}

func TestClassifyErrorAction_Timeout(t *testing.T) {
	aux := &AuxiliaryClient{}
	err := errors.New("context deadline exceeded")
	action := aux.classifyErrorAction(err)
	if action != actionImmediateFallback {
		t.Errorf("unknown error should return actionImmediateFallback, got %d", action)
	}
}

func TestClassifyErrorAction_ModelNotFound(t *testing.T) {
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("404 model not found")
	if aux.classifyErrorAction(err) != actionImmediateFallback {
		t.Error("model not found should return actionImmediateFallback")
	}
}

func TestClassifyErrorAction_Unknown(t *testing.T) {
	aux := &AuxiliaryClient{}
	err := errors.New("something completely unexpected")
	if aux.classifyErrorAction(err) != actionImmediateFallback {
		t.Error("unknown error should return actionImmediateFallback")
	}
}

// ───────────────────────── OpenRouterCompatibleClient ─────────────────────────

func TestNewOpenRouterCompatibleClient(t *testing.T) {
	p := &mockRouterProvider{name: "or"}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	or := NewOpenRouterCompatibleClient(aux)
	if or == nil {
		t.Fatal("OpenRouterCompatibleClient is nil")
	}
}

func TestOpenRouterCompatibleClient_Chat(t *testing.T) {
	p := &mockRouterProvider{name: "or", resp: &llm.ChatResponse{Content: "or-chat"}}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	or := NewOpenRouterCompatibleClient(aux)

	resp, err := or.Chat(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "or-chat" {
		t.Errorf("content = %q, want or-chat", resp.Content)
	}
}

func TestOpenRouterCompatibleClient_ChatStream(t *testing.T) {
	ch := make(chan *llm.StreamDelta, 1)
	ch <- &llm.StreamDelta{Content: "or-stream", Done: true}
	close(ch)

	p := &mockRouterProvider{name: "or", stream: ch}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	or := NewOpenRouterCompatibleClient(aux)

	stream, err := or.ChatStream(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	delta := <-stream
	if delta.Content != "or-stream" {
		t.Errorf("delta content = %q, want or-stream", delta.Content)
	}
}

func TestOpenRouterCompatibleClient_WithFallbackModel(t *testing.T) {
	p := &mockRouterProvider{name: "or", resp: &llm.ChatResponse{Content: "ok"}}
	entries := []*ProviderEntry{{Provider: p, Model: "m", Priority: 1}}
	r := NewProviderRouter(entries)
	defer r.Stop()

	aux := NewAuxiliaryClientWithConfig(r, &AuxiliaryClientConfig{RetryCount: 0})
	or := NewOpenRouterCompatibleClient(aux)

	applyFn := or.WithFallbackModel("primary-model", "fallback-model")
	req := &llm.ChatRequest{}
	applyFn(req)
	if req.Model != "primary-model" {
		t.Errorf("model = %q, want primary-model", req.Model)
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

func TestClassifyErrorAction_ThinkingSignature(t *testing.T) {
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("400 invalid signature for thinking block")
	if aux.classifyErrorAction(err) != actionAbort {
		t.Error("thinking signature format error should return actionAbort")
	}
}
