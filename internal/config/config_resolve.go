// Package config 提供 Nexus Agent 的配置加载和管理。
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ───────────────────────────── 配置加载 ─────────────────────────────

// Load 从指定路径 (空 = 默认路径) 加载配置文件。
// 默认搜索顺序: 当前目录 config.yaml → ~/.nexus/config.yaml。
func Load(path string) (*Config, error) {
	if path == "" {
		// 尝试当前目录
		if _, err := os.Stat("config.yaml"); err == nil {
			path = "config.yaml"
		} else {
			// 尝试用户主目录
			home, homeErr := os.UserHomeDir()
			if homeErr == nil {
				candidate := filepath.Join(home, ".nexus", "config.yaml")
				if _, err := os.Stat(candidate); err == nil {
					path = candidate
				}
			}
		}
	}

	cfg := defaultConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取配置文件 %s: %w", path, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析配置文件 %s: %w", path, err)
		}
	}

	// 环境变量覆盖
	expandEnv(cfg)

	return cfg, nil
}

// expandEnv 递归展开配置值中的 ${ENV_VAR} 引用。
func expandEnv(cfg *Config) {
	// 展开代理配置中的环境变量引用
	cfg.Agent.Proxy = expandEnvString(cfg.Agent.Proxy)

	for k, p := range cfg.Providers {
		p.APIKey = expandEnvString(p.APIKey)
		p.BaseURL = expandEnvString(p.BaseURL)
		p.OAuthURL = expandEnvString(p.OAuthURL)
		cfg.Providers[k] = p
	}
	for k := range cfg.Gateway.Platforms {
		cfg.Gateway.Platforms[k].Token = expandEnvString(cfg.Gateway.Platforms[k].Token)
	}

	// 回退链中的 APIKey 也需要展开
	for i := range cfg.Agent.FallbackChain {
		cfg.Agent.FallbackChain[i].APIKey = expandEnvString(cfg.Agent.FallbackChain[i].APIKey)
		cfg.Agent.FallbackChain[i].BaseURL = expandEnvString(cfg.Agent.FallbackChain[i].BaseURL)
	}
	if cfg.Agent.FallbackModelCfg != nil {
		cfg.Agent.FallbackModelCfg.APIKey = expandEnvString(cfg.Agent.FallbackModelCfg.APIKey)
		cfg.Agent.FallbackModelCfg.BaseURL = expandEnvString(cfg.Agent.FallbackModelCfg.BaseURL)
	}
}

func expandEnvString(s string) string {
	return os.Expand(s, func(key string) string {
		return os.Getenv(key)
	})
}

// defaultConfig 返回默认配置。
func defaultConfig() *Config {
	return &Config{
		Agent: AgentConfig{
			MaxIterations: 90,
			MaxTokens:     8000,
		},
		Tools: ToolsConfig{
			ResultMaxChars: 50000,
		},
		Memory: MemoryConfig{
			MemoryMaxChars: 2200,
			UserMaxChars:   1375,
		},
		Cron: CronConfig{
			Enabled:          false,
			MaxParallelJobs:  3,
			TickIntervalSecs: 60,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
		Approval: ApprovalConfig{
			Mode:     "smart",
			CronMode: "always",
		},
		Sandbox: SandboxConfig{
			Backend:     "local",
			TimeoutSecs: 120,
		},
		// ── 扩展配置默认值 ──
		Trajectory: TrajectoryConfig{
			Format: "sharegpt",
		},
		Redact: RedactConfig{
			Enabled: true,
		},
		Batch: BatchConfig{
			MaxWorkers:         4,
			CheckpointInterval: 300,
		},
		ToolOutput: ToolOutputConfig{
			MaxBytes:      50000,
			MaxLines:      2000,
			MaxLineLength: 2000,
		},
	}
}

// ───────────────────────────── 配置解析 ─────────────────────────────

// ResolveProvider 根据名称返回提供者配置。
func (c *Config) ResolveProvider(name string) (ProviderConfig, error) {
	if p, ok := c.Providers[name]; ok {
		return p, nil
	}
	return ProviderConfig{}, fmt.Errorf("提供者 %q 未在配置中定义", name)
}

// ResolveModel 根据名称返回模型配置，未找到时返回空结构体。
func (c *Config) ResolveModel(name string) (ModelConfig, bool) {
	m, ok := c.Models[name]
	return m, ok
}
