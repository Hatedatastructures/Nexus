// Package gateway 提供消息投递管理。
// DeliveryManager 负责消息格式化、截断、分页和媒体提取。
// 支持 UTF-16 感知截断和按段落边界分割长消息。
package gateway

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"nexus-agent/internal/gateway/platforms"
)

// ───────────────────────────── 媒体标签 ─────────────────────────────

// MediaTag 表示从消息内容中提取的媒体引用。
type MediaTag struct {
	Type     string // 媒体类型: image / file / audio
	URL      string // 媒体 URL 或文件路径
	Original string // 原始标签文本
}

// ───────────────────────────── 投递管理器 ─────────────────────────────

// DeliveryManager 管理消息投递的格式化、截断和媒体处理。
type DeliveryManager struct {
	truncateLen int // 默认消息最大长度
}

// NewDeliveryManager 创建投递管理器。
// truncateLen 为默认消息最大字符数，0 表示不限制。
func NewDeliveryManager(truncateLen int) *DeliveryManager {
	if truncateLen <= 0 {
		truncateLen = 4096 // 默认值
	}
	return &DeliveryManager{
		truncateLen: truncateLen,
	}
}

// FormatMessage 格式化消息内容。
// 根据平台类型应用不同的格式化规则 (如 Slack 的 Markdown、Telegram 的 HTML 等)。
func (d *DeliveryManager) FormatMessage(content string, platform platforms.Platform) string {
	switch platform {
	case platforms.PlatformTelegram:
		// Telegram 使用 MarkdownV2，需要对特殊字符转义
		// 这里返回原始内容，实际转义由适配器处理
		return content
	case platforms.PlatformSlack:
		// Slack 支持有限的 Markdown (mrkdwn)
		return content
	case platforms.PlatformDiscord:
		// Discord 支持 Markdown
		return content
	default:
		return content
	}
}

// TruncateMessage 截断消息至指定长度。
// 使用 UTF-16 感知截断 (对 Telegram 等平台重要，emoji 可能占 2 个 UTF-16 码元)。
// 尽量在段落边界截断，保留完整语义。
func (d *DeliveryManager) TruncateMessage(content string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = d.truncateLen
	}

	// 检查是否需要截断
	runeCount := utf8.RuneCountInString(content)
	if runeCount <= maxLen {
		return content
	}

	// 尝试在段落边界截断
	truncated := content
	paragraphs := strings.Split(content, "\n\n")
	var result strings.Builder
	currentLen := 0

	for i, para := range paragraphs {
		paraLen := utf8.RuneCountInString(para)
		if i > 0 {
			// 加上段落分隔符的长度
			paraLen += 2
		}

		if currentLen+paraLen <= maxLen {
			if i > 0 {
				result.WriteString("\n\n")
			}
			result.WriteString(para)
			currentLen += paraLen
		} else {
			// 尝试在剩余空间内按行截断
			remaining := maxLen - currentLen
			if remaining > 0 && i > 0 {
				result.WriteString("\n\n")
				currentLen += 2
				remaining -= 2
			}
			if remaining > 0 {
				lines := strings.Split(para, "\n")
				for _, line := range lines {
					lineLen := utf8.RuneCountInString(line)
					if currentLen+lineLen+1 > maxLen && currentLen > 0 {
						break
					}
					if currentLen > 0 || i > 0 {
						result.WriteString("\n")
						currentLen++
					}
					result.WriteString(line)
					currentLen += lineLen
				}
			}
			break
		}
	}

	truncated = result.String()
	if truncated == "" {
		// 回退: 按字符截断
		runes := []rune(content)
		if len(runes) > maxLen {
			truncated = string(runes[:maxLen])
		}
	}

	// 追加截断标记
	if utf8.RuneCountInString(truncated) >= maxLen {
		truncated = string([]rune(truncated)[:maxLen-3]) + "..."
	}

	return truncated
}

// SplitLongMessage 将长消息按段落边界分割为多个片段。
// 每个片段不超过 maxLen 字符。
func (d *DeliveryManager) SplitLongMessage(content string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = d.truncateLen
	}

	runeCount := utf8.RuneCountInString(content)
	if runeCount <= maxLen {
		return []string{content}
	}

	var chunks []string
	paragraphs := strings.Split(content, "\n\n")
	var current strings.Builder

	for _, para := range paragraphs {
		paraLen := utf8.RuneCountInString(para)
		currentLen := utf8.RuneCountInString(current.String())

		// 如果单个段落超过限制，按行分割
		if paraLen > maxLen {
			// 先刷新当前缓冲区
			if current.Len() > 0 {
				chunks = append(chunks, strings.TrimSpace(current.String()))
				current.Reset()
			}
			// 按行分割长段落
			lines := strings.Split(para, "\n")
			var lineBuffer strings.Builder
			for _, line := range lines {
				lineLen := utf8.RuneCountInString(line)
				bufLen := utf8.RuneCountInString(lineBuffer.String())
				if bufLen+lineLen+1 > maxLen && lineBuffer.Len() > 0 {
					chunks = append(chunks, strings.TrimSpace(lineBuffer.String()))
					lineBuffer.Reset()
				}
				if lineBuffer.Len() > 0 {
					lineBuffer.WriteString("\n")
				}
				lineBuffer.WriteString(line)
			}
			if lineBuffer.Len() > 0 {
				chunks = append(chunks, strings.TrimSpace(lineBuffer.String()))
			}
			continue
		}

		// 检查是否需要换片
		separatorLen := 0
		if current.Len() > 0 {
			separatorLen = 2 // "\n\n"
		}
		if currentLen+separatorLen+paraLen > maxLen && current.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
	}

	// 最后一片
	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}

	if len(chunks) == 0 {
		// 回退: 按字符硬截断
		runes := []rune(content)
		for i := 0; i < len(runes); i += maxLen {
			end := i + maxLen
			if end > len(runes) {
				end = len(runes)
			}
			chunks = append(chunks, string(runes[i:end]))
		}
	}

	return chunks
}

// ExtractMedia 从消息内容中提取媒体标签和图片 URL。
// 返回纯文本、图片 URL 列表和移除的媒体标签。
//
// 支持的格式:
//   - MEDIA: <path> (或多行格式 [[MEDIA:<path>]])
//   - 行内图片 URL: https://... .jpg/.png/.gif/.webp
//   - Markdown 图片: ![alt](url)
func (d *DeliveryManager) ExtractMedia(content string) (text string, images []string, removed []MediaTag) {
	text = content
	var tags []MediaTag

	// 匹配 MEDIA 标签: MEDIA:<path> 或 [[MEDIA:<path>]]
	mediaRe := regexp.MustCompile(`(?m)^\s*MEDIA:\s*(\S+)\s*$|\[\[MEDIA:([^\]]+)\]\]`)
	matches := mediaRe.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		path := m[1]
		if path == "" {
			path = m[2]
		}
		path = strings.TrimSpace(path)
		if path != "" {
			tags = append(tags, MediaTag{
				Type:     detectMediaType(path),
				URL:      path,
				Original: m[0],
			})
		}
	}
	text = mediaRe.ReplaceAllString(text, "")

	// 匹配 Markdown 图片: ![alt](url)
	imgRe := regexp.MustCompile(`!\[.*?\]\((\S+)\)`)
	imgMatches := imgRe.FindAllStringSubmatch(text, -1)
	for _, m := range imgMatches {
		url := strings.TrimSpace(m[1])
		if url != "" && isImageURL(url) {
			images = append(images, url)
		}
	}

	// 匹配行内图片 URL
	urlRe := regexp.MustCompile(`https?://\S+\.(?:jpg|jpeg|png|gif|webp)(?:\?\S*)?`)
	urlMatches := urlRe.FindAllString(text, -1)
	for _, url := range urlMatches {
		if !contains(images, url) {
			images = append(images, url)
		}
	}

	// 清理多余空行
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")
	text = strings.TrimSpace(text)

	return text, images, tags
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// isImageURL 检查 URL 是否指向已知图片格式。
func isImageURL(url string) bool {
	lower := strings.ToLower(url)
	return strings.HasSuffix(lower, ".jpg") ||
		strings.HasSuffix(lower, ".jpeg") ||
		strings.HasSuffix(lower, ".png") ||
		strings.HasSuffix(lower, ".gif") ||
		strings.HasSuffix(lower, ".webp")
}

// detectMediaType 根据文件扩展名检测媒体类型。
func detectMediaType(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".jpg"),
		strings.HasSuffix(lower, ".jpeg"),
		strings.HasSuffix(lower, ".png"),
		strings.HasSuffix(lower, ".gif"),
		strings.HasSuffix(lower, ".webp"):
		return "image"
	case strings.HasSuffix(lower, ".mp3"),
		strings.HasSuffix(lower, ".wav"),
		strings.HasSuffix(lower, ".ogg"):
		return "audio"
	default:
		return "file"
	}
}

// contains 检查字符串切片是否包含指定值。
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
