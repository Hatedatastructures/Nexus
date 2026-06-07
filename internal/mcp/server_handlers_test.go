package mcp

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// HandleRequest tests
// ---------------------------------------------------------------------------

func TestMCPServer_HandleRequest_Initialize(t *testing.T) {
	t.Parallel()

	srv := NewMCPServer(ServerInfo{Name: "init-test", Version: "2.0"}, &mockRegistry{})

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  map[string]any{"client_info": "test"},
	}

	resp := srv.HandleRequest(context.Background(), req)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected result")
	}
	version, _ := resp.Result["protocolVersion"].(string)
	if version != "2024-11-05" {
		t.Errorf("expected protocol version 2024-11-05, got %s", version)
	}
}

func TestMCPServer_HandleRequest_ToolsList(t *testing.T) {
	t.Parallel()

	registry := &mockRegistry{
		tools: []string{"read"},
		schemas: map[string]*ToolSchema{
			"read": {Name: "read", Description: "Read file", Parameters: map[string]any{"type": "object"}},
		},
	}
	srv := NewMCPServer(ServerInfo{Name: "test", Version: "1.0"}, registry)
	_ = srv.RegisterTool("read")

	// Must initialize first
	srv.HandleRequest(context.Background(), &JSONRPCRequest{
		Method: "initialize", Params: map[string]any{},
	})

	req := &JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"}
	resp := srv.HandleRequest(context.Background(), req)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	toolsRaw := resp.Result["tools"]
	tools, ok := toolsRaw.([]map[string]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool in list, got %v", toolsRaw)
	}
	if tools[0]["name"] != "read" {
		t.Errorf("expected tool name read, got %v", tools[0]["name"])
	}
}

func TestMCPServer_HandleRequest_ToolsList_NotInitialized(t *testing.T) {
	t.Parallel()

	srv := NewMCPServer(ServerInfo{Name: "test", Version: "1.0"}, &mockRegistry{})
	req := &JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"}
	resp := srv.HandleRequest(context.Background(), req)

	if resp.Error == nil {
		t.Error("expected error for uninitialized server")
	}
}

func TestMCPServer_HandleRequest_ToolCall(t *testing.T) {
	t.Parallel()

	registry := &mockRegistry{
		tools:   []string{"echo"},
		schemas: map[string]*ToolSchema{"echo": {Name: "echo", Parameters: map[string]any{}}},
		dispatchFunc: func(_ context.Context, _ string, _ map[string]any) (string, error) {
			return `{"echo": "hello"}`, nil
		},
	}
	srv := NewMCPServer(ServerInfo{Name: "test", Version: "1.0"}, registry)

	// Initialize first
	srv.HandleRequest(context.Background(), &JSONRPCRequest{Method: "initialize", Params: map[string]any{}})

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  map[string]any{"name": "echo", "arguments": map[string]any{"msg": "hi"}},
	}
	resp := srv.HandleRequest(context.Background(), req)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected result")
	}
	content, _ := resp.Result["content"].([]map[string]any)
	if len(content) == 0 {
		t.Error("expected content in result")
	}
	isError, _ := resp.Result["isError"].(bool)
	if isError {
		t.Error("isError should be false")
	}
}

func TestMCPServer_HandleRequest_ToolCall_MissingName(t *testing.T) {
	t.Parallel()

	srv := NewMCPServer(ServerInfo{Name: "test", Version: "1.0"}, &mockRegistry{})
	srv.HandleRequest(context.Background(), &JSONRPCRequest{Method: "initialize", Params: map[string]any{}})

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "tools/call",
		Params:  map[string]any{"arguments": map[string]any{}},
	}
	resp := srv.HandleRequest(context.Background(), req)
	if resp.Error == nil {
		t.Error("expected error for missing tool name")
	}
}

func TestMCPServer_HandleRequest_UnknownMethod(t *testing.T) {
	t.Parallel()

	srv := NewMCPServer(ServerInfo{Name: "test", Version: "1.0"}, &mockRegistry{})
	req := &JSONRPCRequest{JSONRPC: "2.0", ID: 5, Method: "unknown/method"}
	resp := srv.HandleRequest(context.Background(), req)

	if resp.Error == nil {
		t.Error("expected error for unknown method")
	}
	if resp.Error.Code != ErrNotFound {
		t.Errorf("expected error code %d, got %d", ErrNotFound, resp.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// OAuthManager tests
// ---------------------------------------------------------------------------

func TestOAuthManager_isTokenExpiringSoon(t *testing.T) {
	t.Parallel()

	mgr := &OAuthManager{}

	// Token expiring in the past (expired)
	token := &OAuthToken{ExpiresAt: time.Now().Unix() - 100}
	if !mgr.isTokenExpiringSoon(token) {
		t.Error("expired token should be expiring soon")
	}

	// Token expiring in 10 seconds (within 30-second buffer)
	token = &OAuthToken{ExpiresAt: time.Now().Unix() + 10}
	if !mgr.isTokenExpiringSoon(token) {
		t.Error("token expiring in 10s should be expiring soon (30s buffer)")
	}

	// Token expiring far in future
	token = &OAuthToken{ExpiresAt: time.Now().Unix() + 3600}
	if mgr.isTokenExpiringSoon(token) {
		t.Error("token expiring in 1 hour should not be expiring soon")
	}

	// Token with zero ExpiresAt
	token = &OAuthToken{ExpiresAt: 0}
	if mgr.isTokenExpiringSoon(token) {
		t.Error("token with ExpiresAt=0 should not be expiring soon")
	}
}

func TestOAuthManager_GetTokenInfo_Nil(t *testing.T) {
	t.Parallel()

	mgr := &OAuthManager{}
	info := mgr.GetTokenInfo()
	if info != nil {
		t.Error("expected nil info when no token")
	}
}

func TestOAuthManager_GetTokenInfo_WithToken(t *testing.T) {
	t.Parallel()

	mgr := &OAuthManager{
		token: &OAuthToken{
			AccessToken:  "at-123",
			RefreshToken: "rt-456",
			TokenType:    "Bearer",
			Scope:        "read",
			ExpiresAt:    time.Now().Unix() + 3600,
		},
	}

	info := mgr.GetTokenInfo()
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if hasAT, _ := info["has_access_token"].(bool); !hasAT {
		t.Error("expected has_access_token=true")
	}
	if hasRT, _ := info["has_refresh_token"].(bool); !hasRT {
		t.Error("expected has_refresh_token=true")
	}
}

func TestOAuthManager_IsTokenExpired_NoToken(t *testing.T) {
	t.Parallel()

	mgr := &OAuthManager{}
	expired, exists := mgr.IsTokenExpired()
	if exists {
		t.Error("expected exists=false when no token")
	}
	if expired {
		t.Error("expected expired=false when no token")
	}
}

func TestOAuthManager_IsTokenExpired_ValidToken(t *testing.T) {
	t.Parallel()

	mgr := &OAuthManager{
		token: &OAuthToken{
			AccessToken: "at",
			ExpiresAt:   time.Now().Unix() + 3600,
		},
	}
	expired, exists := mgr.IsTokenExpired()
	if !exists {
		t.Error("expected exists=true")
	}
	if expired {
		t.Error("expected expired=false for future token")
	}
}

func TestOAuthManager_IsTokenExpired_ExpiredToken(t *testing.T) {
	t.Parallel()

	mgr := &OAuthManager{
		token: &OAuthToken{
			AccessToken: "at",
			ExpiresAt:   time.Now().Unix() - 10,
		},
	}
	expired, exists := mgr.IsTokenExpired()
	if !exists {
		t.Error("expected exists=true")
	}
	if !expired {
		t.Error("expected expired=true for past token")
	}
}

func TestOAuthManager_SetToken_Nil(t *testing.T) {
	t.Parallel()

	mgr := &OAuthManager{
		store: mustNewTokenStore(t),
	}
	err := mgr.SetToken(nil)
	if err == nil {
		t.Error("expected error for nil token")
	}
}

func TestOAuthManager_ClearToken(t *testing.T) {
	t.Parallel()

	mgr := &OAuthManager{
		token: &OAuthToken{AccessToken: "at"},
		store: mustNewTokenStore(t),
	}

	if err := mgr.ClearToken(); err != nil {
		t.Fatalf("ClearToken failed: %v", err)
	}

	if mgr.token != nil {
		t.Error("expected nil token after clear")
	}
}

func TestOAuthManager_GetValidToken_NoToken(t *testing.T) {
	t.Parallel()

	mgr := &OAuthManager{}
	_, err := mgr.GetValidToken()
	if err == nil {
		t.Error("expected error when no token available")
	}
}

func TestOAuthManager_calculateRemainingTTL(t *testing.T) {
	t.Parallel()

	mgr := &OAuthManager{token: &OAuthToken{ExpiresAt: time.Now().Unix() + 100}}
	ttl := mgr.calculateRemainingTTL()
	if ttl <= 0 || ttl > 100 {
		t.Errorf("unexpected TTL: %d", ttl)
	}
}

func TestOAuthManager_calculateRemainingTTL_Nil(t *testing.T) {
	t.Parallel()

	mgr := &OAuthManager{}
	ttl := mgr.calculateRemainingTTL()
	if ttl != 0 {
		t.Errorf("expected 0 TTL for nil token, got %d", ttl)
	}
}

func TestOAuthManager_calculateRemainingTTL_ZeroExpiresAt(t *testing.T) {
	t.Parallel()

	mgr := &OAuthManager{token: &OAuthToken{ExpiresAt: 0}}
	ttl := mgr.calculateRemainingTTL()
	if ttl != 0 {
		t.Errorf("expected 0 TTL for zero ExpiresAt, got %d", ttl)
	}
}
