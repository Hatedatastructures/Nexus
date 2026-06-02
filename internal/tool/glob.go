// Package tool 提供 Glob 文件模式匹配工具。
// 支持 *, ?, ** 递归匹配和 {a,b} 花括号展开。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ───────────────────────────── Glob 文件匹配工具 ─────────────────────────────

// GlobTool 实现文件名模式匹配搜索。
type GlobTool struct{}

// Name 返回工具名称。
func (t *GlobTool) Name() string { return "glob" }

// Description 返回工具描述。
func (t *GlobTool) Description() string {
	return "按文件名模式匹配搜索文件。支持 *, ?, **, {a,b} 语法。按修改时间排序。"
}

// Toolset 返回工具所属工具集。
func (t *GlobTool) Toolset() string { return "full_stack" }

// Emoji 返回工具图标。
func (t *GlobTool) Emoji() string { return "search" }

// IsAvailable Glob 工具始终可用。
func (t *GlobTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *GlobTool) MaxResultChars() int { return 10000 }

// Schema 返回工具的 JSON Schema。
func (t *GlobTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "glob",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "glob 模式，如 **/*.go, src/**/*.{ts,tsx}",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "搜索的根目录，默认为当前工作目录",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

// Execute 执行 Glob 文件模式匹配。
// 展开花括号 → 处理 ** 递归 → 收集匹配 → 按修改时间排序 → 返回结果。
func (t *GlobTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return ToolError("参数 pattern 是必填项且必须为字符串"), nil
	}

	searchRoot := "."
	if p, ok := args["path"].(string); ok && p != "" {
		searchRoot = p
	}

	// 路径安全检查
	safeRoot, secErr := checkPathSecurity(searchRoot, true)
	if secErr != nil {
		return ToolError(fmt.Sprintf("安全限制: %v", secErr)), nil
	}
	searchRoot = safeRoot

	// 展开花括号 {a,b} → 多个模式
	patterns := expandBraces(pattern)

	// 收集所有匹配的文件路径 (去重)
	seen := make(map[string]bool)
	var matches []string

	for _, pat := range patterns {
		found, err := globRecursive(searchRoot, pat)
		if err != nil {
			continue
		}
		for _, f := range found {
			if !seen[f] {
				seen[f] = true
				matches = append(matches, f)
			}
		}
	}

	if len(matches) == 0 {
		result, err := json.Marshal(map[string]any{
			"output":  fmt.Sprintf("在 %s 中未找到匹配 '%s' 的文件", searchRoot, pattern),
			"pattern": pattern,
			"path":    searchRoot,
			"files":   []string{},
		})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
		return string(result), nil
	}

	// 按修改时间排序 (最近的排前面)
	sortByModTime(matches)

	// 限制结果数量
	limit := 100
	truncated := false
	if len(matches) > limit {
		matches = matches[:limit]
		truncated = true
	}

	// 转换为相对路径
	relPaths := make([]string, len(matches))
	for i, absPath := range matches {
		rel, err := filepath.Rel(searchRoot, absPath)
		if err != nil {
			relPaths[i] = absPath
		} else {
			relPaths[i] = rel
		}
	}

	output := fmt.Sprintf("在 %s 中找到 %d 个匹配 '%s' 的文件", searchRoot, len(relPaths), pattern)
	if truncated {
		output += " (结果已截断，仅显示前 100 个)"
	}

	result, err := json.Marshal(map[string]any{
		"output":    output,
		"pattern":   pattern,
		"path":      searchRoot,
		"files":     relPaths,
		"total":     len(relPaths),
		"truncated": truncated,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}

	return string(result), nil
}

// ───────────────────────────── 花括号展开 ─────────────────────────────

// expandBraces 将模式中的 {a,b,c} 语法展开为多个独立模式。
// 例如: "src/**/*.{ts,tsx}" → ["src/**/*.ts", "src/**/*.tsx"]
// 支持嵌套花括号，每次展开最内层。
func expandBraces(pattern string) []string {
	// 没有花括号，直接返回
	if !strings.Contains(pattern, "{") {
		return []string{pattern}
	}

	var result []string
	result = append(result, pattern)

	// 反复展开直到没有花括号为止
	changed := true
	for changed {
		changed = false
		var next []string
		for _, p := range result {
			expanded := expandOneBrace(p)
			if len(expanded) > 1 {
				changed = true
			}
			next = append(next, expanded...)
		}
		result = next
	}

	return result
}

// expandOneBrace 展开模式中最内层的一个花括号组。
func expandOneBrace(pattern string) []string {
	// 找到最内层的花括号: 最后出现的 { 和匹配的 }
	lastOpen := -1
	depth := 0
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '{' {
			if depth == 0 {
				lastOpen = i
			}
			depth++
		} else if pattern[i] == '}' {
			depth--
			if depth == 0 && lastOpen >= 0 {
				// 找到匹配的花括号对
				inner := pattern[lastOpen+1 : i]
				prefix := pattern[:lastOpen]
				suffix := pattern[i+1:]

				options := strings.Split(inner, ",")
				var result []string
				for _, opt := range options {
					result = append(result, prefix+opt+suffix)
				}
				return result
			}
		}
	}

	// 没有匹配的花括号，返回原样
	return []string{pattern}
}

// ───────────────────────────── 递归 Glob 匹配 ─────────────────────────────

// globRecursive 在 root 目录下按 pattern 进行递归文件匹配。
// 手动展开 ** 为目录递归，其余交给 filepath.Match。
func globRecursive(root, pattern string) ([]string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	// 分离路径前缀: 如果 pattern 以 path/ 开头，将路径部分合并到 root
	parts := strings.SplitN(pattern, "**", 2)
	if len(parts) == 2 {
		// pattern 包含 **
		prefix := strings.TrimRight(parts[0], "/")
		suffix := strings.TrimLeft(parts[1], "/")

		searchDir := absRoot
		if prefix != "" {
			searchDir = filepath.Join(absRoot, filepath.ToSlash(prefix))
		}

		info, err := os.Stat(searchDir)
		if err != nil || !info.IsDir() {
			return nil, nil
		}

		var matches []string

		if suffix == "" {
			// pattern 是 ** (匹配所有文件)
			err = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					slog.Warn("glob: skip inaccessible path", "path", path, "error", err)
					return nil
				}
				if !info.IsDir() {
					matches = append(matches, path)
				}
				return nil
			})
		} else {
			// pattern 是 **/<suffix>
			suffixPattern := suffix
			err = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					slog.Warn("glob: skip inaccessible path", "path", path, "error", err)
					return nil
				}
				if info.IsDir() {
					return nil
				}

				// 获取从 searchDir 开始的相对路径的文件名部分进行匹配
				rel, relErr := filepath.Rel(searchDir, path)
				if relErr != nil {
					return nil
				}
				rel = filepath.ToSlash(rel)

				// 使用 filepath.Match 对 suffix 模式匹配相对路径
				matched, matchErr := matchGlob(suffixPattern, rel)
				if matchErr == nil && matched {
					matches = append(matches, path)
				}
				return nil
			})
		}

		if err != nil {
			return nil, err
		}
		return matches, nil
	}

	// 不包含 ** 的普通模式，直接使用 filepath.Glob
	fullPattern := filepath.Join(absRoot, pattern)
	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		return nil, err
	}

	// 过滤掉目录，只保留文件
	var files []string
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			files = append(files, m)
		}
	}
	return files, nil
}

// matchGlob 使用 filepath.Match 风格匹配，但支持 ** (任意路径段)。
// 对于不含 ** 的模式，直接使用 filepath.Match。
// 对于含 ** 的模式，拆分后逐段匹配。
func matchGlob(pattern, path string) (bool, error) {
	// 简单情况: 不含 **，直接用 filepath.Match
	if !strings.Contains(pattern, "**") {
		return filepath.Match(pattern, path)
	}

	// 如果 pattern 就是 ** 或 **/* 之类的，匹配所有文件
	if pattern == "**" || pattern == "**/*" {
		return true, nil
	}

	// 对于 **/*.ext 模式: 匹配任意深度的 .ext 文件
	parts := strings.SplitN(pattern, "**/", 2)
	if len(parts) == 2 {
		suffix := parts[1]
		// 匹配路径中任意包含 suffix 模式的尾部
		// 对 path 的每个可能的后缀尝试匹配
		pathParts := strings.Split(path, "/")
		for i := 0; i < len(pathParts); i++ {
			tail := strings.Join(pathParts[i:], "/")
			matched, err := filepath.Match(suffix, tail)
			if err == nil && matched {
				return true, nil
			}
		}
		return false, nil
	}

	// 回退: 对完整路径使用 filepath.Match
	return filepath.Match(pattern, path)
}

// ───────────────────────────── 排序辅助 ─────────────────────────────

// fileModEntry 用于按修改时间排序。
type fileModEntry struct {
	path    string
	modTime int64
}

// sortByModTime 将文件路径按修改时间降序排序 (最近修改的排最前面)。
func sortByModTime(paths []string) {
	entries := make([]fileModEntry, len(paths))
	for i, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			entries[i] = fileModEntry{path: p, modTime: 0}
			continue
		}
		entries[i] = fileModEntry{path: p, modTime: info.ModTime().UnixNano()}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime > entries[j].modTime
	})

	for i, e := range entries {
		paths[i] = e.path
	}
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&GlobTool{})
}
