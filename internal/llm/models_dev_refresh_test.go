package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ───────────────────────────── Refresh ─────────────────────────────

func TestModelsDevClient_Refresh_CacheStillValid(t *testing.T) {
	client := NewModelsDevClient()
	// 新创建的客户端缓存刚加载，TTL 内不应刷新
	err := client.Refresh(context.Background())
	if err != nil {
		t.Errorf("Refresh on fresh cache should succeed, got: %v", err)
	}
}

func TestModelsDevClient_Refresh_APIUpdate(t *testing.T) {
	apiModels := []apiModelEntry{
		{ID: "test-model-api", Provider: "test", ContextWindow: 99999, MaxOutput: 9999, Vision: true, Reasoning: true, InputPrice: 1.0, OutputPrice: 2.0},
		{ID: "claude-sonnet-4-20250514", Provider: "anthropic", ContextWindow: 999999, MaxOutput: 99999, Vision: true, Reasoning: true, InputPrice: 99.0, OutputPrice: 99.0},
	}
	body, _ := json.Marshal(apiModels)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client := NewModelsDevClient()
	// 强制缓存过期
	client.mu.Lock()
	client.cacheTime = time.Now().Add(-2 * time.Hour)
	client.mu.Unlock()

	// 替换 HTTP 客户端并重定向到测试服务器
	client.http = server.Client()
	client.http.Transport = &redirectTransport{target: server.URL}

	err := client.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh error: %v", err)
	}

	// 内置模型应保留
	info := client.GetModel("claude-sonnet-4-20250514")
	if info == nil {
		t.Fatal("builtin model should still be present")
	}
	if info.ContextWindow != 999999 {
		t.Errorf("ContextWindow = %d, want 999999 (API should override builtin)", info.ContextWindow)
	}

	// API 新增的模型应该存在
	info2 := client.GetModel("test-model-api")
	if info2 == nil {
		t.Fatal("API-added model should be present")
	}
	if info2.Provider != "test" {
		t.Errorf("Provider = %q, want test", info2.Provider)
	}
}

func TestModelsDevClient_Refresh_APIError_KeepsCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewModelsDevClient()
	client.mu.Lock()
	client.cacheTime = time.Now().Add(-2 * time.Hour)
	client.mu.Unlock()
	client.http = server.Client()
	client.http.Transport = &redirectTransport{target: server.URL}

	err := client.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error when API returns 500")
	}

	// 内置模型应保留
	info := client.GetModel("claude-sonnet-4-20250514")
	if info == nil {
		t.Fatal("builtin models should be preserved after API error")
	}
}

func TestModelsDevClient_Refresh_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	client := NewModelsDevClient()
	client.mu.Lock()
	client.cacheTime = time.Now().Add(-2 * time.Hour)
	client.mu.Unlock()
	client.http = server.Client()
	client.http.Transport = &redirectTransport{target: server.URL}

	err := client.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestModelsDevClient_Refresh_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not valid json`))
	}))
	defer server.Close()

	client := NewModelsDevClient()
	client.mu.Lock()
	client.cacheTime = time.Now().Add(-2 * time.Hour)
	client.mu.Unlock()
	client.http = server.Client()
	client.http.Transport = &redirectTransport{target: server.URL}

	err := client.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	// 内置模型应保留
	info := client.GetModel("gpt-4o")
	if info == nil {
		t.Fatal("builtin models should be preserved after invalid JSON")
	}
}

func TestModelsDevClient_Refresh_Non200Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	client := NewModelsDevClient()
	client.mu.Lock()
	client.cacheTime = time.Now().Add(-2 * time.Hour)
	client.mu.Unlock()
	client.http = server.Client()
	client.http.Transport = &redirectTransport{target: server.URL}

	err := client.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, should contain 403", err.Error())
	}
}

func TestModelsDevClient_Refresh_PreservesBuiltinModels(t *testing.T) {
	apiModels := []apiModelEntry{
		{ID: "api-only-model", Provider: "newprovider", ContextWindow: 50000},
	}
	body, _ := json.Marshal(apiModels)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client := NewModelsDevClient()
	client.mu.Lock()
	client.cacheTime = time.Now().Add(-2 * time.Hour)
	client.mu.Unlock()
	client.http = server.Client()
	client.http.Transport = &redirectTransport{target: server.URL}

	err := client.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh error: %v", err)
	}

	// 所有内置模型 + API 模型都应存在
	for _, m := range builtinModels {
		info := client.GetModel(m.ID)
		if info == nil {
			t.Errorf("builtin model %q should be preserved", m.ID)
		}
	}
	info := client.GetModel("api-only-model")
	if info == nil {
		t.Error("API-only model should be present")
	}
}

func TestModelsDevClient_Refresh_CancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer server.Close()

	client := NewModelsDevClient()
	client.mu.Lock()
	client.cacheTime = time.Now().Add(-2 * time.Hour)
	client.mu.Unlock()
	client.http = server.Client()
	client.http.Transport = &redirectTransport{target: server.URL}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.Refresh(ctx)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ───────────────────────────── fetchFromAPI ─────────────────────────────

func TestModelsDevClient_FetchFromAPI_Success(t *testing.T) {
	apiModels := []apiModelEntry{
		{ID: "model-a", Provider: "prov-a", ContextWindow: 100000, MaxOutput: 4096, Vision: true, Reasoning: false, InputPrice: 1.0, OutputPrice: 2.0},
		{ID: "model-b", Provider: "prov-b", ContextWindow: 200000, MaxOutput: 8192, Vision: false, Reasoning: true, InputPrice: 3.0, OutputPrice: 6.0},
	}
	body, _ := json.Marshal(apiModels)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Method = %q, want GET", r.Method)
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client := NewModelsDevClient()
	client.http = server.Client()
	client.http.Transport = &redirectTransport{target: server.URL}

	result, err := client.fetchFromAPI(context.Background())
	if err != nil {
		t.Fatalf("fetchFromAPI error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result["model-a"].Provider != "prov-a" {
		t.Errorf("model-a provider = %q", result["model-a"].Provider)
	}
	if !result["model-a"].Vision {
		t.Error("model-a Vision should be true")
	}
	if !result["model-b"].Reasoning {
		t.Error("model-b Reasoning should be true")
	}
}

func TestModelsDevClient_FetchFromAPI_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := NewModelsDevClient()
	client.http = server.Client()
	client.http.Transport = &redirectTransport{target: server.URL}

	result, err := client.fetchFromAPI(context.Background())
	if err != nil {
		t.Fatalf("fetchFromAPI error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}

// ───────────────────────────── GetModel 异步刷新 ─────────────────────────────

func TestModelsDevClient_GetModel_TriggerAsyncRefresh(t *testing.T) {
	apiModels := []apiModelEntry{
		{ID: "unknown-model-xyz", Provider: "newprov", ContextWindow: 50000},
	}
	body, _ := json.Marshal(apiModels)

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client := NewModelsDevClient()
	// 强制缓存过期，这样 GetModel 的异步刷新能真正触发
	client.mu.Lock()
	client.cacheTime = time.Now().Add(-2 * time.Hour)
	client.mu.Unlock()
	client.http = server.Client()
	client.http.Transport = &redirectTransport{target: server.URL}

	// 查询不存在的模型，触发异步刷新
	info := client.GetModel("unknown-model-xyz")
	if info != nil {
		t.Error("first lookup should return nil (not yet in cache)")
	}

	// 等待异步刷新完成
	time.Sleep(500 * time.Millisecond)

	// 再次查询，如果刷新成功应该能找到
	info = client.GetModel("unknown-model-xyz")
	if info == nil {
		t.Error("after async refresh, model should be found")
	}
	if requests.Load() == 0 {
		t.Error("expected API request to be made")
	}
}

// redirectTransport 将请求重定向到测试服务器（绕过 const URL 问题）。
type redirectTransport struct {
	target string
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// 重写 URL 到测试服务器
	newURL := *req.URL
	newURL.Scheme = "http"
	newURL.Host = strings.TrimPrefix(t.target, "http://")
	newReq := req.Clone(req.Context())
	newReq.URL = &newURL
	return http.DefaultTransport.RoundTrip(newReq)
}
