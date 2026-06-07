package testutil

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"nexus-agent/internal/llm"
)

func TestMockProviderName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setName  string
		expected string
	}{
		{"default", "", "mock"},
		{"custom", "openai", "openai"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &MockProvider{Name_: tt.setName}
			if got := m.Name(); got != tt.expected {
				t.Errorf("Name() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestMockProviderCreateChatCompletion(t *testing.T) {
	t.Parallel()

	t.Run("default response", func(t *testing.T) {
		t.Parallel()
		m := &MockProvider{}
		resp, err := m.CreateChatCompletion(context.Background(), &llm.ChatRequest{Model: "test-model"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Content != "mock response" {
			t.Errorf("Content = %q, want %q", resp.Content, "mock response")
		}
		if resp.Model != "test-model" {
			t.Errorf("Model = %q, want %q", resp.Model, "test-model")
		}
		if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
			t.Errorf("Usage.TotalTokens = %v, want 15", resp.Usage)
		}
	})

	t.Run("records request", func(t *testing.T) {
		t.Parallel()
		m := &MockProvider{}
		req := &llm.ChatRequest{Model: "gpt-4", Messages: []llm.Message{{Role: "user", Content: "hello"}}}
		_, _ = m.CreateChatCompletion(context.Background(), req)
		if len(m.ChatRequests) != 1 {
			t.Fatalf("ChatRequests len = %d, want 1", len(m.ChatRequests))
		}
		if m.ChatRequests[0].Model != "gpt-4" {
			t.Errorf("ChatRequests[0].Model = %q, want %q", m.ChatRequests[0].Model, "gpt-4")
		}
	})

	t.Run("preset error", func(t *testing.T) {
		t.Parallel()
		m := &MockProvider{ChatError: fmt.Errorf("api error")}
		_, err := m.CreateChatCompletion(context.Background(), &llm.ChatRequest{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if err.Error() != "api error" {
			t.Errorf("error = %q, want %q", err.Error(), "api error")
		}
	})

	t.Run("preset response", func(t *testing.T) {
		t.Parallel()
		preset := &llm.ChatResponse{ID: "custom-id", Content: "custom content"}
		m := &MockProvider{ChatResponse: preset}
		resp, err := m.CreateChatCompletion(context.Background(), &llm.ChatRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.ID != "custom-id" {
			t.Errorf("ID = %q, want %q", resp.ID, "custom-id")
		}
	})

	t.Run("custom func overrides preset", func(t *testing.T) {
		t.Parallel()
		m := &MockProvider{
			ChatResponse: &llm.ChatResponse{Content: "preset"},
			CreateChatCompletionFunc: func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
				return &llm.ChatResponse{Content: "custom func"}, nil
			},
		}
		resp, err := m.CreateChatCompletion(context.Background(), &llm.ChatRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Content != "custom func" {
			t.Errorf("Content = %q, want %q", resp.Content, "custom func")
		}
	})
}

func TestMockProviderCreateChatCompletionStream(t *testing.T) {
	t.Parallel()

	t.Run("preset deltas", func(t *testing.T) {
		t.Parallel()
		deltas := []*llm.StreamDelta{
			{Content: "hello "},
			{Content: "world"},
		}
		m := &MockProvider{StreamDeltas: deltas}
		ch, err := m.CreateChatCompletionStream(context.Background(), &llm.ChatRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var contents []string
		for delta := range ch {
			if delta.Done {
				break
			}
			contents = append(contents, delta.Content)
		}
		if len(contents) != 2 {
			t.Fatalf("got %d deltas, want 2", len(contents))
		}
		if contents[0] != "hello " || contents[1] != "world" {
			t.Errorf("contents = %v, want [hello  world]", contents)
		}
	})

	t.Run("records request", func(t *testing.T) {
		t.Parallel()
		m := &MockProvider{}
		ch, _ := m.CreateChatCompletionStream(context.Background(), &llm.ChatRequest{Model: "stream-model"})
		// Drain channel
		for range ch {
		}
		if len(m.StreamRequests) != 1 {
			t.Fatalf("StreamRequests len = %d, want 1", len(m.StreamRequests))
		}
		if m.StreamRequests[0].Model != "stream-model" {
			t.Errorf("StreamRequests[0].Model = %q", m.StreamRequests[0].Model)
		}
	})

	t.Run("preset stream error", func(t *testing.T) {
		t.Parallel()
		m := &MockProvider{StreamError: fmt.Errorf("stream failed")}
		_, err := m.CreateChatCompletionStream(context.Background(), &llm.ChatRequest{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestMockProviderListModels(t *testing.T) {
	t.Parallel()

	t.Run("default empty list", func(t *testing.T) {
		t.Parallel()
		m := &MockProvider{}
		models, err := m.ListModels(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(models) != 0 {
			t.Errorf("expected empty models, got %d", len(models))
		}
	})

	t.Run("preset models", func(t *testing.T) {
		t.Parallel()
		m := &MockProvider{
			Models: []llm.ModelInfo{
				{ID: "gpt-4"},
				{ID: "gpt-3.5-turbo"},
			},
		}
		models, err := m.ListModels(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(models) != 2 {
			t.Fatalf("expected 2 models, got %d", len(models))
		}
		if models[0].ID != "gpt-4" {
			t.Errorf("models[0].ID = %q, want %q", models[0].ID, "gpt-4")
		}
	})

	t.Run("preset error", func(t *testing.T) {
		t.Parallel()
		m := &MockProvider{ListModelsError: fmt.Errorf("network error")}
		_, err := m.ListModels(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestMockProviderReset(t *testing.T) {
	t.Parallel()
	m := &MockProvider{}
	_, _ = m.CreateChatCompletion(context.Background(), &llm.ChatRequest{})
	_, _ = m.CreateChatCompletionStream(context.Background(), &llm.ChatRequest{})
	m.Reset()
	if len(m.ChatRequests) != 0 {
		t.Errorf("ChatRequests not cleared after Reset")
	}
	if len(m.StreamRequests) != 0 {
		t.Errorf("StreamRequests not cleared after Reset")
	}
}

func TestMockProviderSetChatResponse(t *testing.T) {
	t.Parallel()
	m := &MockProvider{ChatError: fmt.Errorf("err")}
	m.SetChatResponse(&llm.ChatResponse{Content: "new"})
	if m.ChatError != nil {
		t.Error("SetChatResponse should clear ChatError")
	}
	if m.ChatResponse.Content != "new" {
		t.Errorf("ChatResponse.Content = %q, want %q", m.ChatResponse.Content, "new")
	}
}

func TestMockProviderSetChatError(t *testing.T) {
	t.Parallel()
	m := &MockProvider{}
	err := fmt.Errorf("new error")
	m.SetChatError(err)
	if m.ChatError == nil {
		t.Error("SetChatError should set ChatError")
	}
}

func TestMockProviderSetStreamDeltas(t *testing.T) {
	t.Parallel()
	m := &MockProvider{StreamError: fmt.Errorf("err")}
	deltas := []*llm.StreamDelta{{Content: "a"}}
	m.SetStreamDeltas(deltas)
	if m.StreamError != nil {
		t.Error("SetStreamDeltas should clear StreamError")
	}
	if len(m.StreamDeltas) != 1 {
		t.Errorf("StreamDeltas len = %d, want 1", len(m.StreamDeltas))
	}
}

func TestMockProviderSetStreamError(t *testing.T) {
	t.Parallel()
	m := &MockProvider{}
	m.SetStreamError(fmt.Errorf("stream err"))
	if m.StreamError == nil {
		t.Error("SetStreamError should set StreamError")
	}
}

func TestMockProviderConcurrency(t *testing.T) {
	m := &MockProvider{}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.CreateChatCompletion(context.Background(), &llm.ChatRequest{})
		}()
	}
	wg.Wait()
	if len(m.ChatRequests) != 10 {
		t.Errorf("expected 10 requests, got %d", len(m.ChatRequests))
	}
}
