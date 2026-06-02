package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── NewCopilotProvider ──────────────────────────────────────────────────────

func TestNewCopilotProvider_Defaults(t *testing.T) {
	p := NewCopilotProvider("test-token")
	if p.model != copilotDefaultModel {
		t.Errorf("model = %q, want %q", p.model, copilotDefaultModel)
	}
	if p.endpoint != copilotDefaultEndpoint {
		t.Errorf("endpoint = %q, want %q", p.endpoint, copilotDefaultEndpoint)
	}
	if p.token != "test-token" {
		t.Errorf("token = %q, want test-token", p.token)
	}
	if p.httpClient == nil {
		t.Error("expected non-nil httpClient")
	}
	if p.transport == nil {
		t.Error("expected non-nil transport")
	}
}

// ── NewCopilotProviderWithOptions ───────────────────────────────────────────

func TestNewCopilotProviderWithOptions_CustomValues(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	p := NewCopilotProviderWithOptions("tok", "https://custom.host.com/", "gpt-4o-mini", client)
	if p.endpoint != "https://custom.host.com" {
		t.Errorf("endpoint = %q, want https://custom.host.com", p.endpoint)
	}
	if p.model != "gpt-4o-mini" {
		t.Errorf("model = %q, want gpt-4o-mini", p.model)
	}
	if p.httpClient != client {
		t.Error("expected httpClient to be the passed client")
	}
}

func TestNewCopilotProviderWithOptions_NilClient(t *testing.T) {
	p := NewCopilotProviderWithOptions("tok", "", "", nil)
	if p.httpClient == nil {
		t.Error("expected default httpClient when nil passed")
	}
	if p.httpClient.Timeout != 300*time.Second {
		t.Errorf("Timeout = %v, want 300s", p.httpClient.Timeout)
	}
}

func TestNewCopilotProviderWithOptions_EmptyEndpoint(t *testing.T) {
	p := NewCopilotProviderWithOptions("tok", "", "", nil)
	if p.endpoint != copilotDefaultEndpoint {
		t.Errorf("endpoint = %q, want %q", p.endpoint, copilotDefaultEndpoint)
	}
}

func TestNewCopilotProviderWithOptions_EmptyModel(t *testing.T) {
	p := NewCopilotProviderWithOptions("tok", "", "", nil)
	if p.model != copilotDefaultModel {
		t.Errorf("model = %q, want %q", p.model, copilotDefaultModel)
	}
}

// ── CopilotProvider.Name ───────────────────────────────────────────────────

func TestCopilotProvider_Name(t *testing.T) {
	p := NewCopilotProvider("tok")
	if p.Name() != "copilot" {
		t.Errorf("Name() = %q, want copilot", p.Name())
	}
}

// ── CopilotProvider.defaultModels ──────────────────────────────────────────

func TestCopilotProvider_DefaultModels(t *testing.T) {
	p := NewCopilotProvider("tok")
	models := p.defaultModels()
	if len(models) != 5 {
		t.Fatalf("len = %d, want 5", len(models))
	}
	for _, m := range models {
		if m.Provider != "copilot" {
			t.Errorf("model %q provider = %q, want copilot", m.ID, m.Provider)
		}
	}
}

// ── CopilotProvider.setAuthHeaders ─────────────────────────────────────────

func TestCopilotProvider_SetAuthHeaders_WithToken(t *testing.T) {
	p := NewCopilotProvider("my-secret-token")
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	p.setAuthHeaders(req)

	if req.Header.Get("Authorization") != "Bearer my-secret-token" {
		t.Errorf("Authorization = %q, want Bearer my-secret-token", req.Header.Get("Authorization"))
	}
	if req.Header.Get("Editor-Version") != "Nexus/1.0" {
		t.Errorf("Editor-Version = %q, want Nexus/1.0", req.Header.Get("Editor-Version"))
	}
	if req.Header.Get("Copilot-Integration-Id") != "vscode-chat" {
		t.Errorf("Copilot-Integration-Id = %q, want vscode-chat", req.Header.Get("Copilot-Integration-Id"))
	}
}

func TestCopilotProvider_SetAuthHeaders_EmptyToken(t *testing.T) {
	p := NewCopilotProvider("")
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	p.setAuthHeaders(req)

	if req.Header.Get("Authorization") != "" {
		t.Errorf("Authorization should be empty when token is empty, got %q", req.Header.Get("Authorization"))
	}
}

// ── CopilotProvider.buildCopilotRequest ────────────────────────────────────

func TestCopilotProvider_BuildCopilotRequest_NonStream(t *testing.T) {
	p := NewCopilotProvider("tok")
	req := &ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	}

	httpReq, err := p.buildCopilotRequest(context.Background(), req, false)
	if err != nil {
		t.Fatalf("buildCopilotRequest error: %v", err)
	}
	if httpReq.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", httpReq.Method)
	}
	if !strings.HasSuffix(httpReq.URL.String(), "/v1/chat/completions") {
		t.Errorf("URL = %q, should end with /v1/chat/completions", httpReq.URL.String())
	}
	if httpReq.Header.Get("Accept") != "application/json" {
		t.Errorf("Accept = %q, want application/json", httpReq.Header.Get("Accept"))
	}
}

func TestCopilotProvider_BuildCopilotRequest_Stream(t *testing.T) {
	p := NewCopilotProvider("tok")
	req := &ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	}

	httpReq, err := p.buildCopilotRequest(context.Background(), req, true)
	if err != nil {
		t.Fatalf("buildCopilotRequest error: %v", err)
	}
	if httpReq.Header.Get("Accept") != "text/event-stream" {
		t.Errorf("Accept = %q, want text/event-stream for stream", httpReq.Header.Get("Accept"))
	}

	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var parsed map[string]any
	json.Unmarshal(bodyBytes, &parsed)
	streamVal, ok := parsed["stream"]
	if !ok {
		t.Error("expected stream field in request body")
	}
	if streamVal != true {
		t.Errorf("stream = %v, want true", streamVal)
	}
}

func TestCopilotProvider_BuildCopilotRequest_StreamPreservesExistingMetadata(t *testing.T) {
	p := NewCopilotProvider("tok")
	req := &ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
		Metadata: map[string]any{"existing": "value"},
	}

	httpReq, err := p.buildCopilotRequest(context.Background(), req, true)
	if err != nil {
		t.Fatalf("buildCopilotRequest error: %v", err)
	}
	_ = httpReq

	if _, ok := req.Metadata["stream"]; ok {
		t.Error("original req.Metadata should not have stream key (shallow copy should protect it)")
	}
	if req.Metadata["existing"] != "value" {
		t.Error("original req.Metadata values should be preserved")
	}
}

func TestCopilotProvider_BuildCopilotRequest_GetBody(t *testing.T) {
	p := NewCopilotProvider("tok")
	req := &ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: RoleUser, Content: "test"}},
	}

	httpReq, err := p.buildCopilotRequest(context.Background(), req, false)
	if err != nil {
		t.Fatalf("buildCopilotRequest error: %v", err)
	}
	if httpReq.GetBody == nil {
		t.Error("expected GetBody to be set for retry support")
	}
	getBodyReader, err := httpReq.GetBody()
	if err != nil {
		t.Fatalf("GetBody error: %v", err)
	}
	getBodyBytes, _ := io.ReadAll(getBodyReader)
	if len(getBodyBytes) == 0 {
		t.Error("GetBody should return non-empty body")
	}
}

func TestCopilotProvider_BuildCopilotRequest_NonStreamNoStreamInBody(t *testing.T) {
	p := NewCopilotProvider("tok")
	req := &ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	}

	httpReq, err := p.buildCopilotRequest(context.Background(), req, false)
	if err != nil {
		t.Fatalf("buildCopilotRequest error: %v", err)
	}
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var parsed map[string]any
	json.Unmarshal(bodyBytes, &parsed)
	if _, ok := parsed["stream"]; ok {
		t.Error("non-stream request should not have stream field in body")
	}
}

// ── CopilotProvider.CreateChatCompletion ───────────────────────────────────

func TestCopilotProvider_CreateChatCompletion_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Editor-Version") != "Nexus/1.0" {
			t.Errorf("Editor-Version = %q", r.Header.Get("Editor-Version"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"id": "chatcmpl-1",
			"model": "gpt-4o",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": "Hello from Copilot"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 5, "completion_tokens": 3, "total_tokens": 8}
		}`))
	}))
	defer server.Close()

	p := NewCopilotProviderWithOptions("test-token", server.URL, "gpt-4o", server.Client())
	resp, err := p.CreateChatCompletion(context.Background(), &ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion error: %v", err)
	}
	if resp.Content != "Hello from Copilot" {
		t.Errorf("Content = %q, want Hello from Copilot", resp.Content)
	}
}

func TestCopilotProvider_CreateChatCompletion_DefaultModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		json.Unmarshal(bodyBytes, &parsed)
		if parsed["model"] != "gpt-4o" {
			t.Errorf("model = %v, want gpt-4o (default)", parsed["model"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	p := NewCopilotProviderWithOptions("tok", server.URL, "", server.Client())
	resp, err := p.CreateChatCompletion(context.Background(), &ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want ok", resp.Content)
	}
}

func TestCopilotProvider_CreateChatCompletion_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": "rate limited"}`))
	}))
	defer server.Close()

	p := NewCopilotProviderWithOptions("tok", server.URL, "gpt-4o", server.Client())
	_, err := p.CreateChatCompletion(context.Background(), &ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 429 response")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %q, should contain 429", err.Error())
	}
}

func TestCopilotProvider_CreateChatCompletion_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	p := NewCopilotProviderWithOptions("tok", server.URL, "gpt-4o", server.Client())
	_, err := p.CreateChatCompletion(context.Background(), &ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for closed server")
	}
}

// ── CopilotProvider.CreateChatCompletionStream ─────────────────────────────

func TestCopilotProvider_CreateChatCompletionStream_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"id\":\"1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
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
		w.Write([]byte(`internal error`))
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
		json.Unmarshal(bodyBytes, &parsed)
		if parsed["model"] != "gpt-4o" {
			t.Errorf("model = %v, want gpt-4o (default)", parsed["model"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: [DONE]\n\n"))
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
		w.Write([]byte(`forbidden`))
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
		w.Write([]byte(`not json`))
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
		w.Write([]byte(`{
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
		w.Write([]byte(`short`))
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
