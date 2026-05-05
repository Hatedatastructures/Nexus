package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// SetupCommand 实现 nexus setup 命令。
type SetupCommand struct{}

func (c *SetupCommand) Name() string    { return "setup" }
func (c *SetupCommand) Synopsis() string { return "交互式设置向导" }

func (c *SetupCommand) Run(args []string) {
	PrintTitle("Nexus 设置向导")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	// 检查是否已有配置
	cfgPath := GetConfigPath()
	if FileExists(cfgPath) {
		fmt.Println(DimStyle.Render("  检测到已有配置文件"))
		fmt.Print("  是否重新配置? (y/N): ")
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println(DimStyle.Render("  取消配置"))
			return
		}
	}

	// 选择提供者
	fmt.Println()
	fmt.Println(GreenBold.Render("  [1/3] 选择 LLM 提供者"))
	fmt.Println()
	fmt.Println("  可用提供者:")
	fmt.Println("    1. Anthropic (Claude)")
	fmt.Println("    2. OpenAI (GPT)")
	fmt.Println("    3. Google (Gemini)")
	fmt.Println("    4. 自定义 (OpenAI 兼容)")
	fmt.Println()

	fmt.Print("  请选择 (1-4): ")
	providerChoice, _ := reader.ReadString('\n')
	providerChoice = strings.TrimSpace(providerChoice)

	var providerName, apiMode, defaultModel string
	switch providerChoice {
	case "1":
		providerName = "anthropic"
		apiMode = "anthropic_messages"
		defaultModel = "claude-sonnet-4-6"
	case "2":
		providerName = "openai"
		apiMode = "chat_completions"
		defaultModel = "gpt-4o"
	case "3":
		providerName = "gemini"
		apiMode = "gemini"
		defaultModel = "gemini-pro"
	case "4":
		providerName = "custom"
		apiMode = "chat_completions"
		defaultModel = "gpt-4"
	default:
		PrintError("无效选择")
	}

	// 输入 API Key
	fmt.Println()
	fmt.Printf("  请输入 %s 的 API Key: ", providerName)
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)

	if apiKey == "" {
		PrintError("API Key 不能为空")
	}

	// 输入 Base URL (可选)
	defaultBaseURL := getDefaultBaseURL(providerName)
	fmt.Printf("  请输入 Base URL (默认: %s): ", defaultBaseURL)
	baseURL, _ := reader.ReadString('\n')
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	// 输入模型名称 (可选)
	fmt.Printf("  请输入模型名称 (默认: %s): ", defaultModel)
	model, _ := reader.ReadString('\n')
	model = strings.TrimSpace(model)
	if model == "" {
		model = defaultModel
	}

	// 生成配置
	fmt.Println()
	fmt.Println(GreenBold.Render("  [2/3] 生成配置文件"))
	fmt.Println()

	cfg := generateConfig(providerName, apiMode, apiKey, baseURL, model)

	// 保存配置
	if err := saveConfig(cfgPath, cfg); err != nil {
		PrintError("保存配置失败: %v", err)
	}

	PrintSuccess("配置文件已生成")
	fmt.Printf("  路径: %s\n", cfgPath)

	// 完成
	fmt.Println()
	fmt.Println(GreenBold.Render("  [3/3] 完成"))
	fmt.Println()
	fmt.Println("  现在可以使用以下命令:")
	fmt.Println("    nexus chat     - 开始对话")
	fmt.Println("    nexus status   - 查看状态")
	fmt.Println("    nexus doctor   - 系统诊断")
	fmt.Println()
}

func getDefaultBaseURL(provider string) string {
	switch provider {
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "openai":
		return "https://api.openai.com/v1"
	case "gemini":
		return "https://generativelanguage.googleapis.com/v1beta"
	default:
		return "http://localhost:11434/v1"
	}
}

func generateConfig(provider, apiMode, apiKey, baseURL, model string) string {
	return fmt.Sprintf(`# Nexus Agent 配置文件

agent:
  model: "%s"
  provider: "%s"
  max_tokens: 4096
  max_iterations: 90

providers:
  %s:
    api_key: "%s"
    base_url: "%s"
    api_mode: "%s"

tools:
  enabled_toolsets:
    - core
    - developer

logging:
  level: "info"
  format: "text"

sandbox:
  backend: "local"

approval:
  mode: "smart"
`, model, provider, provider, apiKey, baseURL, apiMode)
}

func saveConfig(path string, content string) error {
	// 确保目录存在
	if err := os.MkdirAll(GetNexusHome(), 0755); err != nil {
		return err
	}

	return os.WriteFile(path, []byte(content), 0644)
}

func init() {
	Register(&SetupCommand{})
}
