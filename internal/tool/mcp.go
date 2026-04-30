// Package tool 提供 MCP (Model Context Protocol) 工具。
// 通过 HTTP JSON-RPC 调用外部 MCP 服务器上的工具。
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ───────────────────────────── MCP 工具 ─────────────────────────────

// MCPTool 实现通过 HTTP JSON-RPC 调用外部 MCP 服务器的工具。
type MCPTool struct{}

// Name 返回工具名称。
func (t *MCPTool) Name() string { return "mcp_tool" }

// Description 返回工具描述。
func (t *MCPTool) Description() string {
	return "调用外部 MCP (Model Context Protocol) 服务器上的工具。支持通过 HTTP 端点发送 JSON-RPC 请求。"
}

// Toolset 返回工具所属工具集。
func (t *MCPTool) Toolset() string { return "mcp" }

// Emoji 返回工具图标。
func (t *MCPTool) Emoji() string { return "🔌" }

// IsAvailable 始终可用。
func (t *MCPTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *MCPTool) MaxResultChars() int { return 50000 }

// Schema 返回工具的 JSON Schema。
func (t *MCPTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "mcp_tool",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"endpoint": map[string]any{
					"type":        "string",
					"description": "MCP 服务器的 HTTP 端点 URL (如 http://localhost:3000)",
				},
				"tool_name": map[string]any{
					"type":        "string",
					"description": "要调用的工具名称",
				},
				"arguments": map[string]any{
					"type":        "object",
					"description": "传递给工具参数 (JSON 对象)",
				},
			},
			"required": []string{"endpoint", "tool_name"},
		},
	}
}

// Execute 执行 MCP 工具调用。
func (t *MCPTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	endpoint, ok := args["endpoint"].(string)
	if !ok || endpoint == "" {
		return ToolError("参数 endpoint 是必填项且必须为字符串"), nil
	}

	toolName, ok := args["tool_name"].(string)
	if !ok || toolName == "" {
		return ToolError("参数 tool_name 是必填项且必须为字符串"), nil
	}

	toolArgs, _ := args["arguments"].(map[string]any)

	slog.Info("MCP 工具调用",
		"endpoint", endpoint,
		"tool", toolName,
	)

	result, err := callMCPTool(ctx, endpoint, toolName, toolArgs)
	if err != nil {
		slog.Error("MCP 工具调用失败", "err", err)
		return ToolError(fmt.Sprintf("MCP 工具调用失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"output":   result,
		"tool":     toolName,
		"endpoint": endpoint,
		"status":   "completed",
	}), nil
}

// ───────────────────────────── MCP HTTP 调用 ─────────────────────────────

// callMCPTool 通过 HTTP JSON-RPC 调用 MCP 服务器的工具。
func callMCPTool(ctx context.Context, endpoint, toolName string, toolArgs map[string]any) (string, error) {
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": toolArgs,
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/tools/call", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	if rpcResp.Error != nil {
		return "", fmt.Errorf("MCP 错误 (code %d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if rpcResp.Result != nil {
		if content, ok := rpcResp.Result["content"].([]any); ok && len(content) > 0 {
			if item, ok := content[0].(map[string]any); ok {
				if text, ok := item["text"].(string); ok {
					return text, nil
				}
			}
		}
	}

	return string(respBody), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&MCPTool{})
}
