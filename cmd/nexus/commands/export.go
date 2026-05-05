package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"nexus-agent/internal/config"
)

// ExportCommand 实现 nexus export 命令。
type ExportCommand struct{}

func (c *ExportCommand) Name() string    { return "export" }
func (c *ExportCommand) Synopsis() string { return "导出数据 (memory/config)" }

func (c *ExportCommand) Run(args []string) {
	if len(args) == 0 {
		fmt.Println("用法: nexus export <subcommand>")
		fmt.Println("可用子命令:")
		fmt.Println("  memory      - 导出 MEMORY.md 内容")
		fmt.Println("  config      - 导出当前配置（脱敏）")
		return
	}

	switch args[0] {
	case "memory":
		c.exportMemory()
	case "config":
		c.exportConfig()
	default:
		PrintError("未知子命令: %s", args[0])
	}
}

func (c *ExportCommand) exportMemory() {
	nexusHome := GetNexusHome()
	memPath := nexusHome + "/MEMORY.md"

	data, err := os.ReadFile(memPath)
	if err != nil {
		PrintError("读取 MEMORY.md 失败: %v", err)
	}

	// 直接输出原始内容（便于管道重定向）
	fmt.Print(string(data))
}

func (c *ExportCommand) exportConfig() {
	cfg, err := config.Load("")
	if err != nil {
		PrintError("加载配置失败: %v", err)
	}

	// 创建脱敏副本
	type ProviderConfigExport struct {
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		APIMode  string `json:"api_mode"`
		OAuthURL string `json:"oauth_url,omitempty"`
	}
	type ConfigExport struct {
		Agent     map[string]any                  `json:"agent"`
		Providers map[string]ProviderConfigExport `json:"providers"`
		Gateway   map[string]any                  `json:"gateway"`
		Tools     map[string]any                  `json:"tools"`
		Memory    map[string]any                  `json:"memory"`
		Logging   map[string]any                  `json:"logging"`
		Approval  map[string]any                  `json:"approval"`
		Sandbox   map[string]any                  `json:"sandbox"`
		Cron      map[string]any                  `json:"cron"`
	}

	export := ConfigExport{
		Agent:     map[string]any{"model": cfg.Agent.Model, "provider": cfg.Agent.Provider, "max_tokens": cfg.Agent.MaxTokens, "max_iterations": cfg.Agent.MaxIterations, "fallback_model": cfg.Agent.FallbackModel},
		Providers: make(map[string]ProviderConfigExport),
		Gateway:   map[string]any{"enabled": cfg.Gateway.Enabled},
		Tools:     map[string]any{"result_max_chars": cfg.Tools.ResultMaxChars, "web_search_backend": cfg.Tools.WebSearchBackend},
		Memory:    map[string]any{"memory_max_chars": cfg.Memory.MemoryMaxChars, "user_max_chars": cfg.Memory.UserMaxChars},
		Logging:   map[string]any{"level": cfg.Logging.Level, "format": cfg.Logging.Format},
		Approval:  map[string]any{"mode": cfg.Approval.Mode, "cron_mode": cfg.Approval.CronMode},
		Sandbox:   map[string]any{"backend": cfg.Sandbox.Backend, "timeout_secs": cfg.Sandbox.TimeoutSecs},
		Cron:      map[string]any{"enabled": cfg.Cron.Enabled, "max_parallel_jobs": cfg.Cron.MaxParallelJobs, "tick_interval_secs": cfg.Cron.TickIntervalSecs},
	}

	for name, p := range cfg.Providers {
		export.Providers[name] = ProviderConfigExport{
			BaseURL:  p.BaseURL,
			APIKey:   MaskAPIKey(p.APIKey),
			APIMode:  p.APIMode,
			OAuthURL: p.OAuthURL,
		}
	}

	jsonData, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		PrintError("序列化配置失败: %v", err)
	}

	fmt.Println(string(jsonData))
}

func init() {
	Register(&ExportCommand{})
}
