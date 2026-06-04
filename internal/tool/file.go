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
	"regexp"
	"strings"
	"sync"
)

// ───────────────────────────── 允许目录 (路径安全) ─────────────────────────────

// allowedDir 是文件操作工具允许访问的根目录。
// 默认为当前工作目录，测试中可覆盖此变量以限制文件操作范围。
var (
	allowedDir   string
	allowedDirMu sync.RWMutex
)

func init() {
	dir, err := os.Getwd()
	if err != nil {
		allowedDir = "."
	} else {
		allowedDir = dir
	}
}

func getAllowedDir() string {
	allowedDirMu.RLock()
	defer allowedDirMu.RUnlock()
	return allowedDir
}

// SetAllowedDir updates the allowed root directory for file operations.
func SetAllowedDir(dir string) {
	if dir != "" {
		allowedDirMu.Lock()
		allowedDir = dir
		allowedDirMu.Unlock()
	}
}

// checkPathSecurity 对路径执行安全检查，返回安全错误或空字符串。
// 依次执行: 遍历组件快速拒绝 → 目录边界完整验证。
// 用于所有文件操作工具的入口处统一拦截。
func checkPathSecurity(path string, sanitize bool) (string, error) {
	// 第一步: 快速拒绝包含遍历组件的路径
	if HasTraversalComponent(path) {
		return "", fmt.Errorf("路径包含遍历组件 (..): %s", path)
	}

	// 第二步: 对需要净化的场景先移除危险组件
	checkPath := path
	if sanitize {
		checkPath = SanitizePath(path)
	}

	// 第三步: 完整目录边界验证
	if err := ValidateWithinDir(checkPath, getAllowedDir()); err != nil {
		return "", fmt.Errorf("路径安全检查失败: %w", err)
	}

	return checkPath, nil
}

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
	".nexus/auth.json",
	".nexus/auth.lock",
	".nexus/mcp-tokens/",
	".nexus/pairing/",
	".nexus/webhook_subscriptions.json",
	// SSH 密钥和配置
	".ssh/id_rsa",
	".ssh/id_ed25519",
	".ssh/authorized_keys",
	".ssh/config",
	".ssh/",
	// 云服务凭证
	".aws/",
	".gnupg/",
	".kube/",
	".docker/",
	".azure/",
	".config/gh/",
	".config/gcloud/",
	// Shell 配置（防止写入）
	".bashrc",
	".zshrc",
	".profile",
	".bash_profile",
	".zprofile",
	".netrc",
	".pgpass",
	".npmrc",
	".pypirc",
	".git-credentials",
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
	homeDir, homeErr := os.UserHomeDir()
	if homeErr == nil && homeDir != "" {
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
			if strings.HasSuffix(cleanLower, rest) || strings.Contains(cleanLower, string(os.PathSeparator)+rest) {
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

	// 路径安全检查: 遍历组件拒绝 + 净化 + 目录边界验证
	safePath, secErr := checkPathSecurity(path, true)
	if secErr != nil {
		slog.Warn("file read blocked (path security)", "path", path, "err", secErr)
		return ToolError(fmt.Sprintf("安全限制: %v", secErr)), nil
	}
	path = safePath

	// 安全敏感路径检查
	if isPathSensitive(path) {
		slog.Warn("file read blocked (sensitive path)", "path", path)
		return ToolError(fmt.Sprintf("安全限制: 不允许读取敏感路径 %s", path)), nil
	}

	// 如果是目录，列出目录内容
	info, err := os.Stat(path)
	if err != nil {
		slog.Warn("file read failed", "path", path, "err", err)
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

	// 文件大小检查: 超过 10MB 拒绝读取
	if info.Size() > 10*1024*1024 {
		slog.Warn("file too large, refusing to read", "path", path, "size_mb", info.Size()/(1024*1024))
		return ToolError(fmt.Sprintf("文件过大 (%d MB)，上限 10 MB", info.Size()/(1024*1024))), nil
	}

	// 读取文件
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("file read failed", "path", path, "err", err)
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

	result, err := json.Marshal(map[string]any{
		"output":     output,
		"path":       path,
		"totalLines": len(lines),
		"startLine":  startLine,
		"endLine":    endLine,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
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

	// 路径安全检查: 遍历组件拒绝 + 目录边界验证 (写入不净化路径)
	if _, secErr := checkPathSecurity(path, false); secErr != nil {
		slog.Warn("file write blocked (path security)", "path", path, "err", secErr)
		return ToolError(fmt.Sprintf("安全限制: %v", secErr)), nil
	}

	// 安全敏感路径检查
	if isPathSensitive(path) {
		slog.Warn("file write blocked (sensitive path)", "path", path)
		return ToolError(fmt.Sprintf("安全限制: 不允许写入敏感路径 %s", path)), nil
	}

	// 确保父目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Error("failed to create directory", "dir", dir, "err", err)
		return ToolError(fmt.Sprintf("创建目录失败: %v", err)), nil
	}

	// 写入文件
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		slog.Error("file write failed", "path", path, "err", err)
		return ToolError(fmt.Sprintf("写入文件失败: %v", err)), nil
	}

	slog.Info("file written successfully", "path", path, "size", len(content))
	result, err := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("文件写入成功: %s (%d 字节)", path, len(content)),
		"path":    path,
		"size":    len(content),
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
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

	// 路径安全检查: 遍历组件拒绝 + 目录边界验证 (编辑不净化路径)
	if _, secErr := checkPathSecurity(path, false); secErr != nil {
		slog.Warn("file edit blocked (path security)", "path", path, "err", secErr)
		return ToolError(fmt.Sprintf("安全限制: %v", secErr)), nil
	}

	// 安全敏感路径检查
	if isPathSensitive(path) {
		slog.Warn("file edit blocked (sensitive path)", "path", path)
		return ToolError(fmt.Sprintf("安全限制: 不允许编辑敏感路径 %s", path)), nil
	}

	// 读取文件
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("file read failed", "path", path, "err", err)
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
		slog.Error("file edit write-back failed", "path", path, "err", err)
		return ToolError(fmt.Sprintf("写回文件失败: %v", err)), nil
	}

	slog.Info("file edit succeeded", "path", path)
	result, err := json.Marshal(map[string]any{
		"output": fmt.Sprintf("文件编辑成功: %s (替换了 %d 处)", path, 1),
		"path":   path,
		"diff":   generateUnifiedDiff(content, newContent, path),
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
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

	// 路径安全检查: 遍历组件拒绝 + 目录边界验证 (patch 不净化路径)
	if _, secErr := checkPathSecurity(path, false); secErr != nil {
		slog.Warn("file patch blocked (path security)", "path", path, "err", secErr)
		return ToolError(fmt.Sprintf("安全限制: %v", secErr)), nil
	}

	// 安全敏感路径检查
	if isPathSensitive(path) {
		slog.Warn("file patch blocked (sensitive path)", "path", path)
		return ToolError(fmt.Sprintf("安全限制: 不允许编辑敏感路径 %s", path)), nil
	}

	// 读取文件
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("file read failed", "path", path, "err", err)
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
		slog.Error("file patch write-back failed", "path", path, "err", err)
		return ToolError(fmt.Sprintf("写回文件失败: %v", err)), nil
	}

	slog.Info("file patch succeeded", "path", path, "replacements", expectedReplacements)
	result, err := json.Marshal(map[string]any{
		"output":       fmt.Sprintf("文件 patch 成功: %s (替换了 %d 处)", path, expectedReplacements),
		"path":         path,
		"replacements": expectedReplacements,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
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

// ───────────────────────────── diff 生成 ─────────────────────────────

// generateUnifiedDiff 生成简易的 unified diff 格式输出。
func generateUnifiedDiff(old, newContent, path string) string {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(newContent, "\n")

	var sb strings.Builder
	sb.WriteString("--- " + path + "\n")
	sb.WriteString("+++ " + path + "\n")

	// 找到不同的行范围
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}

	for i := 0; i < maxLen; i++ {
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if oldLine != "" {
				sb.WriteString(fmt.Sprintf("-%d: %s\n", i+1, oldLine))
			}
			if newLine != "" {
				sb.WriteString(fmt.Sprintf("+%d: %s\n", i+1, newLine))
			}
		}
	}

	return sb.String()
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
