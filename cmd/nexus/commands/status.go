package commands

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"nexus-agent/internal/config"
	"nexus-agent/internal/state"
	"nexus-agent/internal/tool"
)

// StatusCommand 实现 nexus status 命令。
type StatusCommand struct{}

func (c *StatusCommand) Name() string    { return "status" }
func (c *StatusCommand) Synopsis() string { return "显示组件状态总览" }

func (c *StatusCommand) Run(args []string) {
	PrintTitle("Nexus 状态总览")
	fmt.Println()

	// 配置文件状态
	c.checkConfig()

	// LLM 提供者状态
	c.checkProviders()

	// 工具状态
	c.checkTools()

	// 会话统计
	c.checkSessions()

	// 网关状态
	c.checkGateway()

	// Cron 状态
	c.checkCron()

	fmt.Println()
}

func (c *StatusCommand) checkConfig() {
	PrintSection("配置文件")
	cfgPath := GetConfigPath()
	if FileExists(cfgPath) {
		info, err := os.Stat(cfgPath)
		if err == nil {
			fmt.Printf("    路径: %s\n", cfgPath)
			fmt.Printf("    修改时间: %s\n", info.ModTime().Format("2006-01-02 15:04:05"))
			PrintSuccess("配置文件存在")
		}
	} else {
		PrintWarning("    配置文件不存在: %s", cfgPath)
	}
	fmt.Println()
}

func (c *StatusCommand) checkProviders() {
	PrintSection("LLM 提供者")
	cfg, err := config.Load("")
	if err != nil {
		PrintWarning("    无法加载配置: %v", err)
		fmt.Println()
		return
	}

	if len(cfg.Providers) == 0 {
		PrintWarning("    无已配置的提供者")
	} else {
		for name, p := range cfg.Providers {
			status := DimStyle.Render("○")
			if p.APIKey != "" && p.BaseURL != "" {
				status = GreenBold.Render("●")
			}
			fmt.Printf("    %s %s (BaseURL: %s)\n", status, name, p.BaseURL)
		}
	}
	fmt.Printf("    默认模型: %s\n", cfg.Agent.Model)
	fmt.Printf("    默认提供者: %s\n", cfg.Agent.Provider)
	fmt.Println()
}

func (c *StatusCommand) checkTools() {
	PrintSection("工具系统")
	tool.DiscoverBuiltin()
	registry := tool.GetRegistry()
	names := registry.ListTools()

	available := 0
	for _, name := range names {
		entry := registry.GetEntry(name)
		if entry != nil && entry.Tool.IsAvailable() {
			available++
		}
	}

	fmt.Printf("    已注册: %d 个\n", len(names))
	fmt.Printf("    可用:   %d 个\n", available)
	fmt.Println()
}

func (c *StatusCommand) checkSessions() {
	PrintSection("会话统计")
	store, err := state.NewStore(GetDBPath())
	if err != nil {
		PrintWarning("    无法打开数据库: %v", err)
		fmt.Println()
		return
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessions, err := store.ListRecentSessions(ctx, 100)
	if err != nil {
		PrintWarning("    查询会话失败: %v", err)
		fmt.Println()
		return
	}

	totalMessages := 0
	totalTokens := 0
	for _, s := range sessions {
		totalMessages += s.MessageCount
		totalTokens += s.InputTokens + s.OutputTokens
	}

	fmt.Printf("    会话总数: %d\n", len(sessions))
	fmt.Printf("    消息总数: %d\n", totalMessages)
	fmt.Printf("    Token 总量: %d\n", totalTokens)
	fmt.Println()
}

func (c *StatusCommand) checkGateway() {
	PrintSection("网关状态")
	cfg, err := config.Load("")
	if err != nil {
		PrintWarning("    无法加载配置")
		fmt.Println()
		return
	}

	if cfg.Gateway.Enabled {
		fmt.Printf("    %s\n", GreenBold.Render("● 已启用"))
		if len(cfg.Gateway.Platforms) > 0 {
			platforms := make([]string, 0, len(cfg.Gateway.Platforms))
			for _, p := range cfg.Gateway.Platforms {
				platforms = append(platforms, p.Platform)
			}
			fmt.Printf("    平台: %s\n", strings.Join(platforms, ", "))
		}
	} else {
		fmt.Printf("    %s\n", DimStyle.Render("○ 未启用"))
	}
	fmt.Println()
}

func (c *StatusCommand) checkCron() {
	PrintSection("Cron 调度器")
	cfg, err := config.Load("")
	if err != nil {
		PrintWarning("    无法加载配置")
		fmt.Println()
		return
	}

	if cfg.Cron.Enabled {
		fmt.Printf("    %s\n", GreenBold.Render("● 已启用"))
		fmt.Printf("    最大并行任务: %d\n", cfg.Cron.MaxParallelJobs)
		fmt.Printf("    检测间隔: %d 秒\n", cfg.Cron.TickIntervalSecs)
	} else {
		fmt.Printf("    %s\n", DimStyle.Render("○ 未启用"))
	}
	fmt.Println()
}

func init() {
	Register(&StatusCommand{})
}
