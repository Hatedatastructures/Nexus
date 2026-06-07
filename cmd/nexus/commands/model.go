package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"nexus-agent/internal/config"
	"nexus-agent/internal/llm"
)

// ModelCommand 实现 nexus model 命令。
type ModelCommand struct{}

func (c *ModelCommand) Name() string     { return "model" }
func (c *ModelCommand) Synopsis() string { return "交互式选择默认模型" }

func (c *ModelCommand) Run(args []string) {
	cfg, err := config.Load("")
	if err != nil {
		PrintError("加载配置失败: %v", err)
	}

	PrintTitle("选择默认模型")
	fmt.Println()

	// 显示当前配置
	fmt.Printf("  当前模型: %s\n", cfg.Agent.Model)
	fmt.Printf("  当前提供者: %s\n", cfg.Agent.Provider)
	fmt.Println()

	// 列出可用提供者
	fmt.Println(GreenBold.Render("  可用提供者:"))
	providerNames := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		providerNames = append(providerNames, name)
	}

	for i, name := range providerNames {
		isDefault := ""
		if name == cfg.Agent.Provider {
			isDefault = " (当前)"
		}
		fmt.Printf("    %d. %s%s\n", i+1, name, isDefault)
	}
	fmt.Println()

	// 选择提供者
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("  请选择提供者 (输入编号或名称): ")
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	var selectedProvider string
	if choice == "" {
		selectedProvider = cfg.Agent.Provider
	} else {
		// 尝试解析为编号
		var idx int
		if _, err := fmt.Sscanf(choice, "%d", &idx); err == nil && idx > 0 && idx <= len(providerNames) {
			selectedProvider = providerNames[idx-1]
		} else {
			// 直接使用名称
			selectedProvider = choice
		}
	}

	// 验证提供者存在
	providerConfig, exists := cfg.Providers[selectedProvider]
	if !exists {
		PrintError("提供者 %q 不存在", selectedProvider)
	}

	// 列出可用模型
	fmt.Println()
	fmt.Printf("  正在获取 %s 的可用模型...\n", selectedProvider)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 创建提供者实例
	var provider llm.Provider
	switch providerConfig.APIMode {
	case "openai", "chat_completions":
		provider = llm.NewOpenAIProvider(nil, providerConfig.APIKey, "", providerConfig.BaseURL)
	case "anthropic", "anthropic_messages":
		provider = llm.NewAnthropicProvider(nil, providerConfig.APIKey, "", providerConfig.BaseURL)
	case "gemini":
		provider = llm.NewGeminiProvider(nil, providerConfig.APIKey, "", providerConfig.BaseURL)
	default:
		provider = llm.NewOpenAIProvider(nil, providerConfig.APIKey, "", providerConfig.BaseURL)
	}

	if provider == nil {
		PrintError("无法创建提供者实例")
	}

	// 尝试列出模型
	models, err := provider.ListModels(ctx)
	if err != nil {
		fmt.Println(DimStyle.Render("  无法获取模型列表，将手动输入模型名称"))
		fmt.Println()
		fmt.Print("  请输入模型名称: ")
		modelName, _ := reader.ReadString('\n')
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			PrintError("模型名称不能为空")
		}

		// 更新配置
		c.updateConfig(selectedProvider, modelName)
		return
	}

	// 显示模型列表
	fmt.Println()
	fmt.Println(GreenBold.Render("  可用模型:"))
	for i, model := range models {
		fmt.Printf("    %d. %s\n", i+1, model.ID)
	}
	fmt.Println()

	// 选择模型
	fmt.Print("  请选择模型 (输入编号): ")
	modelChoice, _ := reader.ReadString('\n')
	modelChoice = strings.TrimSpace(modelChoice)

	var selectedModel string
	var modelIdx int
	if _, err := fmt.Sscanf(modelChoice, "%d", &modelIdx); err == nil && modelIdx > 0 && modelIdx <= len(models) {
		selectedModel = models[modelIdx-1].ID
	} else {
		selectedModel = modelChoice
	}

	if selectedModel == "" {
		PrintError("未选择模型")
	}

	// 更新配置
	c.updateConfig(selectedProvider, selectedModel)
}

func (c *ModelCommand) updateConfig(provider, model string) {
	cfgPath := GetConfigPath()

	// 读取现有配置
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		PrintError("读取配置文件失败: %v", err)
	}

	// 简单替换 (实际应该解析 YAML)
	content := string(data)

	// 替换 agent.model
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "model:") {
			lines[i] = fmt.Sprintf("  model: \"%s\"", model)
		}
		if strings.HasPrefix(strings.TrimSpace(line), "provider:") {
			lines[i] = fmt.Sprintf("  provider: \"%s\"", provider)
		}
	}

	newContent := strings.Join(lines, "\n")

	// 写回配置
	if err := os.WriteFile(cfgPath, []byte(newContent), 0600); err != nil {
		PrintError("写入配置文件失败: %v", err)
	}

	PrintSuccess("默认模型已更新")
	fmt.Printf("  提供者: %s\n", provider)
	fmt.Printf("  模型: %s\n", model)
}
