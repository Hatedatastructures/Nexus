// Package tool 提供工具集定义和组合解析。
// 工具集是工具的命名集合，支持递归包含和循环检测。
package tool

import (
	"fmt"
)

// ───────────────────────────── 工具集定义 ─────────────────────────────

// ToolsetDefinition 定义工具集的组成。
// 工具集可以包含直接的工具名称，也可以通过 Includes 引用其他工具集。
type ToolsetDefinition struct {
	Description string   // 工具集描述
	Tools       []string // 直接包含的工具名称
	Includes    []string // 包含的其他工具集名称
}

// ───────────────────────────── 预定义工具集 ─────────────────────────────

// 预定义默认工具集配置。
// 四大内置工具集覆盖从基础到全栈的各级别能力。
var DefaultToolsets = map[string]*ToolsetDefinition{
	"core": {
		Description: "基础工具集：终端、文件读写、网页搜索与提取",
		Tools:       []string{"terminal", "file_read", "file_write", "web_search", "web_extract"},
	},
	"developer": {
		Description: "开发者工具集：核心工具 + 代码执行、文件编辑、Git 操作",
		Includes:    []string{"core"},
		Tools:       []string{"code_execute", "file_edit", "git"},
	},
	"research": {
		Description: "研究工具集：核心工具 + 浏览器自动化、网页爬取、会话搜索",
		Includes:    []string{"core"},
		Tools:       []string{"browser_navigate", "browser_click", "browser_type", "browser_screenshot", "web_crawl", "session_search"},
	},
	"full_stack": {
		Description: "全栈工具集：所有可用工具的组合",
		Includes:    []string{"developer", "research"},
		Tools:       []string{"delegate_task", "memory", "cron"},
	},
}

// ───────────────────────────── 工具集解析 ─────────────────────────────

// ResolveToolset 递归解析工具集，展开所有 Included 的工具集。
// defs 是工具集定义映射，visited 用于循环依赖检测。
// 返回展平后的工具名称列表 (去重，保持声明顺序)。
func ResolveToolset(name string, defs map[string]*ToolsetDefinition, visited map[string]bool) ([]string, error) {
	if visited == nil {
		visited = make(map[string]bool)
	}

	// 循环依赖检测
	if visited[name] {
		return nil, fmt.Errorf("工具集循环引用: %s", name)
	}
	visited[name] = true

	def, ok := defs[name]
	if !ok {
		return nil, fmt.Errorf("未知工具集: %s", name)
	}

	// 使用 map 去重
	seen := make(map[string]bool)
	var result []string

	// 先递归展开 Includes
	for _, included := range def.Includes {
		subTools, err := ResolveToolset(included, defs, visited)
		if err != nil {
			return nil, err
		}
		for _, t := range subTools {
			if !seen[t] {
				seen[t] = true
				result = append(result, t)
			}
		}
	}

	// 再添加直接工具
	for _, t := range def.Tools {
		if !seen[t] {
			seen[t] = true
			result = append(result, t)
		}
	}

	return result, nil
}

// ResolveDefaultToolset 解析预定义的默认工具集。
// 便捷函数，使用 DefaultToolsets 配置。
func ResolveDefaultToolset(name string) ([]string, error) {
	return ResolveToolset(name, DefaultToolsets, nil)
}

// GetAllToolsetNames 返回所有预定义工具集名称。
func GetAllToolsetNames() []string {
	names := make([]string, 0, len(DefaultToolsets))
	for name := range DefaultToolsets {
		names = append(names, name)
	}
	return names
}
