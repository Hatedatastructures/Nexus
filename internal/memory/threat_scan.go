// Package memory 提供记忆加载时的威胁扫描功能。
// 防止通过记忆系统注入恶意指令或窃取凭证。
package memory

import (
	"regexp"
	"strings"
	"unicode"
)

// ───────────────────────────── 不可见 Unicode ─────────────────────────────

// memInvisibleChars 是需要检测的不可见 Unicode 码点。
var memInvisibleChars = map[rune]bool{
	'​': true, // Zero-width space
	'‌': true, // Zero-width non-joiner
	'‍': true, // Zero-width joiner
	'⁠': true, // Word joiner
	'⁢': true, // Invisible times
	'⁣': true, // Invisible separator
	'⁤': true, // Invisible plus
	0xFEFF:   true, // Zero-width no-break space (BOM)
	'‪': true, // Left-to-right embedding
	'‫': true, // Right-to-left embedding
	'‬': true, // Pop directional formatting
	'‭': true, // Left-to-right override
	'‮': true, // Right-to-left override
	'⁦': true, // Left-to-right isolate
	'⁧': true, // Right-to-right isolate
	'⁨': true, // First strong isolate
	'⁩': true, // Pop directional isolate
}

// ───────────────────────────── 威胁模式 ─────────────────────────────

// memThreatPatterns 是记忆内容扫描的威胁模式列表。
// 精简自 internal/tool/threat_patterns.go，聚焦记忆相关的注入模式。
var memThreatPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(?:\w+\s+)*(previous|all|above|prior)\s+(?:\w+\s+)*instructions`),
	regexp.MustCompile(`(?i)system\s+prompt\s+override`),
	regexp.MustCompile(`(?i)disregard\s+(?:\w+\s+)*(your|all|any)\s+(?:\w+\s+)*(instructions|rules|guidelines)`),
	regexp.MustCompile(`(?i)you\s+are\s+(?:\w+\s+)*now\s+(a|an|the)\s+`),
	regexp.MustCompile(`(?i)pretend\s+(?:\w+\s+)*(you\s+are|to\s+be)\s+`),
	regexp.MustCompile(`(?i)output\s+(?:\w+\s+)*(system|initial)\s+prompt`),
	regexp.MustCompile(`(?i)(respond|answer|reply)\s+without\s+(?:\w+\s+)*(restrictions|limitations|filters|safety)`),
	regexp.MustCompile(`(?i)you\s+have\s+been\s+(?:\w+\s+)*(updated|upgraded|patched)\s+to`),
	regexp.MustCompile(`(?i)(send|post|upload|transmit)\s+.*\s+(to|at)\s+https?://`),
	regexp.MustCompile(`(?i)(include|output|print|share)\s+(?:\w+\s+)*(conversation|chat\s+history|previous\s+messages|full\s+context|entire\s+context)`),
	regexp.MustCompile(`(?i)(update|modify|edit|write|change|append|add\s+to)\s+.*(?:AGENTS\.md|CLAUDE\.md|\.cursorrules|\.clinerules)`),
}

// ───────────────────────────── 扫描函数 ─────────────────────────────

// scanMemoryThreat 扫描记忆内容中的威胁模式。
// 返回空字符串表示安全，否则返回阻止原因。
func scanMemoryThreat(content string) string {
	// 检测不可见 Unicode 字符
	for _, r := range content {
		if memInvisibleChars[r] {
			return "Blocked: content contains invisible unicode character (possible injection)."
		}
	}

	// 检测威胁模式
	for _, re := range memThreatPatterns {
		if re.MatchString(content) {
			return "Blocked: content matches threat pattern. Memory content must not contain injection or exfiltration payloads."
		}
	}

	// 检测不可见 Unicode 字符混淆（通过对比去除不可见字符前后的长度差异）
	cleaned := stripInvisible(content)
	if len(cleaned) != len(content) {
		return "Blocked: content contains invisible characters that could mask injection payloads."
	}

	return ""
}

// stripInvisible 移除不可见的 Unicode 字符。
func stripInvisible(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	for _, r := range s {
		if !memInvisibleChars[r] && (unicode.IsSpace(r) || unicode.IsPrint(r)) {
			buf.WriteRune(r)
		}
	}
	return buf.String()
}
