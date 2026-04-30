// Nexus Agent ACP (Agent Control Protocol) 入口点。
// 通过标准输入/输出实现 MCP 协议，供编辑器 (VS Code/Cursor) 集成。
// 完整的 MCP Server 生命周期: 加载配置 → 初始化工具注册 → 启动 stdin/stdout 循环。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"nexus-agent/internal/config"
	"nexus-agent/internal/mcp"
	"nexus-agent/internal/tool"
)

func main() {
	closeFn := initLogger()
	defer closeFn()

	// 1. 加载配置
	cfg, err := config.Load("")
	if err != nil {
		slog.Error("加载配置失败", "err", err)
		sendError(fmt.Sprintf("配置加载失败: %v", err))
		os.Exit(1)
	}
	slog.Info("配置加载成功",
		"providers", len(cfg.Providers),
		"model", cfg.Agent.Model,
	)

	// 2. 初始化工具注册中心
	tool.DiscoverBuiltin()
	registry := tool.GetRegistry()
	slog.Info("工具注册中心已初始化", "count", len(registry.ListTools()))

	// 3. 创建 MCP Server
	server := mcp.NewSafeMCPServer(
		mcp.ServerInfo{
			Name:    "nexus-agent",
			Version: "1.0.0",
		},
		mcp.NewToolRegistryAdapter(registry),
	)

	// 批量注册所有工具到 MCP 服务器
	ctx := context.Background()
	if err := server.RegisterAllTools(ctx); err != nil {
		slog.Error("工具注册失败", "err", err)
		sendError(fmt.Sprintf("工具注册失败: %v", err))
		os.Exit(1)
	}

	slog.Info("MCP Server 初始化完成")

	// 4. 启动 stdin/stdout 循环
	sigCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// 发送初始化完成信号
	sendMessage(map[string]any{
		"jsonrpc": "2.0",
		"method":  "initialized",
	})

	slog.Info("进入 MCP 请求循环")

	if err := server.RunStdioLoop(sigCtx); err != nil {
		if sigCtx.Err() != nil {
			slog.Info("MCP Server 正常退出")
		} else {
			slog.Error("MCP 循环异常", "err", err)
		}
	}
}

// initLogger 初始化 slog 日志系统。
func initLogger() func() {
	level := os.Getenv("NEXUS_LOG_LEVEL")
	if level == "" {
		level = "info"
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLogLevel(level),
	})
	slog.SetDefault(slog.New(handler))

	return func() {}
}

// parseLogLevel 将日志级别字符串转换为 slog.Level
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// sendMessage 向 stdout 发送 JSON-RPC 消息。
func sendMessage(msg map[string]any) {
	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "JSON 序列化失败: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

// sendError 向 stdout 发送错误通知。
func sendError(message string) {
	sendMessage(map[string]any{
		"jsonrpc": "2.0",
		"method":  "error",
		"params": map[string]any{
			"code":    -32603,
			"message": message,
		},
	})
}
