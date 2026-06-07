package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"nexus-agent/internal/llm"
)

// ───────────────────────── classifyErrorAction ─────────────────────────

func TestClassifyErrorAction_Nil(t *testing.T) {
	t.Parallel()
	aux := &AuxiliaryClient{}
	if aux.classifyErrorAction(nil) != actionRetry {
		t.Error("nil error should return actionRetry")
	}
}

func TestClassifyErrorAction_Billing(t *testing.T) {
	t.Parallel()
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("402 insufficient credits")
	if aux.classifyErrorAction(err) != actionImmediateFallback {
		t.Error("billing error should return actionImmediateFallback")
	}
}

func TestClassifyErrorAction_Auth(t *testing.T) {
	t.Parallel()
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("401 unauthorized: invalid api key")
	if aux.classifyErrorAction(err) != actionAbort {
		t.Error("auth error should return actionAbort")
	}
}

func TestClassifyErrorAction_RateLimit(t *testing.T) {
	t.Parallel()
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("429 rate limit exceeded")
	if aux.classifyErrorAction(err) != actionRetryThenFallback {
		t.Error("rate limit should return actionRetryThenFallback")
	}
}

func TestClassifyErrorAction_ServerError(t *testing.T) {
	t.Parallel()
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("500 internal server error")
	if aux.classifyErrorAction(err) != actionRetryThenFallback {
		t.Error("server error should return actionRetryThenFallback")
	}
}

func TestClassifyErrorAction_Overloaded(t *testing.T) {
	t.Parallel()
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("503 service unavailable")
	if aux.classifyErrorAction(err) != actionRetryThenFallback {
		t.Error("overloaded should return actionRetryThenFallback")
	}
}

func TestClassifyErrorAction_ContextOverflow(t *testing.T) {
	t.Parallel()
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("400 context length exceeded")
	if aux.classifyErrorAction(err) != actionAbort {
		t.Error("context overflow should return actionAbort")
	}
}

func TestClassifyErrorAction_FormatError(t *testing.T) {
	t.Parallel()
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("400 bad request: invalid_request")
	if aux.classifyErrorAction(err) != actionAbort {
		t.Error("format error should return actionAbort")
	}
}

func TestClassifyErrorAction_Timeout(t *testing.T) {
	t.Parallel()
	aux := &AuxiliaryClient{}
	err := errors.New("context deadline exceeded")
	action := aux.classifyErrorAction(err)
	if action != actionImmediateFallback {
		t.Errorf("unknown error should return actionImmediateFallback, got %d", action)
	}
}

func TestClassifyErrorAction_ModelNotFound(t *testing.T) {
	t.Parallel()
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("404 model not found")
	if aux.classifyErrorAction(err) != actionImmediateFallback {
		t.Error("model not found should return actionImmediateFallback")
	}
}

func TestClassifyErrorAction_Unknown(t *testing.T) {
	t.Parallel()
	aux := &AuxiliaryClient{}
	err := errors.New("something completely unexpected")
	if aux.classifyErrorAction(err) != actionImmediateFallback {
		t.Error("unknown error should return actionImmediateFallback")
	}
}

func TestClassifyErrorAction_ThinkingSignature(t *testing.T) {
	t.Parallel()
	aux := &AuxiliaryClient{}
	err := fmt.Errorf("400 invalid signature for thinking block")
	if aux.classifyErrorAction(err) != actionAbort {
		t.Error("thinking signature format error should return actionAbort")
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
