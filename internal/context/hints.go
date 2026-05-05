// Package context 提供子目录上下文提示发现功能。
// 当工具在特定目录执行时，自动发现并注入该目录及其祖先目录中的
// AGENTS.md / CLAUDE.md / .cursorrules 等上下文文件。
package context

import (
	"os"
	"path/filepath"
	"strings"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	maxHintAncestors = 5    // 最大向上遍历层数
	maxHintFileChars = 8192 // 单个提示文件最大字符数
)

// hintFileNames 要查找的上下文文件名
var hintFileNames = []string{
	"AGENTS.md",
	"CLAUDE.md",
	".cursorrules",
	".clinerules",
}

// ───────────────────────────── 数据结构 ─────────────────────────────

// HintTracker 追踪已访问目录和发现的提示。
type HintTracker struct {
	visited map[string]bool
	hints   map[string]string // dir -> hint content
}

// NewHintTracker 创建一个新的提示追踪器。
func NewHintTracker() *HintTracker {
	return &HintTracker{
		visited: make(map[string]bool),
		hints:   make(map[string]string),
	}
}

// ───────────────────────────── 核心函数 ─────────────────────────────

// DiscoverHints 发现指定目录及其祖先目录中的上下文文件。
// 返回发现的所有提示内容（去重，按目录深度从浅到深排序）。
func (ht *HintTracker) DiscoverHints(dir string) []string {
	if dir == "" {
		return nil
	}

	// 解析为绝对路径
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}

	var results []string
	current := absDir

	for i := 0; i < maxHintAncestors; i++ {
		if ht.visited[current] {
			break
		}
		ht.visited[current] = true

		// 在当前目录查找提示文件
		for _, name := range hintFileNames {
			path := filepath.Join(current, name)
			content, err := readHintFile(path)
			if err != nil || content == "" {
				continue
			}

			// 使用相对路径作为 key 避免重复
			key := current + "/" + name
			if _, exists := ht.hints[key]; !exists {
				ht.hints[key] = content
				results = append(results, content)
			}
		}

		// 向上遍历
		parent := filepath.Dir(current)
		if parent == current {
			break // 到达根目录
		}
		current = parent
	}

	return results
}

// DiscoverHintsFromPaths 从工具参数中提取路径并发现提示。
func (ht *HintTracker) DiscoverHintsFromPaths(paths []string) []string {
	var allHints []string
	seen := make(map[string]bool)

	for _, p := range paths {
		absPath, err := filepath.Abs(p)
		if err != nil {
			continue
		}

		// 如果是文件，取其目录
		dir := absPath
		info, err := os.Stat(absPath)
		if err == nil && !info.IsDir() {
			dir = filepath.Dir(absPath)
		}

		hints := ht.DiscoverHints(dir)
		for _, h := range hints {
			if !seen[h] {
				seen[h] = true
				allHints = append(allHints, h)
			}
		}
	}

	return allHints
}

// ExtractPathsFromArgs 从工具参数中提取文件/目录路径。
func ExtractPathsFromArgs(args map[string]any) []string {
	var paths []string

	// 常见路径参数名
	pathKeys := []string{"path", "file", "directory", "dir", "workdir", "cwd", "filename", "target"}

	for _, key := range pathKeys {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				paths = append(paths, s)
			}
		}
	}

	// 从 command 参数中提取路径（简单启发式）
	if cmd, ok := args["command"]; ok {
		if s, ok := cmd.(string); ok {
			paths = append(paths, extractPathsFromCommand(s)...)
		}
	}

	return paths
}

// AllHints 返回所有已发现的提示。
func (ht *HintTracker) AllHints() []string {
	results := make([]string, 0, len(ht.hints))
	for _, content := range ht.hints {
		results = append(results, content)
	}
	return results
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// readHintFile 读取提示文件，限制大小。
func readHintFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	content := string(data)
	if len(content) > maxHintFileChars {
		content = content[:maxHintFileChars]
	}

	return strings.TrimSpace(content), nil
}

// extractPathsFromCommand 从 shell 命令中提取可能的路径。
func extractPathsFromCommand(cmd string) []string {
	var paths []string
	parts := strings.Fields(cmd)

	for _, part := range parts {
		// 跳过标志
		if strings.HasPrefix(part, "-") {
			continue
		}
		// 跳过管道和重定向
		if part == "|" || part == ">" || part == ">>" || part == "<" {
			continue
		}
		// 看起来像路径的参数
		if strings.Contains(part, "/") || strings.Contains(part, "\\") || strings.Contains(part, ".") {
			// 排除明显的非路径
			if len(part) > 2 && !strings.HasPrefix(part, "http") {
				paths = append(paths, part)
			}
		}
	}

	return paths
}
