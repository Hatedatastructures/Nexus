package llm

import "strings"

// sanitizeAnthropicID 清理工具调用 ID，确保符合 Anthropic 的 [a-zA-Z0-9_-] 格式。
func sanitizeAnthropicID(id string) string {
	if id == "" {
		return "tool_0"
	}
	var result strings.Builder
	for _, c := range id {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			result.WriteRune(c)
		} else {
			result.WriteRune('_')
		}
	}
	sanitized := result.String()
	if sanitized == "" {
		return "tool_0"
	}
	return sanitized
}

// stripOrphanedToolBlocks 移除没有配对 tool_result 的 tool_use block，
// 以及没有前序 tool_use 的 tool_result block。
func stripOrphanedToolBlocks(msgs []Message) []Message {
	usedIDs := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Role == RoleAssistant {
			for _, tc := range msg.ToolCalls {
				usedIDs[tc.ID] = true
			}
		}
	}

	resultIDs := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Role == RoleTool {
			resultIDs[msg.ToolCallID] = true
		}
	}

	filtered := make([]Message, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Role == RoleAssistant && len(msg.ToolCalls) > 0 {
			kept := make([]ToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				if resultIDs[tc.ID] {
					kept = append(kept, tc)
				}
			}
			msg.ToolCalls = kept
		}
		if msg.Role == RoleTool {
			if !usedIDs[msg.ToolCallID] {
				continue
			}
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

// mergeConsecutiveRoles 合并连续相同 role 的消息。
func mergeConsecutiveRoles(msgs []Message) []Message {
	if len(msgs) <= 1 {
		return msgs
	}
	result := make([]Message, 0, len(msgs))
	result = append(result, msgs[0])
	for i := 1; i < len(msgs); i++ {
		last := &result[len(result)-1]
		curr := msgs[i]
		if last.Role == curr.Role {
			if curr.Content != "" {
				if last.Content != "" {
					last.Content += "\n" + curr.Content
				} else {
					last.Content = curr.Content
				}
			}
			if len(curr.ToolCalls) > 0 {
				merged := make([]ToolCall, len(last.ToolCalls)+len(curr.ToolCalls))
				copy(merged, last.ToolCalls)
				copy(merged[len(last.ToolCalls):], curr.ToolCalls)
				last.ToolCalls = merged
			}
		} else {
			result = append(result, curr)
		}
	}
	return result
}

// evictOldScreenshots 只保留最近 maxImages 张图片。
func evictOldScreenshots(msgs []Message, maxImages int) []Message {
	if maxImages <= 0 {
		return msgs
	}
	imageCount := 0
	for _, msg := range msgs {
		if msg.Role == RoleUser && containsImageMarker(msg.Content) {
			imageCount++
		}
	}
	if imageCount <= maxImages {
		return msgs
	}
	imagesToRemove := imageCount - maxImages
	result := make([]Message, 0, len(msgs))
	seen := 0
	for _, msg := range msgs {
		if msg.Role == RoleUser && containsImageMarker(msg.Content) {
			seen++
			if seen <= imagesToRemove {
				continue
			}
		}
		result = append(result, msg)
	}
	return result
}

func containsImageMarker(content string) bool {
	return strings.Contains(content, "[image") || strings.Contains(content, "![image") || strings.Contains(content, "data:image/")
}
