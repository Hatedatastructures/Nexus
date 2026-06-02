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

// ───────────────────────────── TestGetModel ─────────────────────────────

func TestGetModel(t *testing.T) {
	client := NewModelsDevClient()

	tests := []struct {
		name     string
		modelID  string
		wantNil  bool
		wantProv string
	}{
		{
			name:     "获取已知模型 claude-sonnet-4-20250514",
			modelID:  "claude-sonnet-4-20250514",
			wantNil:  false,
			wantProv: "anthropic",
		},
		{
			name:     "获取已知模型 gpt-4o",
			modelID:  "gpt-4o",
			wantNil:  false,
			wantProv: "openai",
		},
		{
			name:     "获取已知模型 gemini-2.5-pro",
			modelID:  "gemini-2.5-pro",
			wantNil:  false,
			wantProv: "google",
		},
		{
			name:    "获取已知模型 deepseek-chat",
			modelID: "deepseek-chat",
			wantNil: false,
			wantProv: "deepseek",
		},
		{
			name:    "获取已知模型 mistral-large-latest",
			modelID: "mistral-large-latest",
			wantNil: false,
			wantProv: "mistral",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := client.GetModel(tt.modelID)
			if tt.wantNil {
				if info != nil {
					t.Errorf("GetModel(%q) = %v, want nil", tt.modelID, info)
				}
				return
			}

			if info == nil {
				t.Fatalf("GetModel(%q) = nil, want non-nil", tt.modelID)
			}

			if info.Provider != tt.wantProv {
				t.Errorf("Provider = %q, want %q", info.Provider, tt.wantProv)
			}
			if info.ID != tt.modelID {
				t.Errorf("ID = %q, want %q", info.ID, tt.modelID)
			}
		})
	}
}

// ───────────────────────────── TestListModels ─────────────────────────────

func TestListModels(t *testing.T) {
	client := NewModelsDevClient()

	tests := []struct {
		name     string
		provider string
		minCount int
	}{
		{
			name:     "列出所有模型",
			provider: "",
			minCount: 10,
		},
		{
			name:     "按 provider 过滤 anthropic",
			provider: "anthropic",
			minCount: 3,
		},
		{
			name:     "按 provider 过滤 openai",
			provider: "openai",
			minCount: 3,
		},
		{
			name:     "按 provider 过滤 google",
			provider: "google",
			minCount: 2,
		},
		{
			name:     "按 provider 过滤 deepseek",
			provider: "deepseek",
			minCount: 1,
		},
		{
			name:     "不存在的 provider 返回空列表",
			provider: "nonexistent",
			minCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models := client.ListModels(tt.provider)

			if len(models) < tt.minCount {
				t.Errorf("ListModels(%q) 返回 %d 个模型, 至少需要 %d",
					tt.provider, len(models), tt.minCount)
			}

			// 验证过滤结果中所有模型都属于指定 provider
			for _, m := range models {
				if tt.provider != "" && m.Provider != tt.provider {
					t.Errorf("模型 %s 的 provider = %q, want %q", m.ID, m.Provider, tt.provider)
				}
			}
		})
	}
}

// ───────────────────────────── TestIsVisionModel ─────────────────────────────

func TestIsVisionModel(t *testing.T) {
	client := NewModelsDevClient()

	tests := []struct {
		name    string
		modelID string
		want    bool
	}{
		{name: "claude-sonnet-4 支持 vision", modelID: "claude-sonnet-4-20250514", want: true},
		{name: "claude-opus-4 支持 vision", modelID: "claude-opus-4-20250514", want: true},
		{name: "gpt-4o 支持 vision", modelID: "gpt-4o", want: true},
		{name: "gemini-2.5-pro 支持 vision", modelID: "gemini-2.5-pro", want: true},
		{name: "deepseek-chat 不支持 vision", modelID: "deepseek-chat", want: false},
		{name: "o1-mini 不支持 vision", modelID: "o1-mini", want: false},
		{name: "mistral-large 不支持 vision", modelID: "mistral-large-latest", want: false},
		{name: "不存在的模型不支持 vision", modelID: "nonexistent-model", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.IsVisionModel(tt.modelID)
			if got != tt.want {
				t.Errorf("IsVisionModel(%q) = %v, want %v", tt.modelID, got, tt.want)
			}
		})
	}
}

// ───────────────────────────── TestGetModelUnknown ─────────────────────────────

func TestGetModelUnknown(t *testing.T) {
	client := NewModelsDevClient()

	tests := []struct {
		name    string
		modelID string
	}{
		{name: "完全未知的模型", modelID: "totally-unknown-model"},
		{name: "空字符串", modelID: ""},
		{name: "类似已知但不完全匹配", modelID: "claude-sonnet-4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := client.GetModel(tt.modelID)
			if info != nil {
				t.Errorf("GetModel(%q) = %v, want nil", tt.modelID, info)
			}
		})
	}
}

// ───────────────────────────── TestModelDevInfoFields ─────────────────────────────

func TestModelDevInfoFields(t *testing.T) {
	client := NewModelsDevClient()

	info := client.GetModel("claude-sonnet-4-20250514")
	if info == nil {
		t.Fatal("GetModel() = nil")
	}

	// 验证内置模型的关键字段
	if info.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", info.ContextWindow)
	}
	if !info.Vision {
		t.Error("Vision 应为 true")
	}
	if !info.Reasoning {
		t.Error("Reasoning 应为 true")
	}
}

// ───────────────────────────── ListModels 返回副本 ─────────────────────────────

func TestListModels_ReturnsCopies(t *testing.T) {
	client := NewModelsDevClient()
	models := client.ListModels("anthropic")
	if len(models) == 0 {
		t.Fatal("expected at least one anthropic model")
	}
	original := client.GetModel("claude-sonnet-4-20250514")
	models[0].ID = "tampered"
	if original.ID == "tampered" {
		t.Error("ListModels should return copies, not references to cache")
	}
}

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
		w.Write(body)
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
		w.Write([]byte(`not valid json`))
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
		w.Write(body)
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
		w.Write(body)
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
		w.Write([]byte(`[]`))
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
		w.Write(body)
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
