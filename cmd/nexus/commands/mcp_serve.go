package commands

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"

	"nexus-agent/internal/config"
	"nexus-agent/internal/mcp"
	"nexus-agent/internal/state"
	"nexus-agent/internal/tool"
)

// ───────────────────────────── MCP 消息服务 ─────────────────────────────


// MCPServeCommand 实现 nexus mcp-serve 子命令。
type MCPServeCommand struct{}

func (c *MCPServeCommand) Name() string     { return "mcp-serve" }
func (c *MCPServeCommand) Synopsis() string { return "启动 MCP 消息服务 (stdio)" }

func (c *MCPServeCommand) Run(args []string) {
	_, err := config.Load("")
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
	defer func() { _ = store.Close() }()

	// 初始化工具注册表并发现内置工具
	registry := tool.NewRegistry()
	tool.RegisterAllTools(registry)

	// 将 tool.Registry 适配为 MCP ToolRegistry 接口
	mcpRegistry := mcp.NewToolRegistryAdapter(registry)

	// 创建 MCP 服务器
	serverInfo := mcp.ServerInfo{
		Name:    "nexus-agent",
		Version: "0.1.0",
	}

	server := mcp.NewMCPServer(serverInfo, mcpRegistry)

	// 批量注册所有工具
	ctx := context.Background()
	if err := server.RegisterAllTools(ctx); err != nil {
		PrintError("注册 MCP 工具失败: %v", err)
		return
	}

	// 处理信号
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("MCP 消息服务启动", "tools", len(registry.ListTools()))

	// 运行 MCP stdio 循环（阻塞直到 stdin 关闭或收到信号）
	if err := server.RunStdioLoop(ctx); err != nil {
		slog.Warn("MCP 服务退出", "err", err)
	}
}
