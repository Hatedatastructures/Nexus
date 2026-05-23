// Nexus Agent Gateway 入口点。
// 启动多平台消息网关，支持 Telegram/Discord/Slack/WhatsApp/WeChat/飞书/钉钉。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"nexus-agent/internal/agent"
	"nexus-agent/internal/config"
	"nexus-agent/internal/cron"
	"nexus-agent/internal/gateway"
	"nexus-agent/internal/state"
	"nexus-agent/internal/tool"
	"nexus-agent/pkg/logutil"
)

func main() {
	// 1. 加载完整配置
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 2. 初始化日志
	closeFn := logutil.InitLogger(cfg.Logging.Level, cfg.Logging.Format, cfg.Logging.Dir)
	defer closeFn()

	if !cfg.Gateway.Enabled {
		fmt.Println("网关未启用，请在配置中设置 gateway.enabled=true")
		os.Exit(1)
	}

	slog.Info("nexus gateway starting",
		"platforms", len(cfg.Gateway.Platforms),
	)

	// 3. 创建 State Store
	statePath := filepath.Join(homeDir(), ".nexus", "state.db")
	st, err := state.NewStore(statePath)
	if err != nil {
		slog.Error("创建状态存储失败", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// 执行数据库迁移
	if err := state.RunMigrations(context.Background(), st.DB()); err != nil {
		slog.Error("数据库迁移失败", "err", err)
		os.Exit(1)
	}

	// 3b. 注入子代理执行器 (供 delegate_task 工具使用)
	tool.SetSubAgentRunner(func(ctx context.Context, systemPrompt, task string) (string, error) {
		subAgent := agent.NewAgent(
			agent.WithConfigProvider(cfg),
			agent.WithToolRegistry(tool.GetRegistry()),
			agent.WithMaxIterations(15),
		)
		if subAgent.Provider() == nil {
			return "", fmt.Errorf("子代理 LLM 提供者未初始化")
		}
		result, err := subAgent.RunConversation(ctx, task, nil, systemPrompt)
		if err != nil {
			return "", err
		}
		return result.FinalResponse, nil
	})

	// 4. 创建 Cron Scheduler
	cronSched, err := createCronScheduler(cfg, st)
	if err != nil {
		slog.Warn("创建 cron 调度器失败，将跳过 cron 功能", "err", err)
		cronSched = nil
	}

	// 5. 创建 GatewayRunner
	runner := gateway.NewGatewayRunner(&cfg.Gateway, cfg, st, cronSched)

	// 6. 根据配置注册平台适配器 (从全局注册中心自动发现)
	runner.RegisterFromRegistry(&cfg.Gateway)

	// 7. 启动 GatewayRunner
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := runner.Start(ctx); err != nil {
		slog.Error("启动网关运行器失败", "err", err)
		os.Exit(1)
	}

	slog.Info("nexus gateway started")

	// 8. 等待关闭信号后优雅关闭
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("收到关闭信号，正在优雅关闭...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := runner.Stop(shutdownCtx); err != nil {
		slog.Error("网关关闭过程中出错", "err", err)
	}

	slog.Info("nexus gateway stopped")
}

// homeDir 返回当前用户主目录，失败时返回 "."。
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// createCronScheduler 根据配置创建 cron 调度器。
func createCronScheduler(cfg *config.Config, st *state.Store) (*cron.Scheduler, error) {
	if !cfg.Cron.Enabled {
		return nil, nil
	}

	cronDir := filepath.Join(homeDir(), ".nexus", "cron")
	jobMgr := cron.NewJobManager(st, cronDir)
	executor := cron.NewExecutor(cronDir, &cfg.Agent)

	return cron.NewScheduler(jobMgr, executor), nil
}

