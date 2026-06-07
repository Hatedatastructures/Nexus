package commands

import (
	"context"
	"fmt"
	"time"

	"nexus-agent/internal/config"
	"nexus-agent/internal/llm"
)

// ProviderCommand 实现 nexus provider 命令。
type ProviderCommand struct{}

func (c *ProviderCommand) Name() string     { return "provider" }
func (c *ProviderCommand) Synopsis() string { return "LLM 提供者管理 (list/test)" }

func (c *ProviderCommand) Run(args []string) {
	if len(args) == 0 {
		c.listProviders()
		return
	}

	switch args[0] {
	case "list", "ls":
		c.listProviders()
	case "test":
		if len(args) < 2 {
			PrintError("用法: nexus provider test <name>")
		}
		c.testProvider(args[1])
	default:
		PrintError("未知子命令: %s", args[0])
	}
}

func (c *ProviderCommand) listProviders() {
	cfg, err := config.Load("")
	if err != nil {
		PrintError("加载配置失败: %v", err)
	}

	PrintTitle("已配置的 LLM 提供者")

	if len(cfg.Providers) == 0 {
		fmt.Println(DimStyle.Render("  无已配置的提供者"))
		fmt.Println()
		fmt.Println(DimStyle.Render("  提示: 在 ~/.nexus/config.yaml 中配置 providers"))
		return
	}

	for name, p := range cfg.Providers {
		isDefault := ""
		if name == cfg.Agent.Provider {
			isDefault = GreenBold.Render(" (默认)")
		}
		maskedKey := MaskAPIKey(p.APIKey)
		fmt.Printf("  %s%s\n", GreenBold.Render(name), isDefault)
		fmt.Printf("    BaseURL:  %s\n", p.BaseURL)
		fmt.Printf("    APIMode:  %s\n", p.APIMode)
		fmt.Printf("    API Key:  %s\n", maskedKey)
		if p.OAuthURL != "" {
			fmt.Printf("    OAuth:    %s\n", p.OAuthURL)
		}
		fmt.Println()
	}
	fmt.Printf("  共 %d 个提供者\n", len(cfg.Providers))
}

func (c *ProviderCommand) testProvider(name string) {
	cfg, err := config.Load("")
	if err != nil {
		PrintError("加载配置失败: %v", err)
	}

	pc, err := cfg.ResolveProvider(name)
	if err != nil {
		PrintError("提供者错误: %v", err)
	}

	fmt.Printf("正在测试提供者 %q ...\n", name)
	fmt.Printf("  BaseURL: %s\n", pc.BaseURL)
	fmt.Printf("  APIMode: %s\n", pc.APIMode)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 创建提供者实例
	var provider llm.Provider
	switch pc.APIMode {
	case "openai", "chat_completions":
		provider = llm.NewOpenAIProvider(nil, pc.APIKey, "", pc.BaseURL)
	case "anthropic", "anthropic_messages":
		provider = llm.NewAnthropicProvider(nil, pc.APIKey, "", pc.BaseURL)
	case "gemini":
		provider = llm.NewGeminiProvider(nil, pc.APIKey, "", pc.BaseURL)
	default:
		provider = llm.NewOpenAIProvider(nil, pc.APIKey, "", pc.BaseURL)
	}

	if provider == nil {
		PrintError("无法创建提供者实例")
	}

	// 尝试列出模型
	_, err = provider.ListModels(ctx)
	if err != nil {
		fmt.Println()
		fmt.Println(ErrorStyle.Render(fmt.Sprintf("  ✖ 连接失败: %v", err)))
		return
	}

	fmt.Println()
	fmt.Println(GreenBold.Render("  ✓ 连接成功"))
}
