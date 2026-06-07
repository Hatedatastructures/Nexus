// Package plugin 提供插件清单 (manifest) 的 YAML 解析。
// 清单文件 (plugin.yaml) 声明插件的元数据、依赖和能力。
package plugin

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	pkgerrors "nexus-agent/internal/errors"
)

// ───────────────────────────── 插件清单 ─────────────────────────────

// Manifest 表示插件的清单文件 (plugin.yaml)。
// 描述插件的元数据、依赖关系和能力声明。
type Manifest struct {
	// ── 基本信息 ──
	Name        string `yaml:"name"`                  // 插件名称 (必填，唯一标识)
	Version     string `yaml:"version"`               // 语义版本号 (必填)
	Description string `yaml:"description,omitempty"` // 插件描述
	Author      string `yaml:"author,omitempty"`      // 作者信息
	License     string `yaml:"license,omitempty"`     // SPDX 许可证标识

	// ── 分类与兼容性 ──
	Kind      string   `yaml:"kind,omitempty"`      // 插件类型: tool / hook / memory / composite
	Platforms []string `yaml:"platforms,omitempty"` // 兼容平台 (空 = 全平台)

	// ── 能力声明 ──
	ProvidesTools []string `yaml:"provides_tools,omitempty"` // 提供的工具名列表
	Hooks         []string `yaml:"hooks,omitempty"`          // 注册的钩子事件列表

	// ── 依赖声明 ──
	ExternalDeps []string `yaml:"external_deps,omitempty"` // 外部依赖 (二进制/包名)
	RequiresEnv  []string `yaml:"requires_env,omitempty"`  // 必需的环境变量名

	// ── 入口配置 ──
	Entrypoint string         `yaml:"entrypoint,omitempty"` // 入口文件 (Go 插件 .so 路径)
	Config     map[string]any `yaml:"config,omitempty"`     // 默认配置 (可被运行时覆盖)
}

// ───────────────────────────── 清单解析 ─────────────────────────────

// ParseManifest 从指定路径解析插件清单文件。
// 返回解析后的 Manifest 结构体。
//
// 验证规则:
//   - name 和 version 为必填字段
//   - name 长度不超过 64 字符
//   - version 必须为非空字符串
//   - kind 如果提供，必须是已知类型
func ParseManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.FileIO, fmt.Sprintf("读取清单文件 %s", path), err)
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ConfigInvalid, fmt.Sprintf("解析清单文件 %s", path), err)
	}

	// 验证必填字段
	if m.Name == "" {
		return nil, pkgerrors.New(pkgerrors.ConfigInvalid, "清单缺少必填字段: name")
	}
	if len(m.Name) > 64 {
		return nil, pkgerrors.New(pkgerrors.ConfigInvalid, fmt.Sprintf("插件名称过长 (最多 64 字符): %s", m.Name))
	}
	if m.Version == "" {
		return nil, pkgerrors.New(pkgerrors.ConfigInvalid, "清单缺少必填字段: version")
	}

	// 验证 kind 类型
	if m.Kind != "" && !isValidPluginKind(m.Kind) {
		return nil, pkgerrors.New(pkgerrors.ConfigInvalid, fmt.Sprintf("未知的插件类型: %s (有效值: tool, hook, memory, composite)", m.Kind))
	}

	return &m, nil
}

// ───────────────────────────── 插件类型 ─────────────────────────────

// PluginKind 表示插件的类型分类。
type PluginKind string

const (
	KindTool      PluginKind = "tool"      // 工具插件
	KindHook      PluginKind = "hook"      // 钩子插件
	KindMemory    PluginKind = "memory"    // 记忆插件
	KindComposite PluginKind = "composite" // 复合插件
)

// knownPluginKinds 已知的插件类型集合。
var knownPluginKinds = map[string]PluginKind{
	"tool":      KindTool,
	"hook":      KindHook,
	"memory":    KindMemory,
	"composite": KindComposite,
}

// isValidPluginKind 检查插件类型是否有效。
func isValidPluginKind(kind string) bool {
	_, ok := knownPluginKinds[kind]
	return ok
}

// ───────────────────────────── 清单验证 ─────────────────────────────

// ValidateManifest 验证清单的完整性和一致性。
// 检查必填字段、格式约束和声明一致性。
func ValidateManifest(m *Manifest) error {
	if m == nil {
		return pkgerrors.New(pkgerrors.ConfigInvalid, "清单为 nil")
	}

	if m.Name == "" {
		return pkgerrors.New(pkgerrors.ConfigInvalid, "清单缺少必填字段: name")
	}
	if m.Version == "" {
		return pkgerrors.New(pkgerrors.ConfigInvalid, "清单缺少必填字段: version")
	}

	// 验证 name 格式 (只允许字母、数字、下划线、连字符)
	for _, c := range m.Name {
		if !isNameChar(c) {
			return pkgerrors.New(pkgerrors.ConfigInvalid, fmt.Sprintf("插件名称包含非法字符 '%c' (只允许字母、数字、下划线、连字符)", c))
		}
	}

	return nil
}

// isNameChar 检查字符是否为合法的插件名称字符。
func isNameChar(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_' || c == '-'
}
