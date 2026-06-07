package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── CopilotProvider.CreateChatCompletionStream ─────────────────────────────

func TestCopilotProvider_CreateChatCompletionStream_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := NewCopilotProviderWithOptions("tok", server.URL, "gpt-4o", server.Client())
	ch, err := p.CreateChatCompletionStream(context.Background(), &ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletionStream error: %v", err)
	}

	var content string
	for delta := range ch {
		if delta.Error != nil {
			t.Fatalf("stream delta error: %v", delta.Error)
		}
		content += delta.Content
	}
	if !strings.Contains(content, "Hi") {
		t.Errorf("content = %q, should contain Hi", content)
	}
}

func TestCopilotProvider_CreateChatCompletionStream_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal error`))
	}))
	defer server.Close()

	p := NewCopilotProviderWithOptions("tok", server.URL, "gpt-4o", server.Client())
	_, err := p.CreateChatCompletionStream(context.Background(), &ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, should contain 500", err.Error())
	}
}

func TestCopilotProvider_CreateChatCompletionStream_DefaultModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(bodyBytes, &parsed)
		if parsed["model"] != "gpt-4o" {
			t.Errorf("model = %v, want gpt-4o (default)", parsed["model"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := NewCopilotProviderWithOptions("tok", server.URL, "", server.Client())
	ch, err := p.CreateChatCompletionStream(context.Background(), &ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletionStream error: %v", err)
	}
	for range ch {
	}
}

// ── CopilotProvider.ListModels ─────────────────────────────────────────────

func TestCopilotProvider_ListModels_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	p := NewCopilotProviderWithOptions("tok", server.URL, "gpt-4o", server.Client())
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels should not error on fallback, got: %v", err)
	}
	if len(models) != 5 {
		t.Errorf("len = %d, want 5 (default models)", len(models))
	}
}

func TestCopilotProvider_ListModels_Non200Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`forbidden`))
	}))
	defer server.Close()

	p := NewCopilotProviderWithOptions("tok", server.URL, "gpt-4o", server.Client())
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) != 5 {
		t.Errorf("len = %d, want 5 (fallback models on non-200)", len(models))
	}
}

func TestCopilotProvider_ListModels_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer server.Close()

	p := NewCopilotProviderWithOptions("tok", server.URL, "gpt-4o", server.Client())
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) != 5 {
		t.Errorf("len = %d, want 5 (fallback on invalid JSON)", len(models))
	}
}

func TestCopilotProvider_ListModels_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
				"data": [
					{"id": "gpt-4o", "object": "model"},
					{"id": "gpt-4o-mini", "object": "model"},
					{"id": "o3-mini", "object": "model"}
				]
			}`))
	}))
	defer server.Close()

	p := NewCopilotProviderWithOptions("tok", server.URL, "gpt-4o", server.Client())
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("len = %d, want 3", len(models))
	}
	if models[0].ID != "gpt-4o" {
		t.Errorf("models[0].ID = %q, want gpt-4o", models[0].ID)
	}
	for _, m := range models {
		if m.Provider != "copilot" {
			t.Errorf("model %q provider = %q, want copilot", m.ID, m.Provider)
		}
	}
}

func TestCopilotProvider_ListModels_ReadBodyError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`short`))
	}))
	defer server.Close()

	p := NewCopilotProviderWithOptions("tok", server.URL, "gpt-4o", server.Client())
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) != 5 {
		t.Errorf("len = %d, want 5 (fallback on read error)", len(models))
	}
}
