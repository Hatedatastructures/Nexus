// Package context 提供 @file / @diff / @git / @url 等上下文引用的展开功能。
// 在用户消息中检测特殊引用标记，异步展开为实际内容，并控制 token 预算。
package context

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	refTokenBudgetSoft = 0.25 // 软限制: 25% 的总 token 预算
	refTokenBudgetHard = 0.50 // 硬限制: 50% 的总 token 预算
	charsPerToken      = 4    // 粗略 token 估算
)

// 屏蔽的敏感路径前缀
var sensitivePathPrefixes = []string{
	".ssh", ".aws", ".gnupg", ".gpg", ".nexus/.env",
	".env", "credentials", ".netrc", ".npmrc",
}

// ───────────────────────────── 数据结构 ─────────────────────────────

// ContextReference 表示解析出的上下文引用。
type ContextReference struct {
	Raw       string // 原始匹配文本
	Kind      string // 类型: file, folder, diff, staged, git, url
	Target    string // 目标路径或 URL
	LineStart int    // 起始行号 (0 = 不限)
	LineEnd   int    // 结束行号 (0 = 不限)
}

// referencePattern 匹配 @file(path), @diff, @staged, @git(log), @folder(path), @url(http...)
var referencePattern = regexp.MustCompile(`@(file|folder|diff|staged|git|url)\(([^)]+)\)|@(diff|staged)\b`)

// ───────────────────────────── 核心函数 ─────────────────────────────

// PreprocessReferences 检测并展开消息中的上下文引用。
// tokenBudget 为总 token 预算，展开内容不超过此预算的硬限制。
func PreprocessReferences(ctx context.Context, message string, tokenBudget int) (string, error) {
	refs := ParseReferences(message)
	if len(refs) == 0 {
		return message, nil
	}

	softLimit := int(float64(tokenBudget) * refTokenBudgetSoft * charsPerToken)
	hardLimit := int(float64(tokenBudget) * refTokenBudgetHard * charsPerToken)

	var expanded []string
	totalChars := 0

	for _, ref := range refs {
		if totalChars >= hardLimit {
			break
		}

		content, err := expandReference(ctx, ref)
		if err != nil {
			slog.Debug("展开引用失败", "ref", ref.Raw, "err", err)
			continue
		}

		if content == "" {
			continue
		}

		// 截断到剩余预算
		remaining := hardLimit - totalChars
		if len(content) > remaining {
			content = content[:remaining] + "\n... (截断)"
		}

		expanded = append(expanded, fmt.Sprintf("=== %s ===\n%s", ref.Raw, content))
		totalChars += len(content)
	}

	if len(expanded) == 0 {
		return message, nil
	}

	// 替换原始引用为展开内容
	result := message
	for i, ref := range refs {
		if i < len(expanded) {
			result = strings.Replace(result, ref.Raw, expanded[i], 1)
		}
	}

	// 如果超过软限制，添加警告
	if totalChars > softLimit {
		result = fmt.Sprintf("[注意: 上下文引用内容较多 (%d 字符)]\n\n%s", totalChars, result)
	}

	return result, nil
}

// ParseReferences 从消息中解析所有上下文引用。
func ParseReferences(message string) []ContextReference {
	matches := referencePattern.FindAllStringSubmatch(message, -1)
	var refs []ContextReference

	for _, match := range matches {
		ref := ContextReference{Raw: match[0]}

		if match[1] != "" {
			// @kind(target) 格式
			ref.Kind = match[1]
			ref.Target = match[2]
		} else if match[3] != "" {
			// @diff / @staged 格式 (无参数)
			ref.Kind = match[3]
		}

		// 解析行号范围: file(path:L10-L20)
		if idx := strings.LastIndex(ref.Target, ":L"); idx > 0 {
			rangePart := ref.Target[idx+2:]
			ref.Target = ref.Target[:idx]
			parts := strings.SplitN(rangePart, "-", 2)
			if len(parts) == 2 {
				fmt.Sscanf(parts[0], "%d", &ref.LineStart)
				fmt.Sscanf(parts[1], "%d", &ref.LineEnd)
			}
		}

		refs = append(refs, ref)
	}

	return refs
}

// ───────────────────────────── 展开函数 ─────────────────────────────

func expandReference(ctx context.Context, ref ContextReference) (string, error) {
	switch ref.Kind {
	case "file":
		return expandFile(ref.Target, ref.LineStart, ref.LineEnd)
	case "folder":
		return expandFolder(ref.Target)
	case "diff":
		return expandGitDiff(ctx, false)
	case "staged":
		return expandGitDiff(ctx, true)
	case "git":
		return expandGitLog(ctx, ref.Target)
	case "url":
		return expandURL(ctx, ref.Target)
	default:
		return "", fmt.Errorf("未知引用类型: %s", ref.Kind)
	}
}

// expandFile 展开文件内容。
func expandFile(path string, lineStart, lineEnd int) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	if isSensitivePath(absPath) {
		return "", fmt.Errorf("敏感路径被屏蔽: %s", path)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}

	content := string(data)

	// 行号范围截取
	if lineStart > 0 || lineEnd > 0 {
		lines := strings.Split(content, "\n")
		start := lineStart - 1 // 转为 0-indexed
		if start < 0 {
			start = 0
		}
		end := lineEnd
		if end <= 0 || end > len(lines) {
			end = len(lines)
		}
		if start >= end {
			return "", fmt.Errorf("无效行号范围: L%d-L%d", lineStart, lineEnd)
		}
		content = strings.Join(lines[start:end], "\n")
	}

	return content, nil
}

// expandFolder 展开目录树。
func expandFolder(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	if isSensitivePath(absPath) {
		return "", fmt.Errorf("敏感路径被屏蔽: %s", path)
	}

	var b strings.Builder
	err = filepath.Walk(absPath, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// 跳过隐藏目录
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") && p != absPath {
			return filepath.SkipDir
		}

		// 跳过大文件
		if !info.IsDir() && info.Size() > 1024*1024 {
			return nil
		}

		rel, _ := filepath.Rel(absPath, p)
		depth := strings.Count(rel, string(os.PathSeparator))
		prefix := strings.Repeat("  ", depth)

		if info.IsDir() {
			b.WriteString(fmt.Sprintf("%s%s/\n", prefix, info.Name()))
		} else {
			b.WriteString(fmt.Sprintf("%s%s\n", prefix, info.Name()))
		}

		return nil
	})

	return b.String(), err
}

// expandGitDiff 展开 git diff。
func expandGitDiff(ctx context.Context, staged bool) (string, error) {
	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--stat")

	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git diff 失败: %s", stderr.String())
	}

	return stdout.String(), nil
}

// expandGitLog 展开 git log。
func expandGitLog(ctx context.Context, spec string) (string, error) {
	args := []string{"log", "--oneline", "-20"}
	if spec != "" {
		args = append(args, spec)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git log 失败: %s", stderr.String())
	}

	return stdout.String(), nil
}

// expandURL 展开 URL 内容（占位实现，需集成 web 工具）。
func expandURL(ctx context.Context, url string) (string, error) {
	// URL 安全检查
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return fmt.Sprintf("[URL: %s — 需要 web_extract 工具展开]", url), nil
	}
	return "", fmt.Errorf("不支持的 URL: %s", url)
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// isSensitivePath 检查路径是否包含敏感目录。
func isSensitivePath(path string) bool {
	lower := strings.ToLower(path)
	for _, prefix := range sensitivePathPrefixes {
		if strings.Contains(lower, prefix) {
			return true
		}
	}
	return false
}
