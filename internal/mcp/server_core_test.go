package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// mockRegistry implements ToolRegistry for testing.
type mockRegistry struct {
	tools        []string
	schemas      map[string]*ToolSchema
	dispatchFunc func(ctx context.Context, name string, args map[string]any) (string, error)
}

func (m *mockRegistry) ListTools() []string { return m.tools }
func (m *mockRegistry) GetSchema(name string) (*ToolSchema, bool) {
	s, ok := m.schemas[name]
	return s, ok
}
func (m *mockRegistry) Dispatch(ctx context.Context, name string, args map[string]any) (string, error) {
	if m.dispatchFunc != nil {
		return m.dispatchFunc(ctx, name, args)
	}
	return `{"result": "ok"}`, nil
}

func mustNewTokenStore(t *testing.T) *TokenStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewTokenStore(dir + "/test.json")
	if err != nil {
		t.Fatalf("failed to create token store: %v", err)
	}
	return store
}

func TestNewMCPServer(t *testing.T) {
	t.Parallel()

	info := ServerInfo{Name: "test-server", Version: "1.0"}
	registry := &mockRegistry{tools: []string{}, schemas: map[string]*ToolSchema{}}

	srv := NewMCPServer(info, registry)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if srv.serverInfo.Name != "test-server" {
		t.Errorf("expected server name test-server, got %s", srv.serverInfo.Name)
	}
	if len(srv.tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(srv.tools))
	}
}

func TestNewSafeMCPServer_NilRegistry(t *testing.T) {
	t.Parallel()

	info := ServerInfo{Name: "safe-server", Version: "1.0"}
	srv := NewSafeMCPServer(info, nil)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if srv.registry == nil {
		t.Error("registry should not be nil (should use EmptyToolRegistry)")
	}
}

func TestMCPServer_RegisterTool(t *testing.T) {
	t.Parallel()

	registry := &mockRegistry{
		tools: []string{"tool_a", "tool_b"},
		schemas: map[string]*ToolSchema{
			"tool_a": {Name: "tool_a", Description: "Tool A", Parameters: map[string]any{"type": "object"}},
			"tool_b": {Name: "tool_b", Description: "Tool B", Parameters: map[string]any{"type": "object"}},
		},
	}
	srv := NewMCPServer(ServerInfo{Name: "test", Version: "1.0"}, registry)

	if err := srv.RegisterTool("tool_a"); err != nil {
		t.Fatalf("RegisterTool failed: %v", err)
	}
	if len(srv.tools) != 1 {
		t.Errorf("expected 1 registered tool, got %d", len(srv.tools))
	}
	if srv.tools[0].Name != "tool_a" {
		t.Errorf("expected tool_a, got %s", srv.tools[0].Name)
	}
}

func TestMCPServer_RegisterTool_NotFound(t *testing.T) {
	t.Parallel()

	registry := &mockRegistry{
		tools:   []string{},
		schemas: map[string]*ToolSchema{},
	}
	srv := NewMCPServer(ServerInfo{Name: "test", Version: "1.0"}, registry)

	err := srv.RegisterTool("missing")
	if err == nil {
		t.Error("expected error for missing tool")
	}
}

func TestMCPServer_RegisterTool_NilRegistry(t *testing.T) {
	t.Parallel()

	srv := NewMCPServer(ServerInfo{Name: "test", Version: "1.0"}, nil)

	err := srv.RegisterTool("any")
	if err == nil {
		t.Error("expected error when registry is nil")
	}
}

func TestMCPServer_RegisterAllTools(t *testing.T) {
	t.Parallel()

	registry := &mockRegistry{
		tools: []string{"x", "y"},
		schemas: map[string]*ToolSchema{
			"x": {Name: "x", Description: "X", Parameters: map[string]any{}},
			"y": {Name: "y", Description: "Y", Parameters: map[string]any{}},
		},
	}
	srv := NewMCPServer(ServerInfo{Name: "test", Version: "1.0"}, registry)

	if err := srv.RegisterAllTools(context.Background()); err != nil {
		t.Fatalf("RegisterAllTools failed: %v", err)
	}
	if len(srv.tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(srv.tools))
	}
}

func TestMCPServer_RegisterAllTools_NilRegistry(t *testing.T) {
	t.Parallel()

	srv := NewMCPServer(ServerInfo{Name: "test", Version: "1.0"}, nil)
	if err := srv.RegisterAllTools(context.Background()); err != nil {
		t.Fatalf("RegisterAllTools with nil registry should not error: %v", err)
	}
}

func TestMCPServer_RegisterAllTools_CancelledContext(t *testing.T) {
	t.Parallel()

	registry := &mockRegistry{
		tools: []string{"a", "b", "c"},
		schemas: map[string]*ToolSchema{
			"a": {Name: "a", Parameters: map[string]any{}},
			"b": {Name: "b", Parameters: map[string]any{}},
			"c": {Name: "c", Parameters: map[string]any{}},
		},
	}
	srv := NewMCPServer(ServerInfo{Name: "test", Version: "1.0"}, registry)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := srv.RegisterAllTools(ctx)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestEmptyToolRegistry(t *testing.T) {
	t.Parallel()

	e := &EmptyToolRegistry{}

	if tools := e.ListTools(); tools != nil {
		t.Errorf("expected nil from ListTools, got %v", tools)
	}
	if schema, ok := e.GetSchema("anything"); ok || schema != nil {
		t.Error("expected GetSchema to return nil, false")
	}
	result, err := e.Dispatch(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result from Dispatch")
	}
}

func TestErrorResponse(t *testing.T) {
	t.Parallel()

	resp := errorResponse(42, ErrParse, "parse error")
	if resp.JSONRPC != "2.0" {
		t.Error("expected JSONRPC 2.0")
	}
	if resp.ID != 42 {
		t.Errorf("expected ID 42, got %v", resp.ID)
	}
	if resp.Error == nil {
		t.Fatal("expected non-nil error")
	}
	if resp.Error.Code != ErrParse {
		t.Errorf("expected code %d, got %d", ErrParse, resp.Error.Code)
	}
	if resp.Error.Message != "parse error" {
		t.Errorf("expected message 'parse error', got %s", resp.Error.Message)
	}
}

func TestTrimLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"hello\n", "hello"},
		{"  hello  \n", "hello"},
		{"", ""},
		{"   ", ""},
		{"hello world", "hello world"},
	}

	for _, tt := range tests {
		got := trimLine(tt.input)
		if got != tt.expected {
			t.Errorf("trimLine(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestJSONRPCRequest_JSONSerialization(t *testing.T) {
	t.Parallel()

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "initialize",
		Params:  map[string]any{"key": "value"},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed JSONRPCRequest
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.Method != "initialize" {
		t.Errorf("expected method initialize, got %s", parsed.Method)
	}
}

func TestRPCError_Codes(t *testing.T) {
	t.Parallel()

	codes := map[string]int{
		"ErrParse":        ErrParse,
		"ErrInvalid":      ErrInvalid,
		"ErrNotFound":     ErrNotFound,
		"ErrBadParams":    ErrBadParams,
		"ErrInternal":     ErrInternal,
		"ErrUnauthorized": ErrUnauthorized,
	}

	for name, code := range codes {
		if code >= -32700 && code <= -32000 {
			// Valid range
		} else {
			t.Errorf("%s code %d is outside valid JSON-RPC error range", name, code)
		}
	}
}

func TestResetDefaultManager(t *testing.T) {
	t.Parallel()

	ResetDefaultManager()
	// Should not panic
}
