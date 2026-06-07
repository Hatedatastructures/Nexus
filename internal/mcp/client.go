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
	serverInfo ServerInfo         // 连接的服务器信息
	connected  bool               // 连接状态
	stdin      io.WriteCloser     // 子进程 stdin
	stdout     io.Reader          // 子进程 stdout
	cmd        *exec.Cmd          // 子进程句柄
	cancel     context.CancelFunc // 用于终止 reader goroutine

	mu          sync.Mutex                      // 并发保护
	requestID   int64                           // 请求 ID 计数器
	pendingReqs map[int64]chan *JSONRPCResponse // 待处理请求
}

// NewMCPClient 创建一个新的 MCP 客户端实例。
func NewMCPClient() *MCPClient {
	return &MCPClient{
		pendingReqs: make(map[int64]chan *JSONRPCResponse),
	}
}

// ───────────────────────────── 连接管理 ─────────────────────────────

// Connect 启动 MCP 服务器进程并建立连接。
func (c *MCPClient) Connect(ctx context.Context, config ClientConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return fmt.Errorf("already connected")
	}

	command, args, err := c.parseCommand(config.Command)
	if err != nil {
		return err
	}

	cmdCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	cmd := exec.CommandContext(cmdCtx, command, args...)
	cmd.Env = append(os.Environ(), config.Env...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("创建 stdin 管道失败: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("创建 stdout 管道失败: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("启动 MCP 服务器失败: %w", err)
	}

	c.stdin = stdin
	c.stdout = stdout
	c.cmd = cmd
	c.requestID = 0
	c.pendingReqs = make(map[int64]chan *JSONRPCResponse)

	// 初始化握手
	initReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.requestID,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "nexus-agent",
				"version": "1.0.0",
			},
		},
	}
	c.requestID++

	data, err := json.Marshal(initReq)
	if err != nil {
		cancel()
		return fmt.Errorf("序列化初始化请求失败: %w", err)
	}

	if _, err := fmt.Fprintln(stdin, string(data)); err != nil {
		cancel()
		return fmt.Errorf("发送初始化请求失败: %w", err)
	}

	// 读取初始化响应
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		cancel()
		return fmt.Errorf("读取初始化响应失败: %w", err)
	}

	var initResp JSONRPCResponse
	if err := json.Unmarshal([]byte(line), &initResp); err != nil {
		cancel()
		return fmt.Errorf("解析初始化响应失败: %w", err)
	}

	if initResp.Error != nil {
		cancel()
		return fmt.Errorf("MCP 初始化错误: [%d] %s", initResp.Error.Code, initResp.Error.Message)
	}

	// 解析服务器信息
	if resultBytes, err := json.Marshal(initResp.Result); err == nil {
		_ = json.Unmarshal(resultBytes, &c.serverInfo)
	}

	// 发送 initialized 通知
	initializedReq := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	notifData, _ := json.Marshal(initializedReq)
	_, _ = fmt.Fprintln(stdin, string(notifData))

	// 启动后台 reader
	c.startReader(cmdCtx, stdout)
	c.connected = true

	slog.Info("MCP client connected", "server", c.serverInfo.Name, "command", config.Command)
	return nil
}

// Disconnect 关闭与 MCP 服务器的连接。
func (c *MCPClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	c.connected = false

	if c.cancel != nil {
		c.cancel()
	}

	if c.stdin != nil {
		_ = c.stdin.Close()
	}

	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}

	return nil
}

// IsConnected 返回连接状态。
func (c *MCPClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// GetServerInfo 返回连接的服务器信息。
func (c *MCPClient) GetServerInfo() ServerInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.serverInfo
}

// ───────────────────────────── 后台读取 ─────────────────────────────

// startReader 启动后台读取协程，读取服务端响应。
func (c *MCPClient) startReader(ctx context.Context, stdout io.Reader) {
	go func() {
		reader := bufio.NewReader(stdout)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					slog.Error("failed to read MCP server response", "err", err)
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
							func() {
								defer func() {
									if r := recover(); r != nil {
										slog.Debug("MCP reader: channel already closed, discarding response")
									}
								}()
								ch <- &resp
							}()
						}
					} else if id, ok := resp.ID.(string); ok {
						// 字符串 ID (不太常见)
						var targetCh chan *JSONRPCResponse
						c.mu.Lock()
						for reqID, ch := range c.pendingReqs {
							if fmt.Sprintf("%d", reqID) == id {
								targetCh = ch
								break
							}
						}
						c.mu.Unlock()

						if targetCh != nil {
							func() {
								defer func() {
									if r := recover(); r != nil {
										slog.Debug("MCP reader: channel already closed, discarding response")
									}
								}()
								targetCh <- &resp
							}()
						}
					}
				}
			} else {
				slog.Debug("received MCP server message", "data", line)
			}
		}
	}()
}

// CallTool 调用 MCP 服务器上的指定工具。
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return "", fmt.Errorf("客户端未连接")
	}

	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	result, err := c.doRequest(ctx, "tools/call", params)
	return c.extractTextResult(result, err)
}

// ───────────────────────────── 内部请求机制 ─────────────────────────────

// doRequest 发送一个 JSON-RPC 请求并等待响应。
func (c *MCPClient) doRequest(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	// 注册请求到 pendingReqs
	c.mu.Lock()
	id := c.requestID
	c.requestID++
	ch := make(chan *JSONRPCResponse, 1)
	c.pendingReqs[id] = ch
	c.mu.Unlock()

	// 确保请求完成后清理 pendingReqs
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

	// 写入 stdin（锁仅覆盖写入操作，防止并发消息交错）
	c.mu.Lock()
	stdin := c.stdin
	c.mu.Unlock()
	if stdin == nil {
		return nil, fmt.Errorf("stdin 管道未建立")
	}
	if _, err := fmt.Fprintln(stdin, string(data)); err != nil {
		return nil, fmt.Errorf("写入请求失败: %w", err)
	}

	slog.Debug("sending MCP request", "method", method, "id", id)

	// 等待响应
	select {
	case resp := <-ch:
		if resp == nil {
			return nil, fmt.Errorf("MCP 连接已关闭")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("RPC 错误 [%d]: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
