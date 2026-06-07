package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ───────────────────────────── 文件搜索工具 ─────────────────────────────

// FileSearchTool 实现文件搜索功能 (基于内容模式匹配)。
type FileSearchTool struct{}

// Name 返回工具名称。
func (t *FileSearchTool) Name() string { return "file_search" }

// Description 返回工具描述。
func (t *FileSearchTool) Description() string {
	return "在文件中搜索匹配指定模式的内容。支持正则表达式和文件过滤。"
}

// Toolset 返回工具所属工具集。
func (t *FileSearchTool) Toolset() string { return "file" }

// Emoji 返回工具图标。
func (t *FileSearchTool) Emoji() string { return "🔍" }

// IsAvailable 文件搜索始终可用。
func (t *FileSearchTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *FileSearchTool) MaxResultChars() int { return 50000 }

// Schema 返回工具的 JSON Schema。
func (t *FileSearchTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "file_search",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "搜索模式 (支持子字符串匹配，将自动转义正则特殊字符)",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "搜索目录路径，默认为当前工作目录",
				},
				"glob": map[string]any{
					"type":        "string",
					"description": "文件过滤 glob 模式 (如 *.go, **/*.md)",
				},
				"regex": map[string]any{
					"type":        "boolean",
					"description": "是否使用正则表达式匹配，默认 true",
				},
				"case_sensitive": map[string]any{
					"type":        "boolean",
					"description": "是否区分大小写，默认 false",
				},
				"context_lines": map[string]any{
					"type":        "integer",
					"description": "匹配行上下文行数，默认 0",
				},
				"output_mode": map[string]any{
					"type":        "string",
					"description": "输出模式，默认 content",
					"enum":        []string{"content", "files_with_matches"},
				},
				"head_limit": map[string]any{
					"type":        "integer",
					"description": "最大结果数，默认 250",
				},
				"type": map[string]any{
					"type":        "string",
					"description": "文件扩展名过滤，如 go, py, js",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

// Execute 执行文件搜索。
// 在指定目录下搜索匹配模式的文件内容。
// 支持正则/子串匹配、大小写选项、上下文行、输出模式等。
func (t *FileSearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return ToolError("参数 pattern 是必填项且必须为字符串"), nil
	}

	searchPath := "."
	if p, ok := args["path"].(string); ok && p != "" {
		searchPath = p
	}
	globPattern := "*"
	if g, ok := args["glob"].(string); ok && g != "" {
		globPattern = g
	}

	// 新参数提取
	useRegex := true
	if v, ok := args["regex"].(bool); ok {
		useRegex = v
	}
	caseSensitive := false
	if v, ok := args["case_sensitive"].(bool); ok {
		caseSensitive = v
	}
	contextLines := 0
	if v, ok := args["context_lines"].(float64); ok && v >= 0 {
		contextLines = int(v)
	}
	outputMode := "content"
	if v, ok := args["output_mode"].(string); ok && (v == "content" || v == "files_with_matches") {
		outputMode = v
	}
	headLimit := 250
	if v, ok := args["head_limit"].(float64); ok && v > 0 {
		headLimit = int(v)
	}
	typeFilter := ""
	if v, ok := args["type"].(string); ok && v != "" {
		typeFilter = v
	}

	// 路径安全检查: 遍历组件拒绝 + 净化 + 目录边界验证
	safeSearchPath, secErr := checkPathSecurity(searchPath, true)
	if secErr != nil {
		slog.Warn("file search blocked (path security)", "path", searchPath, "err", secErr)
		return ToolError(fmt.Sprintf("安全限制: %v", secErr)), nil
	}
	searchPath = safeSearchPath

	// 安全敏感路径检查
	if isPathSensitive(searchPath) {
		slog.Warn("file search blocked (sensitive path)", "path", searchPath)
		return ToolError(fmt.Sprintf("安全限制: 不允许搜索敏感路径 %s", searchPath)), nil
	}

	// 构建匹配函数
	var re *regexp.Regexp
	var matchFunc func(string) bool

	if useRegex {
		flags := ""
		if !caseSensitive {
			flags = "(?i)"
		}
		compiled, err := regexp.Compile(flags + pattern)
		if err != nil {
			return ToolError(fmt.Sprintf("正则表达式编译失败: %v", err)), nil
		}
		re = compiled
		matchFunc = func(s string) bool { return re.MatchString(s) }
	} else {
		pat := pattern
		if !caseSensitive {
			pat = strings.ToLower(pat)
		}
		matchFunc = func(s string) bool {
			if caseSensitive {
				return strings.Contains(s, pat)
			}
			return strings.Contains(strings.ToLower(s), pat)
		}
	}

	// 辅助: 根据文件名判断是否通过 type 过滤
	typeExt := "." + typeFilter
	passTypeFilter := func(name string) bool {
		if typeFilter == "" {
			return true
		}
		return strings.EqualFold(filepath.Ext(name), typeExt)
	}

	// 收集匹配的文件
	var results []map[string]any

	err := filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			slog.Warn("file search: skip inaccessible path", "path", path, "error", err)
			return nil
		}
		if info.IsDir() {
			// 跳过敏感目录
			if isPathSensitive(path) {
				return filepath.SkipDir
			}
			// 跳过隐藏目录
			if strings.HasPrefix(info.Name(), ".") && path != searchPath {
				return filepath.SkipDir
			}
			return nil
		}

		// type 扩展名过滤
		if !passTypeFilter(info.Name()) {
			return nil
		}

		// glob 过滤
		if globPattern != "*" {
			matched, _ := filepath.Match(globPattern, info.Name())
			if !matched {
				return nil
			}
		}

		// 检查是否达到最大结果数
		if len(results) >= headLimit {
			return filepath.SkipAll
		}

		// 读取文件内容并搜索
		data, readErr := os.ReadFile(path)
		if readErr != nil || len(data) >= 1<<20 { // 跳过大于等于 1MB 的文件
			return nil
		}

		content := string(data)
		if !matchFunc(content) {
			return nil
		}

		// files_with_matches 模式只记录文件名
		if outputMode == "files_with_matches" {
			results = append(results, map[string]any{
				"file": path,
			})
			return nil
		}

		// content 模式: 查找匹配行（含上下文）
		var matchingLines []map[string]any
		lines := strings.Split(content, "\n")
		matchCount := 0
		maxMatchesPerFile := 50

		for i, line := range lines {
			if matchFunc(line) {
				// 确定上下文范围
				start := i - contextLines
				if start < 0 {
					start = 0
				}
				end := i + contextLines
				if end >= len(lines) {
					end = len(lines) - 1
				}

				// 构建含上下文的行集合
				var contextLinesResult []map[string]any
				for j := start; j <= end; j++ {
					contextLinesResult = append(contextLinesResult, map[string]any{
						"line_number": j + 1,
						"content":     lines[j],
						"is_match":    j == i,
					})
				}

				matchingLines = append(matchingLines, map[string]any{
					"line_number":    i + 1,
					"content":        lines[i],
					"context_before": contextLinesResult,
				})
				matchCount++
				if matchCount >= maxMatchesPerFile {
					break
				}
			}
		}

		results = append(results, map[string]any{
			"file":    path,
			"matches": matchCount,
			"lines":   matchingLines,
		})
		return nil
	})

	if err != nil {
		slog.Warn("file search traversal failed", "path", searchPath, "err", err)
	}

	if len(results) == 0 {
		result, err := json.Marshal(map[string]any{
			"output":  fmt.Sprintf("在 %s 中未找到匹配 '%s' 的文件", searchPath, pattern),
			"pattern": pattern,
			"path":    searchPath,
			"results": []any{},
		})
		if err != nil {
			return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
		}
		return string(result), nil
	}

	resultJSON, err := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("在 %s 中找到 %d 个匹配 '%s' 的文件", searchPath, len(results), pattern),
		"pattern": pattern,
		"path":    searchPath,
		"results": results,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(resultJSON), nil
}
