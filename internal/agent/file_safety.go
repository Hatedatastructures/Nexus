// Package agent 提供文件写入安全检查功能。
// FileSafetyChecker 在工具执行层面对文件写入操作进行二次防护，
// 检查目标路径是否为受保护的敏感文件，并限制单次写入大小。
package agent

import (
	"fmt"
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

// ───────────────────────────── 核心检查方法 ─────────────────────────────

// CheckWrite 检查对指定路径的写入操作是否允许。
// 返回 allowed=true 表示允许写入，reason 为空字符串；
// 返回 allowed=false 表示拒绝写入，reason 包含人类可读的拒绝原因。
//
// 检查项目:
//  1. 内容大小是否超过限制
//  2. 文件扩展名是否为受保护类型
//  3. 路径是否匹配受保护的 glob 模式
func (fs *FileSafetyChecker) CheckWrite(path string, contentSize int64) (allowed bool, reason string) {
	// 检查写入大小限制
	if contentSize > fs.maxWriteSize {
		return false, "写入内容超过大小限制 (" + formatSize(contentSize) + " > " + formatSize(fs.maxWriteSize) + ")"
	}

	// 规范化路径用于匹配
	cleanPath := filepath.ToSlash(filepath.Clean(path))

	// 检查文件扩展名
	ext := strings.ToLower(filepath.Ext(path))
	for _, protected := range fs.protectedExtensions {
		if ext == protected {
			return false, "不允许写入受保护的文件类型 (" + protected + "): " + path
		}
	}

	// 检查路径 glob 模式
	for _, pattern := range fs.protectedPaths {
		if matchProtectedPath(cleanPath, pattern) {
			return false, "不允许写入受保护的路径 (" + pattern + "): " + path
		}
	}

	return true, ""
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// matchProtectedPath 检查路径是否匹配受保护的 glob 模式。
// 支持 ** 递归匹配 (filepath.Match 不支持，需要特殊处理)。
func matchProtectedPath(path, pattern string) bool {
	// 处理 ** 递归匹配: 将 "dir/**" 转换为前缀匹配 "dir/"
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**") + "/"
		return strings.HasPrefix(path, prefix) || path == strings.TrimSuffix(pattern, "/**")
	}

	// 使用 filepath.Match 进行标准 glob 匹配
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		return false
	}
	if matched {
		return true
	}

	// 尝试匹配路径的每个组成部分 (处理带目录的模式)
	// 例如 ".ssh/*" 应该匹配 "/home/user/.ssh/id_rsa"
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
