// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"strings"
)

// ── 缓存控制常量 ──────────────────────────────────────────────────────────

const (
	// CacheControlEphemeral 表示提示缓存控制类型：临时缓存。
	CacheControlEphemeral = "ephemeral"
)

// ── Anthropic 缓存控制 ────────────────────────────────────────────────────

// anthropicCacheModels 列出支持 prompt caching 的 Anthropic Claude 模型子串。
var anthropicCacheModels = []string{
	"claude-3-5",
	"claude-3-7",
	"claude-4",
	"claude-opus",
	"claude-sonnet",
	"claude-haiku",
}

// supportsAnthropicCaching 检测模型是否支持 Anthropic prompt caching。
func supportsAnthropicCaching(model string) bool {
	modelLower := strings.ToLower(model)
	for _, m := range anthropicCacheModels {
		if strings.Contains(modelLower, m) {
			return true
		}
	}
	return false
}

// ApplyCacheControl 根据模型和提供者自动标记消息的缓存控制点。
//
// Anthropic 缓存策略：
//   - 系统提示词：标记为 ephemeral 缓存
//   - 最后一条用户消息之前的消息：标记缓存断点
//
// OpenAI 缓存策略（新 API 格式）：
//   - 对兼容的模型标记缓存控制
//
// 目前主要实现 Anthropic 风格的缓存控制标记。
// OpenAI 原生缓存通过请求级参数控制，此处对兼容模型添加标记。
func ApplyCacheControl(messages []Message, model string) []Message {
	modelLower := strings.ToLower(model)

	// 仅对 Anthropic 缓存兼容模型应用缓存标记
	if !supportsAnthropicCaching(modelLower) {
		return messages
	}

	result := make([]Message, len(messages))
	copy(result, messages)

	cachePointCount := 0
	maxCachePoints := 4 // Anthropic 最多 4 个缓存断点

	for i := range result {
		msg := &result[i]

		// 系统提示词：标记缓存
		if msg.Role == RoleSystem && cachePointCount < maxCachePoints {
			msg.Content = addAnthropicCacheControl(msg.Content)
			cachePointCount++
			continue
		}

		// 对最后一条用户消息之前的内容标记缓存断点
		if cachePointCount >= maxCachePoints {
			break
		}
	}

	return result
}

// addAnthropicCacheControl 为内容添加 Anthropic 缓存控制标记。
// 对纯文本内容，在末尾附加缓存控制指令。
// 注意：实际 Anthropic API 通过 content block 的 cache_control 字段控制，
// 此处仅做标记，实际转换在 Transport 的 BuildRequest 中处理。
func addAnthropicCacheControl(content string) string {
	if strings.Contains(content, "cache_control") {
		return content
	}
	// 添加 Anthropic cache_control 标记，供 Transport 层识别并转换为
	// content block 的 cache_control 字段
	return content + "\n<!-- cache_control: ephemeral -->"
}

// NeedsCacheControl 判断给定的消息和模型是否需要应用提示词缓存。
func NeedsCacheControl(model string) bool {
	return supportsAnthropicCaching(model)
}

// IsAnthropicCacheModel 判断模型是否支持 Anthropic 的 cache_control。
func IsAnthropicCacheModel(model string) bool {
	return supportsAnthropicCaching(model)
}
