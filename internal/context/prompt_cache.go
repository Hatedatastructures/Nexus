// Package context 提供 Anthropic 提示缓存控制。
// ApplyAnthropicCacheControl 在消息列表中插入 cache_control 断点，
// 以利用 Anthropic 的 Prompt Caching 功能减少多轮对话的输入 token 成本。
package context

import (
	"nexus-agent/internal/llm"
)

// ApplyAnthropicCacheControl 在指定消息上设置 cache_control: {type: "ephemeral"} 断点。
//
// 缓存策略:
//   - 系统提示词 (第一条 system 消息) —— 始终缓存，跨轮复用
//   - 最后 cachePoints 条消息 —— 缓存近期上下文
//
// 布局兼容 Anthropic Messages API、OpenRouter 及第三方 Anthropic 兼容网关。
// cachePoints 控制缓存末尾消息的数量，典型值为 3。
// 返回修改后的消息切片 (同一个底层数组，已原地修改)。
func ApplyAnthropicCacheControl(messages []llm.Message, cachePoints int) []llm.Message {
	if len(messages) == 0 || cachePoints <= 0 {
		return messages
	}

	// 系统提示词始终缓存 — 在 system 消息的 Content 后附加 cache_control 标记
	for i := range messages {
		if messages[i].Role == llm.RoleSystem {
			messages[i] = withCacheControl(messages[i])
			break
		}
	}

	// 倒数 cachePoints 条消息 (非 system) 设置缓存
	cached := 0
	for i := len(messages) - 1; i >= 0 && cached < cachePoints; i-- {
		if messages[i].Role == llm.RoleSystem {
			continue
		}
		messages[i] = withCacheControl(messages[i])
		cached++
	}

	return messages
}

// withCacheControl 在消息的 Content 后附加 Anthropic cache_control 标记。
// 该标记会被 Anthropic transport 层识别并在构建请求时转换为正确的格式。
func withCacheControl(msg llm.Message) llm.Message {
	// 对于有工具调用的 assistant 消息，在最后一个 ToolCall 上标记
	if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0 {
		msg.ToolCalls[len(msg.ToolCalls)-1].Extra = map[string]any{
			"cache_control": map[string]string{"type": "ephemeral"},
		}
		return msg
	}

	// 对于 tool 消息，在 ToolCallID 对应的内容上标记
	if msg.Role == llm.RoleTool && msg.ToolCallID != "" {
		if msg.Name == "" {
			msg.Name = "tool"
		}
		return msg
	}

	// 对于 system/user/assistant 纯文本消息，在 Content 后附加标记
	// transport 层会识别此标记并转换为正确的 cache_control 格式
	if msg.Content != "" {
		msg.Content = msg.Content + "\n[cache_control:ephemeral]"
	}
	return msg
}
