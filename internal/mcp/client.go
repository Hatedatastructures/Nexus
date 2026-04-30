// Package mcp 提供 MCP Client 实现。
// 用于与其他 MCP 服务器通过 stdin/stdout 进行通信。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
)

// ───────────────────────────── MCPClient 实现 ─────────────────────────────

// MCPClient 是 MCP 客户端，用于与外部 MCP 服务器通信。
type MCPClient struct {
	serverInfo ServerInfo   // 连接的服务器信息
	connected  bool         // 连接状态
	stdin      io.WriteCloser // 子进程 stdin
	stdout     io.Reader      // 子进程 stdout
	cmd        *exec.Cmd      // 子进程句柄

	mu          sync.Mutex                // 并发保护
	requestID   int64                     // 请求 ID 计数器
	pendingReqs map[int64]chan *JSONRPCResponse // 待处理请求
}

// NewMCPClient 创建 MCP 客户端实例。
func NewMCPClient() *MCPClient {
	return &MCPClient{
		requestID:   1,
		pendingReqs: make(map[int64]chan *JSONRPCResponse),
	}
}

// Connect 通过启动子进程连接到 MCP 服务器。
// command 是服务器可执行文件路径，args 是命令行参数，env 是环境变量。
// 连接后发送 initialize 请求完成握手。
func (c *MCPClient) Connect(ctx context.Context, command string, args []string, env []string) error {
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return fmt.Errorf("客户端已连接")
	}
	c.mu.Unlock()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = env

	// 设置 stdin/stdout 管道
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("创建 stdin 管道失败: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建 stdout 管道失败: %w", err)
	}

	cmd.Stderr = os.Stderr // 子进程的 stderr 直接输出

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 MCP 服务器 %s 失败: %w", command, err)
	}

	c.mu.Lock()
	c.connected = true
	c.stdin = stdin
	c.stdout = stdout
	c.cmd = cmd
	c.mu.Unlock()

	// 启动后台读取协程
	c.startReader(stdout)

	// 发送 initialize 请求
	initParams := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "nexus-acp-client",
			"version": "1.0.0",
		},
	}

	result, err := c.doRequest(ctx, "initialize", initParams)
	if err != nil {
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
		return fmt.Errorf("MCP 初始化失败: %w", err)
	}

	// 解析服务器信息
	if srvInfo, ok := result["serverInfo"].(map[string]any); ok {
		if name, ok := srvInfo["name"].(string); ok {
			c.serverInfo.Name = name
		}
		if ver, ok := srvInfo["version"].(string); ok {
			c.serverInfo.Version = ver
		}
	}

	slog.Info("MCP 客户端已连接",
		"server", c.serverInfo.Name,
		"version", c.serverInfo.Version,
	)

	return nil
}

// startReader 启动后台读取协程，读取服务端响应。
func (c *MCPClient) startReader(stdout io.Reader) {
	go func() {
		reader := bufio.NewReader(stdout)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					slog.Error("读取 MCP 服务器响应失败", "err", err)
				}
				return
			}
			if len(line) <= 1 {
				continue
			}

			// 尝试解析为响应
			var resp JSONRPCResponse
			if err := json.Unmarshal([]byte(line), &resp); err == nil {
				if resp.ID != nil {
					// 匹配待处理请求
					if id, ok := resp.ID.(float64); ok {
						c.mu.Lock()
						ch, exists := c.pendingReqs[int64(id)]
						c.mu.Unlock()
						if exists {
							ch <- &resp
						}
					} else if id, ok := resp.ID.(string); ok {
						// 字符串 ID (不太常见)
						c.mu.Lock()
						for reqID, ch := range c.pendingReqs {
							if fmt.Sprintf("%d", reqID) == id {
								ch <- &resp
								break
							}
						}
						c.mu.Unlock()
					}
				}
			} else {
				slog.Debug("收到 MCP 服务器消息", "data", line)
			}
		}
	}()
}

// CallTool 调用 MCP 服务器上的指定工具。
// name 是工具名称，args 是工具参数。
// 返回工具执行结果的文本内容和可能的错误。
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return "", fmt.Errorf("客户端未连接")
	}
	c.mu.Unlock()

	params := map[string]any{
		"name":      name,
		"arguments": args,
	}

	result, err := c.doRequest(ctx, "tools/call", params)
	if err != nil {
		return "", err
	}

	// 检查是否为错误响应
	if isError, _ := result["isError"].(bool); isError {
		return "", fmt.Errorf("工具 %q 执行返回错误", name)
	}

	// 提取结果文本
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		return "", nil
	}

	if first, ok := content[0].(map[string]any); ok {
		if text, ok := first["text"].(string); ok {
			return text, nil
		}
	}

	return "", fmt.Errorf("无法解析工具结果")
}

// Disconnect 断开与 MCP 服务器的连接。
func (c *MCPClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	c.connected = false

	if c.stdin != nil {
		c.stdin.Close()
	}

	if c.cmd != nil {
		c.cmd.Process.Kill()
	}

	slog.Info("MCP 客户端已断开连接")
	return nil
}

// ───────────────────────────── 内部请求机制 ─────────────────────────────

// doRequest 发送一个 JSON-RPC 请求并等待响应。
func (c *MCPClient) doRequest(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	c.mu.Lock()
	id := c.requestID
	c.requestID++
	ch := make(chan *JSONRPCResponse, 1)
	c.pendingReqs[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pendingReqs, id)
		c.mu.Unlock()
	}()

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 写入 stdin
	c.mu.Lock()
	stdin := c.stdin
	c.mu.Unlock()

	if stdin == nil {
		return nil, fmt.Errorf("stdin 管道未建立")
	}

	if _, err := fmt.Fprintln(stdin, string(data)); err != nil {
		return nil, fmt.Errorf("写入请求失败: %w", err)
	}

	slog.Debug("发送 MCP 请求", "method", method, "id", id)

	// 等待响应
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("RPC 错误 [%d]: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
