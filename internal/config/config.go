// Package config 提供 Nexus Agent 的配置加载和管理。
// 使用 Viper 加载 YAML 配置文件，支持环境变量覆盖。
// 配置优先级: 环境变量 > config.yaml > 嵌入式默认值。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// ───────────────────────────── 主配置 ─────────────────────────────

// Config 是 Nexus Agent 的完整配置结构体。
// 映射 ~/.nexus/config.yaml 的顶层结构。
type Config struct {
	Agent       AgentConfig              `yaml:"agent"`       // 代理配置
	Providers   map[string]ProviderConfig `yaml:"providers"`   // LLM 提供者配置
	Models      map[string]ModelConfig   `yaml:"models"`      // 模型特定配置
	Gateway     GatewayConfig            `yaml:"gateway"`     // 网关配置
	Tools       ToolsConfig              `yaml:"tools"`       // 工具配置
	Memory      MemoryConfig             `yaml:"memory"`      // 记忆配置
	Skills      SkillsConfig             `yaml:"skills"`      // 技能配置
	Cron        CronConfig               `yaml:"cron"`        // Cron 调度配置
	Logging     LoggingConfig            `yaml:"logging"`     // 日志配置
	Approval    ApprovalConfig           `yaml:"approval"`    // 命令审批配置
	Sandbox     SandboxConfig            `yaml:"sandbox"`     // 沙箱执行配置
	MCP         MCPConfig                `yaml:"mcp"`         // MCP 协议配置
	Credentials CredentialConfig         `yaml:"credentials"` // 凭证配置

	// ── 扩展配置 (Phase 1+ 模块预留) ──
	Plugins     PluginsConfig            `yaml:"plugins"`      // 插件系统配置
	Insights    InsightsConfig           `yaml:"insights"`     // 使用洞察配置
	Trajectory  TrajectoryConfig         `yaml:"trajectory"`   // 轨迹记录配置
	Redact      RedactConfig             `yaml:"redact"`       // 密钥脱敏配置
	Batch       BatchConfig              `yaml:"batch"`        // 批处理配置
	URLSafety   URLSafetyConfig          `yaml:"url_safety"`   // URL 安全配置
	ToolOutput  ToolOutputConfig         `yaml:"tool_output"`  // 工具输出限制配置
	ShellHooks  ShellHooksConfig         `yaml:"shell_hooks"`  // Shell Hook 配置
}

// ───────────────────────────── 代理配置 ─────────────────────────────

// AgentConfig 定义代理核心的运行时参数
type AgentConfig struct {
	Model          string `yaml:"model"`           // 默认模型名称
	Provider       string `yaml:"provider"`        // 默认提供者名称
	MaxTokens      int    `yaml:"max_tokens"`      // 最大生成 token 数
	MaxIterations  int    `yaml:"max_iterations"`  // 最大工具调用迭代次数 (默认 90)
	FallbackModel  string `yaml:"fallback_model"`  // 备选故障转移模型
	ToolDelay      float64 `yaml:"tool_delay"`     // 工具执行间隔 (秒)
	SaveTrajectory bool   `yaml:"save_trajectory"` // 是否保存轨迹到文件
}

// ───────────────────────────── 提供者配置 ─────────────────────────────

// ProviderConfig 定义 LLM 提供者的连接参数
type ProviderConfig struct {
	BaseURL  string `yaml:"base_url"`  // API 基础 URL
	APIKey   string `yaml:"api_key"`   // API 密钥 (支持 ${ENV_VAR} 引用)
	APIMode  string `yaml:"api_mode"`  // API 模式: chat_completions / anthropic_messages / bedrock_converse
	OAuthURL string `yaml:"oauth_url"` // OAuth 令牌端点 (可选)
}

// ───────────────────────────── 模型配置 ─────────────────────────────

// ModelConfig 定义单个模型的行为参数
type ModelConfig struct {
	ContextLimit int    `yaml:"context_limit"` // 上下文窗口大小
	MaxOutput    int    `yaml:"max_output"`    // 最大输出 token 数
	Vision       bool   `yaml:"vision"`        // 是否支持视觉
	Reasoning    bool   `yaml:"reasoning"`     // 是否支持推理
	Provider     string `yaml:"provider"`      // 所属提供者
}

// ───────────────────────────── 网关配置 ─────────────────────────────

// GatewayConfig 定义消息网关的运行参数
type GatewayConfig struct {
	Enabled  bool                           `yaml:"enabled"`  // 是否启用网关模式
	Platforms []PlatformEntry               `yaml:"platforms"` // 已启用的平台列表
	Cache    CacheConfig                    `yaml:"cache"`    // 代理缓存配置
	Stream   StreamConfig                   `yaml:"stream"`   // 流式投递配置
}

// PlatformEntry 定义单个平台的网关配置
type PlatformEntry struct {
	Platform string         `yaml:"platform"` // 平台类型: telegram / discord / slack / ...
	Enabled  bool           `yaml:"enabled"`  // 是否启用
	Token    string         `yaml:"token"`    // Bot Token (支持 ${ENV_VAR})
	Settings map[string]any `yaml:"settings"` // 平台特定设置
}

// CacheConfig 定义代理缓存参数
type CacheConfig struct {
	MaxSize int           `yaml:"max_size"` // 最大缓存会话数 (默认 128)
	IdleTTL time.Duration `yaml:"idle_ttl"` // 空闲超时驱逐 (默认 1h)
}

// StreamConfig 定义流式投递行为
type StreamConfig struct {
	Enabled      bool          `yaml:"enabled"`      // 是否启用流式投递
	BufferSize   int           `yaml:"buffer_size"`   // 缓冲区字符数 (触发编辑的阈值)
	EditInterval time.Duration `yaml:"edit_interval"` // 编辑间隔 (默认 1s)
}

// ───────────────────────────── 工具配置 ─────────────────────────────

// ToolsConfig 定义工具系统的配置
type ToolsConfig struct {
	EnabledToolsets  []string `yaml:"enabled_toolsets"`  // 默认启用的工具集
	DisabledToolsets []string `yaml:"disabled_toolsets"` // 默认禁用的工具集
	ResultMaxChars   int      `yaml:"result_max_chars"`  // 工具结果最大字符数 (默认 50000)
	BrowserPath      string   `yaml:"browser_path"`      // 浏览器可执行文件路径 (空 = 自动)
	WebSearchBackend string   `yaml:"web_search_backend"` // 网页搜索后端: exa / firecrawl / parallel
}

// ───────────────────────────── 记忆配置 ─────────────────────────────

// MemoryConfig 定义记忆系统的配置
type MemoryConfig struct {
	MemoryMaxChars int  `yaml:"memory_max_chars"` // MEMORY.md 最大字符数 (默认 2200)
	UserMaxChars   int  `yaml:"user_max_chars"`   // USER.md 最大字符数 (默认 1375)
	ExternalProvider string `yaml:"external_provider"` // 外部记忆提供者 (插件名)
}

// ───────────────────────────── 技能配置 ─────────────────────────────

// SkillsConfig 定义技能系统的配置
type SkillsConfig struct {
	Disabled []string `yaml:"disabled"`    // 禁用的技能列表
	ExternalDirs []string `yaml:"external_dirs"` // 外部技能目录
}

// ───────────────────────────── Cron 配置 ─────────────────────────────

// CronConfig 定义定时任务的配置
type CronConfig struct {
	Enabled          bool `yaml:"enabled"`           // 是否启用 cron 调度
	MaxParallelJobs  int  `yaml:"max_parallel_jobs"` // 最大并行任务数 (默认 3)
	TickIntervalSecs int  `yaml:"tick_interval_secs"` // 检测间隔 (默认 60)
}

// ───────────────────────────── 日志配置 ─────────────────────────────

// LoggingConfig 定义日志系统的配置
type LoggingConfig struct {
	Level  string `yaml:"level"`  // 日志级别: debug / info / warn / error
	Format string `yaml:"format"` // 日志格式: json / text
	Dir    string `yaml:"dir"`    // 日志目录 (默认 ~/.nexus/logs)
}

// ───────────────────────────── 审批配置 ─────────────────────────────

// ApprovalConfig 定义命令审批的行为
type ApprovalConfig struct {
	Mode        string   `yaml:"mode"`         // 审批模式: off / smart / always
	CronMode    string   `yaml:"cron_mode"`    // Cron 作业审批模式
	Allowlist   []string `yaml:"allowlist"`    // 永久允许的命令模式
	Blocklist   []string `yaml:"blocklist"`    // 永久禁止的命令模式
}

// ───────────────────────────── 沙箱配置 ─────────────────────────────

// SandboxConfig 定义终端沙箱的配置
type SandboxConfig struct {
	Backend       string `yaml:"backend"`        // 后端: local / docker / ssh / modal / daytona
	DefaultShell  string `yaml:"default_shell"`  // 默认 Shell (空 = /bin/bash 或 cmd.exe)
	DockerImage   string `yaml:"docker_image"`   // Docker 镜像名
	SSHHost       string `yaml:"ssh_host"`       // SSH 主机地址
	SSHUser       string `yaml:"ssh_user"`       // SSH 用户名
	TimeoutSecs   int    `yaml:"timeout_secs"`   // 命令超时 (默认 120)
}

// ───────────────────────────── MCP 配置 ─────────────────────────────

// MCPConfig 定义 MCP 协议的配置
type MCPConfig struct {
	Enabled bool                   `yaml:"enabled"` // 是否启用 MCP
	Servers map[string]MCPServer   `yaml:"servers"` // 已配置的 MCP 服务器
}

// MCPServer 定义单个 MCP 服务器的配置
type MCPServer struct {
	Command string   `yaml:"command"` // 启动命令
	Args    []string `yaml:"args"`    // 命令参数
	Env     []string `yaml:"env"`     // 环境变量
}

// ───────────────────────────── 凭证配置 ─────────────────────────────

// CredentialConfig 定义凭证管理配置
type CredentialConfig struct {
	Selection   string `yaml:"selection"`    // 选择策略: fill_first / round_robin / random / least_used
	FallbackKey string `yaml:"fallback_key"` // 备选 API Key 环境变量名
}

// ───────────────────────────── 插件配置 ─────────────────────────────

// PluginsConfig 定义插件系统的配置
type PluginsConfig struct {
	Enabled bool     `yaml:"enabled"` // 是否启用插件系统
	Dirs    []string `yaml:"dirs"`    // 插件目录列表
}

// ───────────────────────────── 洞察配置 ─────────────────────────────

// InsightsConfig 定义使用洞察的配置
type InsightsConfig struct {
	Enabled bool `yaml:"enabled"` // 是否启用使用洞察
}

// ───────────────────────────── 轨迹配置 ─────────────────────────────

// TrajectoryConfig 定义轨迹记录的配置
type TrajectoryConfig struct {
	Enabled bool   `yaml:"enabled"` // 是否保存轨迹
	Dir     string `yaml:"dir"`     // 轨迹保存目录
	Format  string `yaml:"format"`  // 轨迹格式: sharegpt / openai
}

// ───────────────────────────── 脱敏配置 ─────────────────────────────

// RedactConfig 定义密钥脱敏的配置
type RedactConfig struct {
	Enabled  bool     `yaml:"enabled"`  // 是否启用脱敏
	Patterns []string `yaml:"patterns"` // 额外的自定义脱敏模式
}

// ───────────────────────────── 批处理配置 ─────────────────────────────

// BatchConfig 定义批处理运行器的配置
type BatchConfig struct {
	MaxWorkers          int `yaml:"max_workers"`           // 最大并行 worker 数
	CheckpointInterval  int `yaml:"checkpoint_interval"`   // 检查点间隔 (秒)
}

// ───────────────────────────── URL 安全配置 ─────────────────────────────

// URLSafetyConfig 定义 URL 安全检查的配置
type URLSafetyConfig struct {
	AllowPrivateURLs bool     `yaml:"allow_private_urls"` // 是否允许访问私有 IP URL
	BlockedIPs       []string `yaml:"blocked_ips"`        // 额外屏蔽的 IP 列表
}

// ───────────────────────────── 工具输出配置 ─────────────────────────────

// ToolOutputConfig 定义工具输出限制
type ToolOutputConfig struct {
	MaxBytes      int `yaml:"max_bytes"`       // 单次输出最大字节数 (默认 50000)
	MaxLines      int `yaml:"max_lines"`       // 单次输出最大行数 (默认 2000)
	MaxLineLength int `yaml:"max_line_length"` // 单行最大长度 (默认 2000)
}

// ───────────────────────────── Shell Hook 配置 ─────────────────────────────

// ShellHooksConfig 定义 Shell Hook 系统的配置
type ShellHooksConfig struct {
	Enabled     bool `yaml:"enabled"`      // 是否启用 Shell Hook
	AcceptHooks bool `yaml:"accept_hooks"` // 自动接受 hook 首次使用同意
}

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
	for k, p := range cfg.Providers {
		p.APIKey = expandEnvString(p.APIKey)
		p.BaseURL = expandEnvString(p.BaseURL)
		p.OAuthURL = expandEnvString(p.OAuthURL)
		cfg.Providers[k] = p
	}
	for k := range cfg.Gateway.Platforms {
		cfg.Gateway.Platforms[k].Token = expandEnvString(cfg.Gateway.Platforms[k].Token)
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
