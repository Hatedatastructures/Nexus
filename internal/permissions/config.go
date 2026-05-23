// config.go 定义权限配置加载。
// 支持从用户级 (~/.nexus/permissions.yaml) 和项目级 (.nexus/permissions.yaml) 加载策略。
// 两层配置合并时，项目级规则优先于用户级规则。

package permissions

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ───────────────────────────── 配置文件结构 ─────────────────────────────

// PermissionConfig 是权限配置文件的顶层结构。
// 对应 permissions.yaml 的 YAML 格式。
type PermissionConfig struct {
	// Version 配置文件版本号，用于未来兼容性。
	Version int `yaml:"version"`

	// Default 默认权限级别 (当无规则命中时)。
	Default string `yaml:"default,omitempty"`

	// Rules 自定义权限规则列表。
	Rules []RuleConfig `yaml:"rules,omitempty"`

	// Profiles 权限配置预设 (如 "safe", "dev", "unrestricted")。
	Profiles map[string]ProfileConfig `yaml:"profiles,omitempty"`
}

// RuleConfig 是单条规则的 YAML 配置格式。
type RuleConfig struct {
	// Tool 工具名 glob 模式。
	Tool string `yaml:"tool"`

	// Args 参数匹配模式列表。
	Args []string `yaml:"args,omitempty"`

	// Level 权限级别名称 (auto_allow, auto_deny, ask_once, ask_always, escalate)。
	Level string `yaml:"level"`

	// Reason 规则说明。
	Reason string `yaml:"reason,omitempty"`
}

// ProfileConfig 是权限预设配置。
type ProfileConfig struct {
	// Description 预设描述。
	Description string `yaml:"description,omitempty"`

	// Default 预设的默认级别。
	Default string `yaml:"default,omitempty"`

	// Rules 预设的规则列表。
	Rules []RuleConfig `yaml:"rules,omitempty"`
}

// ───────────────────────────── 配置加载 ─────────────────────────────

// LoadConfig 从指定路径加载权限配置文件。
// 如果路径为空，按默认搜索顺序查找:
//
//	1. 当前目录 .nexus/permissions.yaml
//	2. 用户主目录 ~/.nexus/permissions.yaml
func LoadConfig(path string) (*PermissionConfig, error) {
	if path == "" {
		path = findConfigFile()
	}

	if path == "" {
		return &PermissionConfig{Version: 1}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取权限配置 %s: %w", path, err)
	}

	var cfg PermissionConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析权限配置 %s: %w", path, err)
	}

	slog.Info("permission config loaded", "path", path, "rules", len(cfg.Rules))
	return &cfg, nil
}

// LoadMergedConfig 加载并合并用户级和项目级配置。
// 合并策略: 项目级规则插入到用户级规则之前 (更高优先级)。
// 项目级默认级别覆盖用户级默认级别。
func LoadMergedConfig(projectDir string) (*PermissionConfig, error) {
	// 加载用户级配置
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Debug("unable to get user home directory", "err", err)
	}
	userCfgPath := ""
	if home != "" {
		userCfgPath = filepath.Join(home, ".nexus", "permissions.yaml")
	}
	userCfg, err := LoadConfig(userCfgPath)
	if err != nil {
		slog.Warn("user-level permission config load failed, ignoring", "path", userCfgPath, "err", err)
		userCfg = &PermissionConfig{Version: 1}
	}

	// 加载项目级配置
	projectCfgPath := ""
	if projectDir != "" {
		projectCfgPath = filepath.Join(projectDir, ".nexus", "permissions.yaml")
	}
	projectCfg, err := LoadConfig(projectCfgPath)
	if err != nil {
		slog.Warn("project-level permission config load failed, ignoring", "path", projectCfgPath, "err", err)
		projectCfg = &PermissionConfig{Version: 1}
	}

	// 合并: 项目级规则在前，用户级规则在后
	merged := &PermissionConfig{
		Version: 1,
		Default: userCfg.Default,
		Rules:   make([]RuleConfig, 0, len(projectCfg.Rules)+len(userCfg.Rules)),
		Profiles: make(map[string]ProfileConfig),
	}

	// 项目级默认级别覆盖用户级
	if projectCfg.Default != "" {
		merged.Default = projectCfg.Default
	}

	// 项目级规则优先
	merged.Rules = append(merged.Rules, projectCfg.Rules...)
	merged.Rules = append(merged.Rules, userCfg.Rules...)

	// 合并预设 (项目级覆盖用户级同名预设)
	for name, profile := range userCfg.Profiles {
		merged.Profiles[name] = profile
	}
	for name, profile := range projectCfg.Profiles {
		merged.Profiles[name] = profile
	}

	return merged, nil
}

// ───────────────────────────── 配置转策略 ─────────────────────────────

// BuildPolicy 从配置构建策略实例。
// 将 YAML 配置中的规则转换为运行时策略对象。
func BuildPolicy(cfg *PermissionConfig) (*Policy, error) {
	policy := &Policy{
		Name:        "custom",
		Description: "从配置文件加载的自定义策略",
		Rules:       make([]Rule, 0, len(cfg.Rules)),
	}

	// 解析默认级别
	if cfg.Default != "" {
		level, err := ParseLevel(cfg.Default)
		if err != nil {
			return nil, fmt.Errorf("配置默认级别无效: %w", err)
		}
		policy.Default = level
	} else {
		policy.Default = LevelAskAlways
	}
	policy.DefaultReason = "未匹配任何规则，使用配置默认策略"

	// 解析规则
	for i, rc := range cfg.Rules {
		level, err := ParseLevel(rc.Level)
		if err != nil {
			return nil, fmt.Errorf("规则 #%d 级别无效: %w", i, err)
		}
		rule := Rule{
			ToolPattern: rc.Tool,
			ArgPatterns: rc.Args,
			Level:       level,
			Reason:      rc.Reason,
		}
		// 验证 glob 模式合法性
		if _, err := filepath.Match(rule.ToolPattern, "test"); err != nil {
			return nil, fmt.Errorf("规则 #%d 工具模式 %q 无效: %w", i, rule.ToolPattern, err)
		}
		policy.Rules = append(policy.Rules, rule)
	}

	return policy, nil
}

// BuildPolicyWithProfile 从配置中指定预设构建策略。
// 如果预设不存在，使用默认配置。
func BuildPolicyWithProfile(cfg *PermissionConfig, profileName string) (*Policy, error) {
	if profileName == "" {
		return BuildPolicy(cfg)
	}

	profile, ok := cfg.Profiles[profileName]
	if !ok {
		return nil, fmt.Errorf("权限预设 %q 不存在", profileName)
	}

	// 用预设配置覆盖顶层配置
	profileCfg := &PermissionConfig{
		Version: cfg.Version,
		Default: profile.Default,
		Rules:   profile.Rules,
	}

	// 预设没有默认值时继承顶层
	if profileCfg.Default == "" {
		profileCfg.Default = cfg.Default
	}

	return BuildPolicy(profileCfg)
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// findConfigFile 按默认搜索顺序查找权限配置文件。
func findConfigFile() string {
	// 1. 当前目录 .nexus/permissions.yaml
	if _, err := os.Stat(".nexus/permissions.yaml"); err == nil {
		return ".nexus/permissions.yaml"
	}

	// 2. 用户主目录 ~/.nexus/permissions.yaml
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".nexus", "permissions.yaml")
	if _, err := os.Stat(path); err == nil {
		return path
	}

	return ""
}
