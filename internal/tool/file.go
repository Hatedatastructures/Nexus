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
	"sync"
)

// ───────────────────────────── 允许目录 (路径安全) ─────────────────────────────

// allowedDir 是文件操作工具允许访问的根目录。
// 默认为当前工作目录，测试中可覆盖此变量以限制文件操作范围。
var (
	allowedDir   = func() string { dir, err := os.Getwd(); if err != nil { return "." }; return dir }()
	allowedDirMu sync.RWMutex
)

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
		fmt.Fprintf(&buf, "目录: %s\n\n", path)
		for _, e := range entries {
			var prefix string
			if e.IsDir() {
				prefix = "📁 "
			} else {
				prefix = "📄 "
			}
			fmt.Fprintf(&buf, "%s%s\n", prefix, e.Name())
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
