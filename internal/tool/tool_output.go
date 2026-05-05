// Package tool 提供工具输出限制功能。
// 控制工具返回结果的大小，防止过大输出导致上下文窗口溢出。
// 支持字节限制、行数限制和单行长度限制。
package tool

import (
	"strings"
)

// ───────────────────────────── 输出限制配置 ─────────────────────────────

// ToolOutputLimits 定义工具输出的大小限制。
// 所有字段使用默认值时:
//   - MaxBytes: 50000 字节
//   - MaxLines: 2000 行
//   - MaxLineLength: 2000 字符/行
type ToolOutputLimits struct {
	MaxBytes      int // 单次输出最大字节数
	MaxLines      int // 单次输出最大行数
	MaxLineLength int // 单行最大字符长度
}

// DefaultOutputLimits 返回默认的输出限制配置。
func DefaultOutputLimits() ToolOutputLimits {
	return ToolOutputLimits{
		MaxBytes:      50000,
		MaxLines:      2000,
		MaxLineLength: 2000,
	}
}

// ───────────────────────────── 输出限制函数 ─────────────────────────────

// LimitOutput 对工具输出内容应用所有限制。
// 处理顺序: 行长度截断 → 行数截断 → 字节截断。
// 任何截断操作都会在末尾附加截断提示信息。
func LimitOutput(content string, limits ToolOutputLimits) string {
	if content == "" {
		return content
	}

	// 应用默认值
	if limits.MaxLineLength <= 0 {
		limits.MaxLineLength = 2000
	}
	if limits.MaxLines <= 0 {
		limits.MaxLines = 2000
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 50000
	}

	// ── 第一步: 行长度截断 ──
	result := TruncateLineLength(content, limits.MaxLineLength)

	// ── 第二步: 行数截断 ──
	result = TruncateLines(result, limits.MaxLines)

	// ── 第三步: 字节截断 ──
	result = TruncateBytes(result, limits.MaxBytes)

	return result
}

// TruncateLines 将内容截断到指定的最大行数。
// 超出限制时在末尾附加截断提示。
func TruncateLines(content string, maxLines int) string {
	if maxLines <= 0 {
		return content
	}

	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content
	}

	truncated := lines[:maxLines]
	remaining := len(lines) - maxLines
	truncated = append(truncated, "", "...[内容已截断，剩余 "+itoa(remaining)+" 行]")

	return strings.Join(truncated, "\n")
}

// TruncateLineLength 将每行截断到指定的最大字符长度。
// 超出限制的行会被截断并附加省略标记。
func TruncateLineLength(content string, maxLen int) string {
	if maxLen <= 0 {
		return content
	}

	lines := strings.Split(content, "\n")
	truncated := false

	for i, line := range lines {
		if len(line) > maxLen {
			lines[i] = line[:maxLen] + "...[行已截断]"
			truncated = true
		}
	}

	if !truncated {
		return content
	}

	return strings.Join(lines, "\n")
}

// TruncateBytes 将内容截断到指定的最大字节数。
// 确保不会在多字节字符中间截断。
func TruncateBytes(content string, maxBytes int) string {
	if maxBytes <= 0 {
		return content
	}

	if len(content) <= maxBytes {
		return content
	}

	// 确保不在多字节 UTF-8 字符中间截断
	truncated := content[:maxBytes]

	// 回退到最近的完整 UTF-8 字符边界
	for len(truncated) > 0 && !isUTF8Start(truncated[len(truncated)-1]) {
		truncated = truncated[:len(truncated)-1]
	}

	return truncated + "\n...[内容已截断，原始大小: " + itoa(len(content)) + " 字节]"
}

// isUTF8Start 检查字节是否为 UTF-8 序列的起始字节。
// UTF-8 起始字节模式: 0xxxxxxx 或 11xxxxxx
// 续接字节模式: 10xxxxxx
func isUTF8Start(b byte) bool {
	return (b & 0xC0) != 0x80
}

// itoa 将整数转换为字符串，避免引入 fmt 包。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	negative := false
	if n < 0 {
		negative = true
		n = -n
	}

	var digits []byte
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}

	// 反转
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}

	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
