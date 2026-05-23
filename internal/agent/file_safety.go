// Package agent 提供文件写入安全检查功能。
// FileSafetyChecker 在工具执行层面对文件写入操作进行二次防护，
// 检查目标路径是否为受保护的敏感文件，并限制单次写入大小。
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ───────────────────────────── 文件安全检查器 ─────────────────────────────

// FileSafetyChecker 对文件写入操作进行安全检查。
// 在工具 dispatch 层面提供第二道防线，防止 AI 代理写入敏感文件。
type FileSafetyChecker struct {
	// protectedPaths 是受保护路径的 glob 模式列表。
	// 使用 filepath.Match 进行匹配，支持 * 和 ? 通配符。
	protectedPaths []string

	// protectedExtensions 是受保护的文件扩展名列表 (含点号，如 ".pem")。
	protectedExtensions []string

	// maxWriteSize 是单次写入的最大字节数。默认 10MB。
	maxWriteSize int64

	// allowedRoot 是允许写入的根目录。如果非空，所有写入路径必须在此目录下。
	// 防止路径遍历攻击 (如 "../../../etc/passwd")。
	allowedRoot string

	// hermesHome 是 Nexus 配置/数据目录（如 ~/.nexus）。
	hermesHome string

	// hermesRoot 是项目根目录。
	hermesRoot string
}

// 默认最大写入大小: 10MB
const defaultMaxWriteSize int64 = 10 * 1024 * 1024

// NewFileSafetyChecker 创建带有默认保护规则的 FileSafetyChecker。
// 默认保护的路径和扩展名涵盖常见的凭证文件、密钥文件和大型依赖目录。
func NewFileSafetyChecker() *FileSafetyChecker {
	return &FileSafetyChecker{
		protectedPaths: []string{
			// 环境变量文件
			".env",
			".env.*",
			// SSH 相关
			".ssh/*",
			// GPG 相关
			".gnupg/*",
			// 云服务凭证
			".aws/credentials",
			".kube/config",
			// 大型依赖目录 (防止写入巨型目录)
			"node_modules/**",
			".git/objects/**",
		},
		protectedExtensions: []string{
			// 证书和密钥
			".pem",
			".key",
			".p12",
			".pfx",
			".cert",
			".crt",
			".keystore",
		},
		maxWriteSize: defaultMaxWriteSize,
	}
}

// NewFileSafetyCheckerWithConfig 使用自定义配置创建 FileSafetyChecker。
// 允许调用方覆盖默认的保护规则和大小限制。
func NewFileSafetyCheckerWithConfig(paths []string, extensions []string, maxSize int64) *FileSafetyChecker {
	fs := NewFileSafetyChecker()
	if paths != nil {
		fs.protectedPaths = paths
	}
	if extensions != nil {
		fs.protectedExtensions = extensions
	}
	if maxSize > 0 {
		fs.maxWriteSize = maxSize
	}
	return fs
}

// SetAllowedRoot 设置允许写入的根目录。
// 设置后，所有写入操作的目标路径必须在此目录下，防止路径遍历攻击。
func (fs *FileSafetyChecker) SetAllowedRoot(root string) {
	if root != "" {
		absRoot, err := filepath.Abs(root)
		if err == nil {
			if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
				fs.allowedRoot = resolved
				return
			}
			fs.allowedRoot = absRoot
		}
	}
}

// SetHermesPaths 设置 Nexus 配置目录和项目根目录路径。
// 用于 CheckRead 和扩展的 CheckWrite 中判断凭证文件位置。
func (fs *FileSafetyChecker) SetHermesPaths(home, root string) {
	if home != "" {
		if abs, err := filepath.Abs(home); err == nil {
			if resolved, err := filepath.EvalSymlinks(abs); err == nil {
				fs.hermesHome = resolved
			} else {
				fs.hermesHome = abs
			}
		}
	}
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			if resolved, err := filepath.EvalSymlinks(abs); err == nil {
				fs.hermesRoot = resolved
			} else {
				fs.hermesRoot = abs
			}
		}
	}
}

// ───────────────────────────── 核心检查方法 ─────────────────────────────

// CheckWrite 检查对指定路径的写入操作是否允许。
// 返回 allowed=true 表示允许写入，reason 为空字符串；
// 返回 allowed=false 表示拒绝写入，reason 包含人类可读的拒绝原因。
func (fs *FileSafetyChecker) CheckWrite(path string, contentSize int64) (allowed bool, reason string) {
	if contentSize > fs.maxWriteSize {
		return false, "写入内容超过大小限制 (" + formatSize(contentSize) + " > " + formatSize(fs.maxWriteSize) + ")"
	}

	if fs.allowedRoot != "" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return false, "无法解析路径: " + err.Error()
		}
		resolved := absPath
		if r, err := filepath.EvalSymlinks(absPath); err == nil {
			resolved = r
		}
		rel, err := filepath.Rel(fs.allowedRoot, resolved)
		if err != nil || strings.HasPrefix(rel, "..") {
			return false, "路径超出允许范围: " + path
		}
	}

	cleanPath := filepath.ToSlash(filepath.Clean(path))

	ext := strings.ToLower(filepath.Ext(path))
	for _, protected := range fs.protectedExtensions {
		if ext == protected {
			return false, "不允许写入受保护的文件类型 (" + protected + "): " + path
		}
	}

	for _, pattern := range fs.protectedPaths {
		if matchProtectedPath(cleanPath, pattern) {
			return false, "不允许写入受保护的路径 (" + pattern + "): " + path
		}
	}

	// 检查 Hermes 控制文件写入拒绝
	if reason := fs.checkHermesDenyWrite(absOrResolvedPath(path)); reason != "" {
		return false, reason
	}

	return true, ""
}

// CheckRead 检查对指定路径的读取操作是否允许。
// 阻止读取凭证文件和敏感目录。
func (fs *FileSafetyChecker) CheckRead(path string) (allowed bool, reason string) {
	resolved, err := resolvePath(path)
	if err != nil {
		return true, ""
	}

	cleanPath := filepath.ToSlash(filepath.Clean(path))
	for _, pattern := range fs.protectedPaths {
		if matchProtectedPath(cleanPath, pattern) {
			return false, "不允许读取受保护的路径 (" + pattern + "): " + path
		}
	}

	if reason := fs.checkCredentialDenyRead(resolved); reason != "" {
		return false, reason
	}

	return true, ""
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return abs, nil
		}
		return "", err
	}
	return resolved, nil
}

func absOrResolvedPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

// credentialDenyFiles 在 hermesHome/hermesRoot 下拒绝读取的精确文件名。
var credentialDenyFiles = map[string]bool{
	"auth.json":                  true,
	"auth.lock":                  true,
	".anthropic_oauth.json":      true,
	".env":                       true,
	"webhook_subscriptions.json": true,
}

func (fs *FileSafetyChecker) checkCredentialDenyRead(resolved string) string {
	name := filepath.Base(resolved)
	if !credentialDenyFiles[name] {
		for _, base := range []string{fs.hermesHome, fs.hermesRoot} {
			if base == "" {
				continue
			}
			rel, err := filepath.Rel(base, resolved)
			if err == nil && strings.HasPrefix(filepath.ToSlash(rel), "mcp-tokens/") {
				return "不允许读取 mcp-tokens 目录: " + resolved
			}
		}
		return ""
	}
	for _, base := range []string{fs.hermesHome, fs.hermesRoot} {
		if base == "" {
			continue
		}
		rel, err := filepath.Rel(base, resolved)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return "不允许读取凭证文件 " + name + ": " + resolved
		}
	}
	return ""
}

// hermesDenyWriteFiles 在 hermesHome 下拒绝写入的控制文件。
var hermesDenyWriteFiles = map[string]bool{
	"auth.json":                  true,
	"config.yaml":                true,
	"webhook_subscriptions.json": true,
}

func (fs *FileSafetyChecker) checkHermesDenyWrite(resolved string) string {
	name := filepath.Base(resolved)
	if hermesDenyWriteFiles[name] && fs.hermesHome != "" {
		rel, err := filepath.Rel(fs.hermesHome, resolved)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return "不允许写入控制文件 " + name + ": " + resolved
		}
	}
	if fs.hermesHome != "" {
		rel, err := filepath.Rel(fs.hermesHome, resolved)
		if err == nil && strings.HasPrefix(filepath.ToSlash(rel), "mcp-tokens/") {
			return "不允许写入 mcp-tokens 目录: " + resolved
		}
	}
	return ""
}

// matchProtectedPath 检查路径是否匹配受保护的 glob 模式。
// 支持 ** 递归匹配 (filepath.Match 不支持，需要特殊处理)。
func matchProtectedPath(path, pattern string) bool {
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**") + "/"
		return strings.HasPrefix(path, prefix) || path == strings.TrimSuffix(pattern, "/**")
	}

	matched, err := filepath.Match(pattern, path)
	if err != nil {
		return false
	}
	if matched {
		return true
	}

	if strings.Contains(pattern, "/") {
		parts := strings.Split(path, "/")
		for i := range parts {
			subPath := strings.Join(parts[i:], "/")
			matched, _ = filepath.Match(pattern, subPath)
			if matched {
				return true
			}
		}
	}

	return false
}

// formatSize 将字节数格式化为人类可读的字符串。
func formatSize(bytes int64) string {
	const (
		KB int64 = 1024
		MB int64 = KB * 1024
	)
	switch {
	case bytes >= MB:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
