// Package tool 提供文件操作工具。
// 包含文件读取、写入、编辑和搜索功能。
// 所有文件操作都会进行敏感路径安全检查。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ───────────────────────────── 敏感路径列表 ─────────────────────────────

// sensitivePaths 包含不允许读写的敏感系统路径。
// 匹配方式: 完整路径匹配或前缀匹配。
var sensitivePaths = []string{
	// Unix 系统敏感文件
	"/etc/passwd",
	"/etc/shadow",
	"/etc/sudoers",
	"/etc/ssh/",
	// Nexus 配置和凭证
	".nexus/.env",
	".nexus/credentials",
	// SSH 密钥
	".ssh/id_rsa",
	".ssh/id_ed25519",
	".ssh/authorized_keys",
	// Windows 系统目录
	`C:\Windows\System32`,
	`C:\Windows\System`,
	// 通用敏感路径
	"/proc/",
	"/sys/",
}

// ───────────────────────────── 路径安全 ─────────────────────────────

// isPathSensitive 检查给定路径是否为敏感路径。
// 对路径进行规范化和大小写标准化后与敏感路径列表比对。
func isPathSensitive(path string) bool {
	// 规范化路径
	cleanPath := filepath.Clean(path)
	cleanLower := strings.ToLower(cleanPath)

	// 展开 HOME 目录
	homeDir, _ := os.UserHomeDir()
	if homeDir != "" {
		cleanLower = strings.ReplaceAll(cleanLower, strings.ToLower(homeDir), "~")
	}

	for _, sensitive := range sensitivePaths {
		sensitiveLower := strings.ToLower(sensitive)
		// 完整匹配
		if cleanLower == sensitiveLower {
			return true
		}
		// 前缀匹配 (目录路径)
		if strings.HasSuffix(sensitiveLower, string(os.PathSeparator)) &&
			strings.HasPrefix(cleanLower, sensitiveLower) {
			return true
		}
		// 带 home 匹配
		if strings.HasPrefix(sensitiveLower, "~") {
			rest := strings.TrimPrefix(sensitiveLower, "~")
			if strings.HasSuffix(cleanLower, rest) || strings.Contains(cleanLower, rest) {
				return true
			}
		}
	}
	return false
}

// ───────────────────────────── 文件读取工具 ─────────────────────────────

// FileReadTool 实现文件读取功能。
type FileReadTool struct{}

// Name 返回工具名称。
func (t *FileReadTool) Name() string { return "file_read" }

// Description 返回工具描述。
func (t *FileReadTool) Description() string {
	return "读取文件内容。支持指定起止行号以读取文件的部分内容。"
}

// Toolset 返回工具所属工具集。
func (t *FileReadTool) Toolset() string { return "file" }

// Emoji 返回工具图标。
func (t *FileReadTool) Emoji() string { return "📄" }

// IsAvailable 文件读取始终可用。
func (t *FileReadTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *FileReadTool) MaxResultChars() int { return 100000 }

// Schema 返回工具的 JSON Schema。
func (t *FileReadTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "file_read",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "要读取的文件路径 (绝对路径或相对路径)",
				},
				"start_line": map[string]any{
					"type":        "integer",
					"description": "起始行号 (从 1 开始)，默认从第一行开始",
				},
				"end_line": map[string]any{
					"type":        "integer",
					"description": "结束行号 (包含)，默认到文件末尾",
				},
			},
			"required": []string{"path"},
		},
	}
}

// Execute 执行文件读取。
// 检查敏感路径 → 读取文件 → 格式化输出。
func (t *FileReadTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ToolError("参数 path 是必填项且必须为字符串"), nil
	}

	// 安全敏感路径检查
	if isPathSensitive(path) {
		slog.Warn("文件读取被阻止 (敏感路径)", "path", path)
		return ToolError(fmt.Sprintf("安全限制: 不允许读取敏感路径 %s", path)), nil
	}

	// 如果是目录，列出目录内容
	info, err := os.Stat(path)
	if err != nil {
		slog.Warn("文件读取失败", "path", path, "err", err)
		return ToolError(fmt.Sprintf("读取失败: %v", err)), nil
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return ToolError(fmt.Sprintf("读取目录失败: %v", err)), nil
		}
		var buf strings.Builder
		buf.WriteString(fmt.Sprintf("目录: %s\n\n", path))
		for _, e := range entries {
			prefix := "  "
			if e.IsDir() {
				prefix = "📁 "
			} else {
				prefix = "📄 "
			}
			buf.WriteString(fmt.Sprintf("%s%s\n", prefix, e.Name()))
		}
		return ToolResult(map[string]any{"output": buf.String(), "path": path}), nil
	}

	// 读取文件
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("文件读取失败", "path", path, "err", err)
		return ToolError(fmt.Sprintf("读取文件失败: %v", err)), nil
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	// 行范围过滤
	startLine := 1
	endLine := len(lines)
	if v, ok := args["start_line"].(float64); ok && v > 0 {
		startLine = int(v)
	}
	if v, ok := args["end_line"].(float64); ok && v > 0 {
		endLine = int(v)
	}

	// 边界检查
	if startLine < 1 {
		startLine = 1
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > len(lines) {
		startLine = len(lines)
	}

	selectedLines := lines[startLine-1 : endLine]
	output := strings.Join(selectedLines, "\n")

	result, _ := json.Marshal(map[string]any{
		"output":     output,
		"path":       path,
		"totalLines": len(lines),
		"startLine":  startLine,
		"endLine":    endLine,
	})

	return string(result), nil
}

// ───────────────────────────── 文件写入工具 ─────────────────────────────

// FileWriteTool 实现文件写入功能。
type FileWriteTool struct{}

// Name 返回工具名称。
func (t *FileWriteTool) Name() string { return "file_write" }

// Description 返回工具描述。
func (t *FileWriteTool) Description() string {
	return "将内容写入文件。如果文件已存在则覆盖。"
}

// Toolset 返回工具所属工具集。
func (t *FileWriteTool) Toolset() string { return "file" }

// Emoji 返回工具图标。
func (t *FileWriteTool) Emoji() string { return "✏️" }

// IsAvailable 文件写入始终可用。
func (t *FileWriteTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *FileWriteTool) MaxResultChars() int { return 1000 }

// Schema 返回工具的 JSON Schema。
func (t *FileWriteTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "file_write",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "目标文件路径 (绝对路径或相对路径)",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "要写入的文件内容",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

// Execute 执行文件写入。
// 检查敏感路径 → 确保目录存在 → 写入文件。
func (t *FileWriteTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ToolError("参数 path 是必填项且必须为字符串"), nil
	}
	content, ok := args["content"].(string)
	if !ok {
		return ToolError("参数 content 是必填项且必须为字符串"), nil
	}

	// 安全敏感路径检查
	if isPathSensitive(path) {
		slog.Warn("文件写入被阻止 (敏感路径)", "path", path)
		return ToolError(fmt.Sprintf("安全限制: 不允许写入敏感路径 %s", path)), nil
	}

	// 确保父目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Error("创建目录失败", "dir", dir, "err", err)
		return ToolError(fmt.Sprintf("创建目录失败: %v", err)), nil
	}

	// 写入文件
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		slog.Error("文件写入失败", "path", path, "err", err)
		return ToolError(fmt.Sprintf("写入文件失败: %v", err)), nil
	}

	slog.Info("文件写入成功", "path", path, "size", len(content))
	result, _ := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("文件写入成功: %s (%d 字节)", path, len(content)),
		"path":    path,
		"size":    len(content),
	})

	return string(result), nil
}

// ───────────────────────────── 文件编辑工具 ─────────────────────────────

// FileEditTool 实现文件编辑功能 (基于文本替换)。
type FileEditTool struct{}

// Name 返回工具名称。
func (t *FileEditTool) Name() string { return "file_edit" }

// Description 返回工具描述。
func (t *FileEditTool) Description() string {
	return "替换文件中的指定文本。old_text 必须在文件中唯一匹配。"
}

// Toolset 返回工具所属工具集。
func (t *FileEditTool) Toolset() string { return "file" }

// Emoji 返回工具图标。
func (t *FileEditTool) Emoji() string { return "📝" }

// IsAvailable 文件编辑始终可用。
func (t *FileEditTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *FileEditTool) MaxResultChars() int { return 1000 }

// Schema 返回工具的 JSON Schema。
func (t *FileEditTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "file_edit",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "要编辑的文件路径",
				},
				"old_text": map[string]any{
					"type":        "string",
					"description": "要被替换的原始文本 (必须在文件中唯一存在)",
				},
				"new_text": map[string]any{
					"type":        "string",
					"description": "替换后的新文本 (空字符串表示删除)",
				},
			},
			"required": []string{"path", "old_text"},
		},
	}
}

// Execute 执行文件编辑。
// 读取文件 → 查找替换 → 写回文件。
func (t *FileEditTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ToolError("参数 path 是必填项且必须为字符串"), nil
	}
	oldText, ok := args["old_text"].(string)
	if !ok || oldText == "" {
		return ToolError("参数 old_text 是必填项且必须为字符串"), nil
	}
	newText, _ := args["new_text"].(string)

	// 安全敏感路径检查
	if isPathSensitive(path) {
		slog.Warn("文件编辑被阻止 (敏感路径)", "path", path)
		return ToolError(fmt.Sprintf("安全限制: 不允许编辑敏感路径 %s", path)), nil
	}

	// 读取文件
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("文件读取失败", "path", path, "err", err)
		return ToolError(fmt.Sprintf("读取文件失败: %v", err)), nil
	}

	content := string(data)

	// 查找并替换
	count := strings.Count(content, oldText)
	if count == 0 {
		return ToolError(fmt.Sprintf("old_text 在文件中不存在: %s", path)), nil
	}
	if count > 1 {
		return ToolError(fmt.Sprintf("old_text 在文件中出现 %d 次，无法唯一匹配。请提供更多上下文以确保唯一性。", count)), nil
	}

	newContent := strings.Replace(content, oldText, newText, 1)

	// 写回文件
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		slog.Error("文件编辑写回失败", "path", path, "err", err)
		return ToolError(fmt.Sprintf("写回文件失败: %v", err)), nil
	}

	slog.Info("文件编辑成功", "path", path)
	result, _ := json.Marshal(map[string]any{
		"output": fmt.Sprintf("文件编辑成功: %s (替换了 %d 处)", path, 1),
		"path":   path,
	})

	return string(result), nil
}

// ───────────────────────────── patch 精确替换工具 ─────────────────────────────

// PatchTool 实现文件精确文本替换功能。
// 与 file_edit 不同，允许指定替换次数。
type PatchTool struct{}

// Name 返回工具名称。
func (t *PatchTool) Name() string { return "patch" }

// Description 返回工具描述。
func (t *PatchTool) Description() string {
	return "对文件进行精确的文本替换。支持指定替换次数，适用于需要批量替换的场景。"
}

// Toolset 返回工具所属工具集。
func (t *PatchTool) Toolset() string { return "file" }

// Emoji 返回工具图标。
func (t *PatchTool) Emoji() string { return "🔧" }

// IsAvailable patch 工具始终可用。
func (t *PatchTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *PatchTool) MaxResultChars() int { return 1000 }

// Schema 返回工具的 JSON Schema。
func (t *PatchTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "patch",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "要编辑的文件路径",
				},
				"old_text": map[string]any{
					"type":        "string",
					"description": "要被替换的原始文本",
				},
				"new_text": map[string]any{
					"type":        "string",
					"description": "替换后的新文本 (空字符串表示删除)",
				},
				"expected_replacements": map[string]any{
					"type":        "integer",
					"description": "期望的替换次数，默认为 1。如果实际匹配次数不等于此值则拒绝执行。",
				},
			},
			"required": []string{"path", "old_text"},
		},
	}
}

// Execute 执行文件 patch 替换。
// 读取文件 → 验证匹配次数 → 执行替换 → 写回文件。
func (t *PatchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ToolError("参数 path 是必填项且必须为字符串"), nil
	}
	oldText, ok := args["old_text"].(string)
	if !ok || oldText == "" {
		return ToolError("参数 old_text 是必填项且必须为字符串"), nil
	}
	newText, _ := args["new_text"].(string)

	// 期望替换次数，默认 1
	expectedReplacements := 1
	if v, ok := args["expected_replacements"].(float64); ok && v > 0 {
		expectedReplacements = int(v)
	}

	// 安全敏感路径检查
	if isPathSensitive(path) {
		slog.Warn("文件 patch 被阻止 (敏感路径)", "path", path)
		return ToolError(fmt.Sprintf("安全限制: 不允许编辑敏感路径 %s", path)), nil
	}

	// 读取文件
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("文件读取失败", "path", path, "err", err)
		return ToolError(fmt.Sprintf("读取文件失败: %v", err)), nil
	}

	content := string(data)

	// 统计匹配次数
	count := strings.Count(content, oldText)
	if count == 0 {
		return ToolError(fmt.Sprintf("old_text 在文件中不存在: %s", path)), nil
	}
	if count != expectedReplacements {
		return ToolError(fmt.Sprintf("old_text 在文件中出现 %d 次，但期望替换次数为 %d。请调整 expected_replacements 参数或提供更多上下文。", count, expectedReplacements)), nil
	}

	// 执行替换
	newContent := strings.Replace(content, oldText, newText, expectedReplacements)

	// 写回文件
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		slog.Error("文件 patch 写回失败", "path", path, "err", err)
		return ToolError(fmt.Sprintf("写回文件失败: %v", err)), nil
	}

	slog.Info("文件 patch 成功", "path", path, "replacements", expectedReplacements)
	result, _ := json.Marshal(map[string]any{
		"output":       fmt.Sprintf("文件 patch 成功: %s (替换了 %d 处)", path, expectedReplacements),
		"path":         path,
		"replacements": expectedReplacements,
	})

	return string(result), nil
}

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
			},
			"required": []string{"pattern"},
		},
	}
}

// Execute 执行文件搜索。
// 在指定目录下搜索匹配模式的文件内容。
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

	// 安全敏感路径检查
	if isPathSensitive(searchPath) {
		slog.Warn("文件搜索被阻止 (敏感路径)", "path", searchPath)
		return ToolError(fmt.Sprintf("安全限制: 不允许搜索敏感路径 %s", searchPath)), nil
	}

	// 收集匹配的文件
	var results []map[string]any
	maxResults := 50 // 限制最大结果数

	err := filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过无法访问的文件
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

		// glob 过滤
		matched, _ := filepath.Match(globPattern, info.Name())
		if !matched && globPattern != "*" {
			return nil
		}

		// 检查是否达到最大结果
		if len(results) >= maxResults {
			return filepath.SkipAll
		}

		// 读取文件内容并搜索
		data, readErr := os.ReadFile(path)
		if readErr != nil || len(data) > 1<<20 { // 跳过大于 1MB 的文件
			return nil
		}

		content := string(data)
		if strings.Contains(content, pattern) {
			// 查找匹配行
			var matchingLines []map[string]any
			lines := strings.Split(content, "\n")
			for i, line := range lines {
				if strings.Contains(line, pattern) {
					matchingLines = append(matchingLines, map[string]any{
						"line_number": i + 1,
						"content":     strings.TrimSpace(line),
					})
					if len(matchingLines) >= 5 { // 每个文件最多 5 个匹配行
						break
					}
				}
			}

			results = append(results, map[string]any{
				"file":    path,
				"matches": len(matchingLines),
				"lines":   matchingLines,
			})
		}
		return nil
	})

	if err != nil {
		slog.Warn("文件搜索遍历失败", "path", searchPath, "err", err)
	}

	if len(results) == 0 {
		result, _ := json.Marshal(map[string]any{
			"output":  fmt.Sprintf("在 %s 中未找到匹配 '%s' 的文件", searchPath, pattern),
			"pattern": pattern,
			"path":    searchPath,
			"results": []any{},
		})
		return string(result), nil
	}

	resultJSON, _ := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("在 %s 中找到 %d 个匹配 '%s' 的文件", searchPath, len(results), pattern),
		"pattern": pattern,
		"path":    searchPath,
		"results": results,
	})

	return string(resultJSON), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	reg := GetRegistry()
	reg.Register(&FileReadTool{})
	reg.Register(&FileWriteTool{})
	reg.Register(&FileEditTool{})
	reg.Register(&PatchTool{})
	reg.Register(&FileSearchTool{})
}
