// Package context 提供上下文文件加载功能。
// 按优先级搜索 AGENTS.md、CLAUDE.md 等上下文文件，将内容注入系统提示词。
package context

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ContextFilePriority 定义上下文文件的搜索优先级。
// 文件按此顺序搜索，找到第一个存在的文件后停止。
// 顺序反映了从项目特定到通用的优先级递减。
var ContextFilePriority = []string{
	"AGENTS.md",
	"CLAUDE.md",
	".cursorrules",
	".nexus.md",
}

// ───────────────────────────── Prompt Injection 检测 ─────────────────────────────

// contextThreatPattern 定义 prompt injection 威胁模式。
type contextThreatPattern struct {
	re    *regexp.Regexp
	label string
}

var contextThreatPatterns = []contextThreatPattern{
	{regexp.MustCompile(`(?i)ignore\s+(previous|all|above|prior)\s+instructions`), "prompt_injection"},
	{regexp.MustCompile(`(?i)disregard\s+(the\s+)?(above|previous|all)\s+(instructions|prompts|directives)`), "prompt_injection"},
	{regexp.MustCompile(`(?i)you\s+are\s+now\s+in\s+developer\s+mode`), "developer_mode_injection"},
	{regexp.MustCompile(`(?i)new\s+system\s*:\s*`), "system_prompt_override"},
	{regexp.MustCompile(`(?i)<\|assistant\|>`), "role_injection"},
	{regexp.MustCompile(`(?i)\[SYSTEM\]\s+RESET`), "system_reset_injection"},
	{regexp.MustCompile(`(?i)forget\s+all\s+(previous|your)\s+(instructions|rules|constraints)`), "memory_injection"},
	{regexp.MustCompile(`(?i)from\s+now\s+on\s*,?\s*(you\s+are|your\s+name|your\s+role)`), "role_override"},
}

// scanContextContent 扫描上下文内容中是否存在 prompt injection 模式。
// 返回发现的威胁标签列表。
func scanContextContent(content string) []string {
	var threats []string
	for _, tp := range contextThreatPatterns {
		if tp.re.MatchString(content) {
			threats = append(threats, tp.label)
		}
	}
	return threats
}

// sanitizeContextContent 清洗上下文内容，移除或转义潜在的 injection 模式。
// 返回清洗后的内容和发现的威胁列表。
func sanitizeContextContent(content string) (string, []string) {
	threats := scanContextContent(content)
	if len(threats) == 0 {
		return content, nil
	}

	// 记录检测到的威胁并继续，但移除可能的恶意标记
	cleaned := content
	// 移除类似角色注入的标记
	for _, tp := range contextThreatPatterns {
		cleaned = tp.re.ReplaceAllString(cleaned, "[已移除: "+tp.label+"]")
	}

	return cleaned, threats
}

// loadContextFiles 按优先级搜索并读取上下文文件内容。
//
// 搜索策略:
//  1. 从当前工作目录开始向上搜索 (最多 5 层)
//  2. 按 ContextFilePriority 定义的顺序查找
//  3. 找到第一个存在的文件后立即返回
//
// 返回格式化后的上下文字符串 (含 Markdown 引用块)。
// 未找到任何文件时返回空字符串。
func (b *Builder) loadContextFiles() string {
	// 获取当前工作目录
	cwd, err := os.Getwd()
	if err != nil {
		slog.Warn("无法获取当前工作目录", "err", err)
		return ""
	}

	// 向上搜索目录 (最多 5 层)
	for depth := 0; depth < 5; depth++ {
		for _, filename := range ContextFilePriority {
			path := filepath.Join(cwd, filename)
			content, err := os.ReadFile(path)
			if err == nil && len(content) > 0 {
				slog.Debug("加载上下文文件", "path", path, "filename", filename)
				rawContent := string(content)

				// 安全扫描: 检测 prompt injection
				if threats := scanContextContent(rawContent); len(threats) > 0 {
					slog.Warn("上下文文件包含潜在的 prompt injection 模式",
						"path", path, "threats", strings.Join(threats, ", "))
					// 清洗内容后使用
					rawContent, _ = sanitizeContextContent(rawContent)
				}

				// 格式化为代码块引用
				return formatContextFileContent(filename, rawContent)
			}
		}

		// 向上一级目录
		parent := filepath.Dir(cwd)
		if parent == cwd {
			break // 已到达根目录
		}
		cwd = parent
	}

	return ""
}

// formatContextFileContent 将上下文文件内容格式化为系统提示词中的引用块。
// 使用 Markdown 格式包裹，便于模型识别文件边界。
func formatContextFileContent(filename, content string) string {
	// 确保内容不会超出合理大小 (最多 8000 字符)
	if len(content) > 8000 {
		content = content[:8000] + "\n...[内容截断，完整文件请查看磁盘]..."
	}

	return "\n\n## 上下文文件: " + filename + "\n\n```markdown\n" + content + "\n```\n"
}
