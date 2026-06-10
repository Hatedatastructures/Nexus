// Package mcp 提供 MCP Client 的辅助函数和类型定义。
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ───────────────────────────── 类型定义 ─────────────────────────────

// ClientConfig MCP 客户端配置。
type ClientConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     []string          `json:"env"`
	Headers map[string]string `json:"headers"`
}

// ToolInfo MCP 工具信息。
type ToolInfo struct {
	Name        string
	Description string
	InputSchema any
}

// ───────────────────────────── 命令解析与验证 ─────────────────────────────

// validateMCPCommand 验证 MCP 服务器命令路径。
func validateMCPCommand(command string) error {
	if command == "" {
		return fmt.Errorf("命令不能为空")
	}
	if !filepath.IsAbs(command) {
		return fmt.Errorf("MCP 服务器命令必须是绝对路径: %s", command)
	}
	info, err := os.Stat(command)
	if err != nil {
		return fmt.Errorf("MCP 服务器命令不存在: %s", command)
	}
	if info.IsDir() {
		return fmt.Errorf("MCP 服务器命令不能是目录: %s", command)
	}
	return nil
}

// parseCommand 解析命令字符串，支持带引号的参数。
func (c *MCPClient) parseCommand(cmdStr string) (string, []string, error) {
	if cmdStr == "" {
		return "", nil, fmt.Errorf("命令不能为空")
	}

	parts := strings.Fields(cmdStr)
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("命令不能为空")
	}

	command := parts[0]
	args := parts[1:]

	// 检查命令是否存在
	path, err := exec.LookPath(command)
	if err != nil {
		if filepath.IsAbs(command) {
			info, err := os.Stat(command)
			if err != nil {
				return "", nil, fmt.Errorf("MCP 服务器命令不存在: %s", command)
			}
			if info.IsDir() {
				return "", nil, fmt.Errorf("MCP 服务器命令不能是目录: %s", command)
			}
			return command, args, nil
		}
		return "", nil, fmt.Errorf("MCP 服务器命令不存在: %s", command)
	}

	return path, args, nil
}

// ───────────────────────────── 工具发现 ─────────────────────────────

// ListTools 列出 MCP 服务器上可用的工具。
func (c *MCPClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	result, err := c.doRequest(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}

	toolsRaw, ok := result["tools"].([]any)
	if !ok {
		return nil, fmt.Errorf("无效的工具列表响应")
	}

	tools := make([]ToolInfo, 0, len(toolsRaw))
	for _, t := range toolsRaw {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		nameVal, ok := m["name"].(string)
		if !ok || nameVal == "" {
			continue
		}
		ti := ToolInfo{
			Name:        nameVal,
			Description: "",
		}
		if desc, ok := m["description"].(string); ok {
			ti.Description = desc
		}
		if schema, ok := m["inputSchema"]; ok {
			ti.InputSchema = schema
		}
		tools = append(tools, ti)
	}

	return tools, nil
}

// ───────────────────────────── 结果提取 ─────────────────────────────

// extractTextResult 从 doRequest 结果中提取文本内容。
func (c *MCPClient) extractTextResult(result map[string]any, err error) (string, error) {
	if err != nil {
		return "", err
	}
	content, ok := result["content"].([]any)
	if !ok {
		text, _ := result["text"].(string)
		if text != "" {
			return text, nil
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}

	var texts []string
	for _, item := range content {
		if m, ok := item.(map[string]any); ok {
			if t, ok := m["text"].(string); ok {
				texts = append(texts, t)
			}
		}
	}
	if len(texts) > 0 {
		return texts[0], nil
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}
