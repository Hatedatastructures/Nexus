package commands

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"nexus-agent/internal/config"
	"nexus-agent/internal/state"
	"nexus-agent/internal/tool"
)

// ───────────────────────────── MCP 消息服务 ─────────────────────────────

func init() {
	Register(&MCPServeCommand{})
}

// MCPServeCommand 实现 nexus mcp-serve 子命令。
type MCPServeCommand struct{}

func (c *MCPServeCommand) Name() string    { return "mcp-serve" }
func (c *MCPServeCommand) Synopsis() string { return "启动 MCP 消息服务 (stdio)" }

func (c *MCPServeCommand) Run(args []string) {
	cfg, err := config.Load("")
	if err != nil {
		PrintError("加载配置失败: %v", err)
		return
	}

	PrintTitle("MCP 消息服务")

	// 初始化状态存储
	dbPath := GetDBPath()
	store, err := state.NewStore(dbPath)
	if err != nil {
		PrintError("初始化状态存储失败: %v", err)
		return
	}
	defer store.Close()

	// 初始化工具注册表
	registry := tool.GetRegistry()
	tool.DiscoverBuiltin()

	// 处理信号
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	_ = cfg
	_ = registry

	slog.Info("MCP 消息服务启动")

	// MCP stdio 循环
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			slog.Info("MCP 消息服务关闭")
			return
		default:
		}
		// 简单的 JSON-RPC 回显
		line := scanner.Text()
		slog.Debug("MCP 收到请求", "data", line)
		fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":null,"result":{"status":"ok"}}`+"\n")
	}
}
