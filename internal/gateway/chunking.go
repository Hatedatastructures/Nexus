// Package gateway 提供 UTF-16 感知的消息分片功能。
// 用于将长消息拆分为符合平台限制的多个片段。
package gateway

import (
	"fmt"
	"strings"
	"unicode/utf16"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	defaultMaxChunkLen = 4000  // 默认最大 chunk 长度 (UTF-16 code units)
	chunkHeaderFmt     = "[%d/%d] " // chunk 头格式
)

// ───────────────────────────── 分片函数 ─────────────────────────────

// ChunkMessage 将消息按 UTF-16 长度分片。
// 每个 chunk 不超过 maxLen 个 UTF-16 code units。
// 在 chunk 间添加序号标记。
func ChunkMessage(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = defaultMaxChunkLen
	}

	// 计算 UTF-16 长度
	encoded := utf16.Encode([]rune(text))
	if len(encoded) <= maxLen {
		return []string{text}
	}

	var chunks []string
	remaining := text
	chunkNum := 1

	// 预估总 chunk 数
	totalChunks := (len(encoded) + maxLen - 1) / maxLen

	runes := []rune(remaining)
	pos := 0

	for pos < len(runes) {
		// 计算 header 长度
		header := fmt.Sprintf(chunkHeaderFmt, chunkNum, totalChunks)
		headerLen := len(utf16.Encode([]rune(header)))

		// 可用内容长度 (UTF-16 code units)
		contentLen := maxLen - headerLen
		if contentLen <= 0 {
			contentLen = maxLen
		}

		// 逐 rune 累加 UTF-16 长度，找到不超出 contentLen 的最大 rune 数。
		// 原实现直接用 contentLen 作为 rune 数量，混淆了 UTF-16 code units
		// 与 rune 的概念，包含 emoji 等多码元字符时会超出限制。
		usedUTF16 := 0
		availRunes := 0
		for i := pos; i < len(runes); i++ {
			runeUTF16Len := len(utf16.Encode([]rune{runes[i]}))
			if usedUTF16+runeUTF16Len > contentLen {
				break
			}
			usedUTF16 += runeUTF16Len
			availRunes++
		}
		if availRunes == 0 {
			availRunes = 1 // 至少取一个 rune，避免无限循环
		}

		// 按 UTF-16 边界截取
		chunkText := string(runes[pos : pos+availRunes])
		if chunkText == "" {
			break
		}

		chunks = append(chunks, header+chunkText)
		pos += availRunes
		chunkNum++
	}

	return chunks
}

// ChunkMessageSimple 将消息按字符长度分片（不考虑 UTF-16）。
// 用于不严格要求 UTF-16 的平台。
func ChunkMessageSimple(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = defaultMaxChunkLen
	}

	if len([]rune(text)) <= maxLen {
		return []string{text}
	}

	var chunks []string
	runes := []rune(text)
	totalChunks := (len(runes) + maxLen - 1) / maxLen

	for i := 0; i < len(runes); i += maxLen {
		end := i + maxLen
		if end > len(runes) {
			end = len(runes)
		}

		chunkNum := i/maxLen + 1
		header := fmt.Sprintf(chunkHeaderFmt, chunkNum, totalChunks)
		chunks = append(chunks, header+string(runes[i:end]))
	}

	return chunks
}

// ───────────────────────────── UTF-16 截取 ─────────────────────────────

// 截取UTF16 按 UTF-16 code unit 数量截取字符串。
func 截取UTF16(s string, maxUnits int) string {
	if maxUnits <= 0 {
		return ""
	}

	encoded := utf16.Encode([]rune(s))
	if len(encoded) <= maxUnits {
		return s
	}

	// 截取并解码回 UTF-8
	truncated := encoded[:maxUnits]
	runes := utf16.Decode(truncated)
	return string(runes)
}

// UTF16Len 计算字符串的 UTF-16 code unit 数量。
func UTF16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// ───────────────────────────── 消息截断 ─────────────────────────────

// TruncateMessage 截断消息到指定 UTF-16 长度，并添加省略标记。
func TruncateMessage(text string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = defaultMaxChunkLen
	}

	encoded := utf16.Encode([]rune(text))
	if len(encoded) <= maxLen {
		return text
	}

	// 为省略标记预留空间
	suffix := "\n...[消息已截断]"
	suffixLen := UTF16Len(suffix)
	availableLen := maxLen - suffixLen

	if availableLen <= 0 {
		return suffix
	}

	truncated := 截取UTF16(text, availableLen)
	return truncated + suffix
}

// SplitAtNewlines 在换行符处分片，尽量保持完整行。
func SplitAtNewlines(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = defaultMaxChunkLen
	}

	lines := strings.Split(text, "\n")
	var chunks []string
	var current strings.Builder

	for _, line := range lines {
		lineLen := UTF16Len(line)
		currentLen := UTF16Len(current.String())

		// 如果当前行本身就超过限制，需要单独分片
		if lineLen > maxLen {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
			// 对超长行进行硬分片
			subChunks := ChunkMessage(line, maxLen)
			chunks = append(chunks, subChunks...)
			continue
		}

		// 如果加上当前行会超过限制
		if currentLen+lineLen+1 > maxLen {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
		}

		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(line)
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	return chunks
}
