package commands

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"nexus-agent/internal/config"
	"nexus-agent/internal/tool"
)

// ConfigCommand 实现 nexus config 命令。
type ConfigCommand struct{}

func (c *ConfigCommand) Name() string    { return "config" }
func (c *ConfigCommand) Synopsis() string { return "配置管理 (show/validate/edit/set/path)" }

func (c *ConfigCommand) Run(args []string) {
	if len(args) == 0 {
		c.showConfig()
		return
	}

	switch args[0] {
	case "show":
		c.showConfig()
	case "validate":
		c.validateConfig()
	case "edit":
		c.editConfig()
	case "set":
		if len(args) < 3 {
			PrintError("用法: nexus config set <key> <value>")
		}
		c.setConfig(args[1], args[2])
	case "path":
		fmt.Println(GetConfigPath())
	default:
		PrintError("未知子命令: %s", args[0])
	}
}

func (c *ConfigCommand) showConfig() {
	cfg, err := config.Load("")
	if err != nil {
		PrintError("加载配置失败: %v", err)
	}

	PrintTitle("当前配置")

	// 代理配置
	PrintSection("Agent")
	fmt.Printf("    模型:       %s\n", cfg.Agent.Model)
	fmt.Printf("    提供者:     %s\n", cfg.Agent.Provider)
	fmt.Printf("    最大 Token:  %d\n", cfg.Agent.MaxTokens)
	fmt.Printf("    最大迭代:    %d\n", cfg.Agent.MaxIterations)
	fmt.Printf("    备选模型:    %s\n", cfg.Agent.FallbackModel)
	fmt.Println()

	// 提供者配置（脱敏）
	PrintSection("Providers")
	if len(cfg.Providers) == 0 {
		fmt.Printf("    %s\n", DimStyle.Render("无已配置的提供者"))
	} else {
		for name, p := range cfg.Providers {
			maskedKey := MaskAPIKey(p.APIKey)
			fmt.Printf("    %-20s  %s\n", name, maskedKey)
			fmt.Printf("      BaseURL: %s\n", p.BaseURL)
			fmt.Printf("      APIMode: %s\n", p.APIMode)
		}
	}
	fmt.Println()

	// 工具配置
	PrintSection("Tools")
	fmt.Printf("    结果最大字符: %d\n", cfg.Tools.ResultMaxChars)
	fmt.Printf("    搜索后端:     %s\n", cfg.Tools.WebSearchBackend)
	fmt.Println()

	// 网关配置
	PrintSection("Gateway")
	if cfg.Gateway.Enabled {
		fmt.Printf("    %s\n", GreenBold.Render("● 已启用"))
	} else {
		fmt.Printf("    %s\n", DimStyle.Render("○ 未启用"))
	}
	fmt.Println()

	// 日志配置
	PrintSection("Logging")
	fmt.Printf("    级别: %s\n", cfg.Logging.Level)
	fmt.Printf("    格式: %s\n", cfg.Logging.Format)
}

func (c *ConfigCommand) validateConfig() {
	cfg, err := config.Load("")
	if err != nil {
		PrintError("加载配置失败: %v", err)
	}

	valid := true
	var issues []string

	// 检查至少有一个提供者
	if len(cfg.Providers) == 0 {
		issues = append(issues, "未配置任何 LLM 提供者")
		valid = false
	}

	// 检查每个提供者
	for name, p := range cfg.Providers {
		if p.APIKey == "" {
			issues = append(issues, fmt.Sprintf("提供者 %q 缺少 API Key", name))
			valid = false
		}
		if p.BaseURL == "" {
			issues = append(issues, fmt.Sprintf("提供者 %q 缺少 BaseURL", name))
			valid = false
		}
		if p.APIMode == "" {
			issues = append(issues, fmt.Sprintf("提供者 %q 未指定 APIMode", name))
		}
	}

	// 检查默认模型
	if cfg.Agent.Model == "" {
		issues = append(issues, "未指定默认模型 (agent.model)")
		valid = false
	}

	// 检查工具集配置
	for _, ts := range cfg.Tools.EnabledToolsets {
		if _, ok := tool.DefaultToolsets[ts]; !ok {
			issues = append(issues, fmt.Sprintf("未知工具集: %q", ts))
			valid = false
		}
	}

	// 检查结果
	PrintTitle("配置验证结果")

	if valid {
		PrintSuccess("配置文件有效")
	} else {
		fmt.Println(ErrorStyle.Render("  ✖ 配置文件存在问题"))
		fmt.Println()
		for _, issue := range issues {
			fmt.Printf("    %s %s\n", ErrorStyle.Render("•"), issue)
		}
		os.Exit(1)
	}

	fmt.Printf("\n  提供者数量: %d\n", len(cfg.Providers))
	fmt.Printf("  默认模型:   %s\n", cfg.Agent.Model)
	fmt.Printf("  沙箱后端:   %s\n", cfg.Sandbox.Backend)
	fmt.Printf("  审批模式:   %s\n", cfg.Approval.Mode)
	fmt.Println()
}

func (c *ConfigCommand) editConfig() {
	cfgPath := GetConfigPath()

	// 获取编辑器
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		// 尝试常见编辑器
		for _, e := range []string{"vim", "nano", "vi", "notepad"} {
			if _, err := exec.LookPath(e); err == nil {
				editor = e
				break
			}
		}
	}

	if editor == "" {
		PrintError("未找到编辑器，请设置 EDITOR 环境变量")
	}

	editor = sanitizeEditorPath(editor)

	fmt.Printf("  使用 %s 编辑配置文件\n", editor)
	fmt.Printf("  文件路径: %s\n", cfgPath)
	fmt.Println()

	cmd := exec.Command(editor, cfgPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		PrintError("编辑器退出失败: %v", err)
	}
}

func sanitizeEditorPath(editor string) string {
	if idx := strings.IndexByte(editor, ' '); idx >= 0 {
		editor = editor[:idx]
	}
	resolved, err := exec.LookPath(editor)
	if err != nil {
		return "vi"
	}
	return resolved
}

func (c *ConfigCommand) setConfig(key string, value string) {
	cfgPath := GetConfigPath()

	// 读取配置文件
	if _, err := os.ReadFile(cfgPath); err != nil {
		PrintError("读取配置文件失败: %v", err)
	}

	// 简单的键值对设置 (支持 agent.model 格式)
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		PrintError("键格式错误，应为 section.key (如 agent.model)")
	}

	// 这里简化处理，实际应该解析 YAML 并修改
	fmt.Println(DimStyle.Render("  config set 功能开发中..."))
	fmt.Printf("  键: %s\n", key)
	fmt.Printf("  值: %s\n", value)
	fmt.Printf("  文件: %s\n", cfgPath)
}

func init() {
	Register(&ConfigCommand{})
}
