// Package context 提供上下文文件加载功能。
// 按优先级搜索 AGENTS.md、CLAUDE.md 等上下文文件，将内容注入系统提示词。
package context

import (
	"encoding/base64"
	"html"
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
	// ── 英文模式 ──
	{regexp.MustCompile(`(?i)ignore\s+(previous|all|above|prior)\s+instructions`), "prompt_injection"},
	{regexp.MustCompile(`(?i)disregard\s+(the\s+)?(above|previous|all)\s+(instructions|prompts|directives)`), "prompt_injection"},
	{regexp.MustCompile(`(?i)you\s+are\s+now\s+in\s+developer\s+mode`), "developer_mode_injection"},
	{regexp.MustCompile(`(?i)new\s+system\s*:\s*`), "system_prompt_override"},
	{regexp.MustCompile(`(?i)<\|assistant\|>`), "role_injection"},
	{regexp.MustCompile(`(?i)\[SYSTEM\]\s+RESET`), "system_reset_injection"},
	{regexp.MustCompile(`(?i)forget\s+all\s+(previous|your)\s+(instructions|rules|constraints)`), "memory_injection"},
	{regexp.MustCompile(`(?i)from\s+now\s+on\s*,?\s*(you\s+are|your\s+name|your\s+role)`), "role_override"},
	// ── 中文模式 ──
	{regexp.MustCompile(`忽略(之前|上面|以上|先前)(的)?(指令|提示|规则|指示)`), "prompt_injection_zh"},
	{regexp.MustCompile(`你(现在|从现在开始)(是|扮演|叫做)`), "role_override_zh"},
	{regexp.MustCompile(`无视(之前|上面|以上|先前)(的)?(指令|提示|规则|指示)`), "prompt_injection_zh"},
	{regexp.MustCompile(`抛弃(之前|上面|以上|先前)(的)?(指令|提示|规则|指示)`), "prompt_injection_zh"},
	// ── 俄语模式 ──
	{regexp.MustCompile(`(?i)игнориру(й|ть)\s+(предыдущие|все)\s+(инструкции|указания)`), "prompt_injection_ru"},
	{regexp.MustCompile(`(?i)забудь\s+(все|предыдущие)\s+(инструкции|правила)`), "memory_injection_ru"},
	// ── 阿拉伯语模式 ──
	{regexp.MustCompile(`تجاهل\s+(التعليمات|التوجيهات)\s+(السابقة|أعلاه)`), "prompt_injection_ar"},
	{regexp.MustCompile(`انت\s+الان\s+في\s+وضع\s+المطور`), "developer_mode_injection_ar"},
}

// base64InjectionRe 匹配长度超过 100 字符的 base64 编码序列。
// 攻击者可能使用 base64 编码绕过明文注入检测。
var base64InjectionRe = regexp.MustCompile(`[A-Za-z0-9+/]{100,}={0,2}`)

// zeroWidthChars 定义需要检测的零宽字符列表。
// 这些字符不可见，可被用于在正常文本中隐藏恶意指令。
// 使用 Unicode 转义序列避免编译器对不可见字符的解析问题。
var zeroWidthChars = []rune{
	0x200B, // ZERO WIDTH SPACE
	0x200C, // ZERO WIDTH NON-JOINER
	0x200D, // ZERO WIDTH JOINER
	0xFEFF, // ZERO WIDTH NO-BREAK SPACE (BOM)
	0x2060, // WORD JOINER
}

// detectBase64Injection 检测内容中是否存在 base64 编码的注入攻击。
// 策略: 提取长 base64 序列，解码后用现有威胁模式重新扫描。
func detectBase64Injection(content string) []string {
	var threats []string
	matches := base64InjectionRe.FindAllString(content, -1)

	for _, match := range matches {
		decoded, err := base64.StdEncoding.DecodeString(match)
		if err != nil {
			// 尝试 RawStdEncoding (无填充)
			decoded, err = base64.RawStdEncoding.DecodeString(match)
			if err != nil {
				continue
			}
		}

		decodedStr := string(decoded)
		// 对解码后的内容执行完整的威胁模式扫描
		for _, tp := range contextThreatPatterns {
			if tp.re.MatchString(decodedStr) {
				threats = append(threats, "base64_encoded_"+tp.label)
			}
		}
	}

	return threats
}

// detectZeroWidthChars 检测内容中是否存在零宽字符。
// 零宽字符可用于在正常文本中隐藏不可见的恶意指令，
// 绕过基于正则的文本匹配检测。
func detectZeroWidthChars(content string) []string {
	for _, r := range content {
		for _, zw := range zeroWidthChars {
			if r == zw {
				return []string{"zero_width_char_detected"}
			}
		}
	}
	return nil
}

// scanContextContent 扫描上下文内容中是否存在 prompt injection 模式。
// 综合检测: 正则模式匹配 + base64 解码扫描 + 零宽字符检测。
// 返回发现的威胁标签列表。
func scanContextContent(content string) []string {
	var threats []string

	// ── 1. 正则模式匹配 (多语言) ──
	for _, tp := range contextThreatPatterns {
		if tp.re.MatchString(content) {
			threats = append(threats, tp.label)
		}
	}

	// ── 2. Base64 编码注入检测 ──
	base64Threats := detectBase64Injection(content)
	threats = append(threats, base64Threats...)

	// ── 3. 零宽字符检测 ──
	zwThreats := detectZeroWidthChars(content)
	threats = append(threats, zwThreats...)

	return threats
}

// sanitizeContextContent 清洗上下文内容，移除或转义潜在的 injection 模式。
// 返回清洗后的内容和发现的威胁列表。
func sanitizeContextContent(content string) (string, []string) {
	threats := scanContextContent(content)
	if len(threats) == 0 {
		return content, nil
	}

	// Remove malicious markers
	cleaned := content
	for _, tp := range contextThreatPatterns {
		cleaned = tp.re.ReplaceAllString(cleaned, "[已移除: "+tp.label+"]")
	}

	// Defense-in-depth: decode HTML entities and rescan for evaded patterns
	decoded := html.UnescapeString(cleaned)
	if decoded != cleaned {
		for _, tp := range contextThreatPatterns {
			decoded = tp.re.ReplaceAllString(decoded, "[已移除: "+tp.label+"]")
		}
		cleaned = decoded
	}

	return cleaned, threats
}

// loadContextFiles 按优先级搜索并读取上下文文件内容。
//
// 搜索策略:
//  1. 从当前工作目录开始向上搜索到根目录
//  2. 按 ContextFilePriority 定义的顺序查找
//  3. 收集所有找到的文件，按 content hash 去重
//  4. 单文件 4000 字符上限 + 总计 12000 字符预算
//
// 返回格式化后的上下文字符串 (含 Markdown 引用块)。
// 未找到任何文件时返回空字符串。
func (b *Builder) loadContextFiles() string {
	cwd, err := os.Getwd()
	if err != nil {
		slog.Warn("failed to get current working directory", "err", err)
		return ""
	}

	const maxFileChars = 4000
	const totalBudget = 12000
	seen := make(map[string]bool) // content hash → seen
	var results []string
	totalChars := 0

	for depth := 0; depth < 20; depth++ {
		for _, filename := range ContextFilePriority {
			path := filepath.Join(cwd, filename)
			content, err := os.ReadFile(path)
			if err != nil || len(content) == 0 {
				continue
			}

			// Hash 去重 (使用内容前 64 字节作为简易 hash)
			h := 简易Hash(content)
			if seen[h] {
				continue
			}
			seen[h] = true

			slog.Debug("loading context file", "path", path, "filename", filename)
			rawContent := string(content)

			// 安全扫描
			if threats := scanContextContent(rawContent); len(threats) > 0 {
				slog.Warn("context file contains potential prompt injection pattern",
					"path", path, "threats", strings.Join(threats, ", "))
				rawContent, _ = sanitizeContextContent(rawContent)
			}

			// 单文件截断
			if len(rawContent) > maxFileChars {
				rawContent = rawContent[:maxFileChars] + "\n...[truncated]..."
			}

			// 预算检查
			if totalChars+len(rawContent) > totalBudget {
				remaining := totalBudget - totalChars
				if remaining > 200 {
					rawContent = rawContent[:remaining] + "\n...[budget exceeded]..."
				} else {
					break
				}
			}

			results = append(results, formatContextFileContent(filename, rawContent))
			totalChars += len(rawContent)

			if totalChars >= totalBudget {
				break
			}
		}

		parent := filepath.Dir(cwd)
		if parent == cwd {
			break
		}
		cwd = parent

		if totalChars >= totalBudget {
			break
		}
	}

	if len(results) == 0 {
		return ""
	}
	return strings.Join(results, "")
}

// 简易Hash 返回内容的简易指纹用于去重。
func 简易Hash(data []byte) string {
	if len(data) < 64 {
		return string(data)
	}
	return string(data[:32]) + string(data[len(data)-32:])
}

const maxContextFileContentLen = 8000

// formatContextFileContent 将上下文文件内容格式化为系统提示词中的引用块。
// 使用 Markdown 格式包裹，便于模型识别文件边界。
// 超过 8000 字符的内容会被截断并添加提示。
func formatContextFileContent(filename, content string) string {
	if len(content) > maxContextFileContentLen {
		content = content[:maxContextFileContentLen] + "\n\n... 内容截断 ..."
	}
	return "\n\n## 上下文文件: " + filename + "\n\n```markdown\n" + content + "\n```\n"
}
