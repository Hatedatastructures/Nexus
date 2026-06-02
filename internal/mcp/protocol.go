// Package mcp 提供 MCP (Model Context Protocol) 协议的核心实现。
// 包含 JSON-RPC 2.0 协议类型定义、MCP Server 和 MCP Client。
// 用于 Nexus Agent 的 ACP (Agent Control Protocol) 入口。
package mcp

import (
	"context"
	"sync/atomic"
)

// ───────────────────────────── JSON-RPC 2.0 类型 ─────────────────────────────

// JSONRPCRequest 表示一个 JSON-RPC 2.0 请求。
// 符合 JSON-RPC 2.0 规范: https://www.jsonrpc.org/specification
type JSONRPCRequest struct {
	JSONRPC string         `json:"jsonrpc"`              // 协议版本，固定 "2.0"
	ID      any            `json:"id,omitempty"`         // 请求 ID (通知类型为空)
	Method  string         `json:"method"`               // 方法名称
	Params  map[string]any `json:"params,omitempty"`     // 方法参数
}

// JSONRPCResponse 表示一个 JSON-RPC 2.0 响应。
type JSONRPCResponse struct {
	JSONRPC string         `json:"jsonrpc"`              // 协议版本，固定 "2.0"
	ID      any            `json:"id"`                   // 对应请求的 ID
	Result  map[string]any `json:"result,omitempty"`     // 成功时的结果
	Error   *RPCError      `json:"error,omitempty"`      // 失败时的错误
}

// JSONRPCNotification 表示一个 JSON-RPC 2.0 通知 (无 ID)。
type JSONRPCNotification struct {
	JSONRPC string         `json:"jsonrpc"`              // 协议版本，固定 "2.0"
	Method  string         `json:"method"`               // 通知方法名
	Params  map[string]any `json:"params,omitempty"`     // 通知参数
}

// RequestID 是 JSON-RPC 请求 ID 的类型别名。
// 可以是字符串、数字或 null。使用 any 以兼容两种类型。
type RequestID any

// ───────────────────────────── 错误类型 ─────────────────────────────

// RPCError 表示 JSON-RPC 2.0 标准错误。
type RPCError struct {
	Code    int    `json:"code"`          // 错误码 (-32700 ~ -32603 为标准范围)
	Message string `json:"message"`       // 错误描述
	Data    any    `json:"data,omitempty"` // 附加数据 (可选)
}

// 标准 JSON-RPC 2.0 错误码
const (
	ErrParse        = -32700 // 解析错误
	ErrInvalid      = -32600 // 无效请求
	ErrNotFound     = -32601 // 方法不存在
	ErrBadParams    = -32602 // 参数无效
	ErrInternal     = -32603 // 内部错误
	ErrUnauthorized = -32001 // 未认证
)

// ───────────────────────────── MCP 协议类型 ─────────────────────────────

// MCPServer 是 MCP 服务器的结构体。
// 管理工具注册中心，处理 initialize/tools-list/tools-call 等核心方法。
type MCPServer struct {
	serverInfo   ServerInfo              // 服务器信息
	registry     ToolRegistry            // 工具注册中心 (接口注入)
	tools        []*ToolDefinition       // 已注册工具列表
	capabilities map[string]any          // 服务器能力声明
	initialized  atomic.Bool              // 是否已完成初始化
}

// ServerInfo 描述 MCP 服务器的元信息。
type ServerInfo struct {
	Name    string `json:"name"`    // 服务器名称
	Version string `json:"version"` // 服务器版本
}

// ToolDefinition 描述一个工具的 MCP 协议格式定义。
type ToolDefinition struct {
	Name        string         `json:"name"`                  // 工具名称
	Description string         `json:"description,omitempty"` // 工具描述
	InputSchema map[string]any `json:"inputSchema"`           // JSON Schema 输入参数
}

// ToolRegistry 是工具注册中心的接口抽象。
// 使 MCP Server 不依赖具体实现，支持注入任意注册中心。
type ToolRegistry interface {
	// ListTools 返回所有已注册工具的名称列表
	ListTools() []string
	// GetSchema 返回指定工具的 Schema
	GetSchema(name string) (*ToolSchema, bool)
	// Dispatch 执行指定工具
	Dispatch(ctx context.Context, name string, args map[string]any) (string, error)
}

// ToolSchema 是从工具注册中心获取的工具 Schema。
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}
