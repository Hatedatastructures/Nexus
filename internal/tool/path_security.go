// Package tool 提供路径安全防护功能。
// 包含路径遍历检测、目录边界验证和路径净化。
// 防止通过 .. 或符号链接逃逸出允许的目录范围。
package tool

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ───────────────────────────── 路径遍历防护 ─────────────────────────────

// ValidateWithinDir 验证给定路径在解析后是否位于允许的目录内。
// 会解析符号链接并规范化路径，然后检查是否以允许目录为前缀。
// 返回 nil 表示路径安全，否则返回描述违规的 error。
func ValidateWithinDir(path, allowedDir string) error {
	// 规范化允许目录
	absAllowed, err := filepath.Abs(allowedDir)
	if err != nil {
		return &PathSecurityError{
			Path:   allowedDir,
			Reason: "无法解析允许目录的绝对路径",
			Err:    err,
		}
	}

	// 规范化目标路径
	absPath, err := filepath.Abs(path)
	if err != nil {
		return &PathSecurityError{
			Path:   path,
			Reason: "无法解析目标路径的绝对路径",
			Err:    err,
		}
	}

	// 解析符号链接获取真实路径
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// 文件可能尚不存在，回退到使用绝对路径
		// 对于写入场景，检查父目录
		if os.IsNotExist(err) {
			parentDir := filepath.Dir(absPath)
			realParent, evalErr := filepath.EvalSymlinks(parentDir)
			if evalErr != nil {
				return &PathSecurityError{
					Path:   path,
					Reason: "无法解析父目录的符号链接",
					Err:    evalErr,
				}
			}
			// 将真实父目录与文件名拼接
			realPath = filepath.Join(realParent, filepath.Base(absPath))
		} else {
			return &PathSecurityError{
				Path:   path,
				Reason: "无法解析符号链接",
				Err:    err,
			}
		}
	}

	// 同样解析允许目录的符号链接
	realAllowed, err := filepath.EvalSymlinks(absAllowed)
	if err != nil {
		return &PathSecurityError{
			Path:   allowedDir,
			Reason: "无法解析允许目录的符号链接",
			Err:    err,
		}
	}

	// 确保路径分隔符一致
	sep := string(os.PathSeparator)
	realAllowed = strings.TrimSuffix(realAllowed, sep) + sep
	realPath = strings.TrimSuffix(realPath, sep)

	// 检查路径是否以允许目录为前缀
	if realPath == strings.TrimSuffix(realAllowed, sep) ||
		strings.HasPrefix(realPath+sep, realAllowed) {
		return nil
	}

	return &PathSecurityError{
		Path:   path,
		Reason: "路径超出了允许的目录范围",
	}
}

// PathSecurityError 表示路径安全检查失败的错误。
type PathSecurityError struct {
	Path   string // 违规的路径
	Reason string // 违规原因
	Err    error  // 底层错误 (可选)
}

// Error 实现 error 接口。
func (e *PathSecurityError) Error() string {
	if e.Err != nil {
		return "路径安全违规: " + e.Reason + " (" + e.Path + ": " + e.Err.Error() + ")"
	}
	return "路径安全违规: " + e.Reason + " (" + e.Path + ")"
}

// Unwrap 实现 errors.Unwrap 接口。
func (e *PathSecurityError) Unwrap() error {
	return e.Err
}

// ───────────────────────────── 遍历组件检测 ─────────────────────────────

// HasTraversalComponent 快速检查路径中是否包含遍历组件 ("..")。
// 适用于不需要完整目录边界验证的快速过滤场景。
// 检查路径的每个组成部分是否为 ".."。
func HasTraversalComponent(path string) bool {
	// 清理路径后按分隔符分割
	cleaned := filepath.Clean(path)
	parts := strings.Split(cleaned, string(os.PathSeparator))

	for _, part := range parts {
		if part == ".." {
			return true
		}
	}
	return false
}

// ───────────────────────────── 路径净化 ─────────────────────────────

// SanitizePath 移除路径中的危险组件，返回安全的路径。
// 处理的危险组件包括:
//   - 遍历序列 (..)
//   - 空路径段
//   - 不可见 Unicode 字符
//   - 路径前缀中的分隔符
func SanitizePath(path string) string {
	// 替换反斜杠为正斜杠 (统一分隔符)
	cleaned := strings.ReplaceAll(path, `\`, "/")

	// 移除不可见的 Unicode 字符 (零宽空格等)
	cleaned = removeInvisibleChars(cleaned)

	// 按分隔符分割，过滤危险组件
	parts := strings.Split(cleaned, "/")
	var safeParts []string

	for _, part := range parts {
		// 跳过空段
		if part == "" {
			continue
		}
		// 跳过遍历组件
		if part == ".." {
			continue
		}
		// 跳过当前目录引用 (保留有意义的名称)
		if part == "." {
			continue
		}
		safeParts = append(safeParts, part)
	}

	if len(safeParts) == 0 {
		return "."
	}

	return strings.Join(safeParts, "/")
}

// removeInvisibleChars 移除字符串中的不可见 Unicode 字符。
// 保留常规空白字符 (空格、制表符、换行符)。
func removeInvisibleChars(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))

	for _, r := range s {
		// 保留常规空白和可打印字符
		if unicode.IsSpace(r) || unicode.IsPrint(r) {
			buf.WriteRune(r)
		}
		// 跳过不可见字符 (零宽空格、BOM 等)
	}

	return buf.String()
}
