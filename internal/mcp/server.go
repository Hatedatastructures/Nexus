// Package mcp 提供 MCP Server 实现。
// 从 stdin 读取 JSON-RPC 请求，处理核心方法，写入响应到 stdout。
package mcp

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// ───────────────────────────── 构造函数 ─────────────────────────────

// NewMCPServer 创建 MCP 服务器实例。
func NewMCPServer(info ServerInfo, registry ToolRegistry) *MCPServer {
	s := &MCPServer{
		serverInfo:   info,
		registry:     registry,
		capabilities: make(map[string]any),
		tools:        make([]*ToolDefinition, 0),
	}
	s.capabilities["tools"] = map[string]any{}
	return s
}

// NewSafeMCPServer 创建 MCP 服务器，registry 为 nil 时使用空注册中心。
func NewSafeMCPServer(info ServerInfo, registry ToolRegistry) *MCPServer {
	if registry == nil {
		registry = &EmptyToolRegistry{}
	}
	return NewMCPServer(info, registry)
}

// ───────────────────────────── 工具注册 ─────────────────────────────

// RegisterTool 向 MCP 服务器注册一个工具。
func (s *MCPServer) RegisterTool(name string) error {
	if s.registry == nil {
		return fmt.Errorf("工具注册中心未初始化")
	}

	schema, ok := s.registry.GetSchema(name)
	if !ok {
		return fmt.Errorf("工具 %q 未在注册中心中找到", name)
	}

	def := &ToolDefinition{
		Name:        schema.Name,
		Description: schema.Description,
		InputSchema: schema.Parameters,
	}

	s.tools = append(s.tools, def)
	slog.Debug("MCP tool registered", "name", name)
	return nil
}

// RegisterAllTools 将注册中心中的所有工具批量注册到 MCP 服务器。
func (s *MCPServer) RegisterAllTools(ctx context.Context) error {
	if s.registry == nil {
		return nil
	}

	names := s.registry.ListTools()
	for _, name := range names {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := s.RegisterTool(name); err != nil {
			slog.Warn("failed to register MCP tool", "name", name, "err", err)
		}
	}
	slog.Info("MCP tools bulk registration completed", "count", len(s.tools))
	return nil
}

// ───────────────────────────── 请求处理 ─────────────────────────────

// HandleRequest 处理单个 JSON-RPC 请求。
func (s *MCPServer) HandleRequest(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	resp := &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	switch req.Method {
	case "initialize":
		resp.Result = s.handleInitialize(ctx, req.Params)
	case "tools/list":
		result, err := s.handleToolsList(ctx, req.Params)
		if err != nil {
			return errorResponse(req.ID, ErrInternal, err.Error())
		}
		resp.Result = result
	case "tools/call":
		result, err := s.handleToolCall(ctx, req.Params)
		if err != nil {
			return errorResponse(req.ID, ErrInternal, err.Error())
		}
		resp.Result = result
	default:
		return errorResponse(req.ID, ErrNotFound, fmt.Sprintf("方法 %q 未实现", req.Method))
	}

	return resp
}

// ───────────────────────────── 核心处理器 ─────────────────────────────

// handleInitialize 处理 initialize 请求。
func (s *MCPServer) handleInitialize(_ context.Context, params map[string]any) map[string]any {
	s.initialized.Store(true)

	slog.Info("MCP initialize request",
		"client_info", params,
		"server", s.serverInfo.Name,
		"version", s.serverInfo.Version,
	)

	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    s.capabilities,
		"serverInfo": map[string]any{
			"name":    s.serverInfo.Name,
			"version": s.serverInfo.Version,
		},
	}
}

// handleToolsList 返回已注册的工具列表。
func (s *MCPServer) handleToolsList(_ context.Context, _ map[string]any) (map[string]any, error) {
	if !s.initialized.Load() {
		return nil, fmt.Errorf("服务器未初始化")
	}

	tools := make([]map[string]any, 0, len(s.tools))
	for _, def := range s.tools {
		tools = append(tools, map[string]any{
			"name":        def.Name,
			"description": def.Description,
			"inputSchema": def.InputSchema,
		})
	}

	return map[string]any{"tools": tools}, nil
}

// handleToolCall 执行指定的工具。
func (s *MCPServer) handleToolCall(ctx context.Context, params map[string]any) (map[string]any, error) {
	if !s.initialized.Load() {
		return nil, fmt.Errorf("服务器未初始化")
	}

	name, ok := params["name"].(string)
	if !ok || name == "" {
		return nil, fmt.Errorf("参数 name 是必填项")
	}

	args, _ := params["arguments"].(map[string]any)

	if s.registry == nil {
		return nil, fmt.Errorf("工具注册中心未初始化")
	}

	result, err := s.registry.Dispatch(ctx, name, args)
	if err != nil {
		return nil, fmt.Errorf("工具 %q 执行失败: %v", name, err)
	}

	return map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": result,
			},
		},
		"isError": false,
	}, nil
}

// ───────────────────────────── stdin/stdout 循环 ─────────────────────────────

// RunStdioLoop 从 stdin 读取请求，处理并写入 stdout。
// 支持 NEXUS_MCP_AUTH_TOKEN 环境变量进行认证。
func (s *MCPServer) RunStdioLoop(ctx context.Context) error {
	authToken := os.Getenv("NEXUS_MCP_AUTH_TOKEN")
	if authToken == "" {
		slog.Warn("MCP server running without authentication — set NEXUS_MCP_AUTH_TOKEN for security")
	}

	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()

	authenticated := authToken == ""

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("读取 stdin 失败: %w", err)
		}

		line = trimLine(line)
		if line == "" {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			resp := errorResponse(nil, ErrParse, fmt.Sprintf("JSON 解析失败: %v", err))
			writeResponse(writer, resp)
			continue
		}

		// 认证检查
		if !authenticated {
			if req.Method != "auth" {
				writeResponse(writer, errorResponse(req.ID, ErrUnauthorized, "需要先进行认证"))
				continue
			}
			token, _ := req.Params["token"].(string)
			if subtle.ConstantTimeCompare([]byte(token), []byte(authToken)) != 1 {
				writeResponse(writer, errorResponse(req.ID, ErrUnauthorized, "认证失败"))
				continue
			}
			authenticated = true
			writeResponse(writer, &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]any{"status": "authenticated"},
			})
			slog.Info("MCP client authenticated successfully")
			continue
		}

		resp := s.HandleRequest(ctx, &req)
		writeResponse(writer, resp)
	}
}

// trimLine 去除行首尾空白。
func trimLine(s string) string {
	return strings.TrimSpace(s)
}

// writeResponse 将 JSON-RPC 响应写入输出。
func writeResponse(w *bufio.Writer, resp *JSONRPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Error("failed to serialize response", "err", err)
		return
	}
	fmt.Fprintln(w, string(data))
	w.Flush()
}

// errorResponse 创建 JSON-RPC 错误响应。
func errorResponse(id any, code int, message string) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
	}
}

// ───────────────────────────── 空注册中心 ─────────────────────────────

// EmptyToolRegistry 是 ToolRegistry 的空实现。
type EmptyToolRegistry struct{}

func (e *EmptyToolRegistry) ListTools() []string          { return nil }
func (e *EmptyToolRegistry) GetSchema(string) (*ToolSchema, bool) { return nil, false }
func (e *EmptyToolRegistry) Dispatch(context.Context, string, map[string]any) (string, error) {
	return `{"error": "no tools registered"}`, nil
}
