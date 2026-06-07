// Package context 提供上下文压缩中的工具输出修剪、边界对齐和工具对清理功能。
// prune 相关方法通过将旧的工具调用结果替换为简短摘要减少上下文体积；
// 边界对齐和工具对清理确保压缩后消息结构的正确性。
package context

import (
	"crypto/md5"
	"fmt"
	"log/slog"
	"strings"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── 工具输出修剪 ─────────────────────────────

// pruneOldToolResults 将旧工具消息替换为简短摘要。
//
// 修剪策略:
//  1. 去重: 大型工具结果 (>500 字符) 相同 hash 只保留最新版本，旧版替换为提示
//  2. 摘要化: 将旧工具消息替换为类似 "[terminal] ran `npm test` -> exit 0, 47 lines" 的摘要
//  3. 参数截断: 将旧助手消息中的大型工具调用参数截断到 500 字符
//
// protectTailCount 指定尾部保护消息数，这些消息不受修剪影响。
// protectTailTokens 指定尾部保护的 token 预算 (可选，优先于 protectTailCount)。
//
// 返回修剪后的消息列表和修剪计数。
func (c *Compressor) pruneOldToolResults(messages []llm.Message, protectTailCount int, protectTailTokens int) ([]llm.Message, int) {
	if len(messages) == 0 {
		return messages, 0
	}

	// 创建消息深拷贝副本
	result := make([]llm.Message, len(messages))
	copy(result, messages)
	for i := range result {
		if len(result[i].ToolCalls) > 0 {
			result[i].ToolCalls = make([]llm.ToolCall, len(messages[i].ToolCalls))
			copy(result[i].ToolCalls, messages[i].ToolCalls)
		}
	}
	pruned := 0

	// 构建 call_id → (tool_name, arguments) 索引
	callIDToTool := make(map[string][2]string)
	for _, msg := range result {
		if msg.Role == llm.RoleAssistant {
			for _, tc := range msg.ToolCalls {
				callIDToTool[tc.ID] = [2]string{tc.Name, tc.Arguments}
			}
		}
	}

	pruneBoundary := c.calcPruneBoundary(result, protectTailCount, protectTailTokens)

	// Pass 1: 去重大型工具结果 (>500 字符)
	pruneHashSeen := make(map[string]int)
	for i := len(result) - 1; i >= 0; i-- {
		msg := &result[i]
		if msg.Role != llm.RoleTool || len(msg.Content) < 500 {
			continue
		}
		h := fmt.Sprintf("%x", md5.Sum([]byte(msg.Content)))[:12]
		if existingIdx, exists := pruneHashSeen[h]; exists {
			result[i].Content = fmt.Sprintf("[重复工具输出 — 与最近调用 #%d 结果相同]", existingIdx)
			pruned++
		} else {
			pruneHashSeen[h] = i
		}
	}

	// Pass 2: 将边界之前的旧工具结果替换为摘要
	for i := 0; i < pruneBoundary && i < len(result); i++ {
		msg := &result[i]
		if msg.Role != llm.RoleTool || msg.Content == "" {
			continue
		}
		if strings.HasPrefix(msg.Content, "[重复工具输出") || len(msg.Content) <= 200 {
			continue
		}
		toolName := "unknown"
		toolArgs := ""
		if info, ok := callIDToTool[msg.ToolCallID]; ok {
			toolName = info[0]
			toolArgs = info[1]
		}
		result[i].Content = summarizeToolResult(toolName, toolArgs, msg.Content)
		pruned++
	}

	// Pass 3: 截断边界之前的助手消息中的大型工具调用参数
	for i := 0; i < pruneBoundary && i < len(result); i++ {
		msg := &result[i]
		if msg.Role != llm.RoleAssistant || len(msg.ToolCalls) == 0 {
			continue
		}
		modified := false
		newTCs := make([]llm.ToolCall, len(msg.ToolCalls))
		copy(newTCs, msg.ToolCalls)
		for j := range newTCs {
			if len(newTCs[j].Arguments) > 500 {
				truncated := truncateToolCallArgsJSON(newTCs[j].Arguments, 500)
				if truncated != newTCs[j].Arguments {
					newTCs[j].Arguments = truncated
					modified = true
				}
			}
		}
		if modified {
			result[i].ToolCalls = newTCs
		}
	}

	return result, pruned
}

// calcPruneBoundary 计算修剪边界 (不修剪尾部的消息)。
func (c *Compressor) calcPruneBoundary(messages []llm.Message, protectTailCount int, protectTailTokens int) int {
	const charsPerToken = 4

	if protectTailTokens > 0 && protectTailTokens < 1000000 {
		accumulated := 0
		minProtect := protectTailCount
		if minProtect > len(messages) {
			minProtect = len(messages)
		}
		boundary := len(messages)

		for i := len(messages) - 1; i >= 0; i-- {
			msgTokens := len(messages[i].Content)/charsPerToken + 10
			for _, tc := range messages[i].ToolCalls {
				msgTokens += len(tc.Arguments) / charsPerToken
			}
			if accumulated+msgTokens > protectTailTokens && (len(messages)-i) >= minProtect {
				boundary = i
				break
			}
			accumulated += msgTokens
			boundary = i
		}
		return boundary
	}

	if protectTailCount >= len(messages) {
		return 0
	}
	return len(messages) - protectTailCount
}

// ───────────────────────────── 边界对齐 ─────────────────────────────

// alignBoundaryForward 将压缩起始边界向前推移，跳过孤立的工具结果消息。
func (c *Compressor) alignBoundaryForward(messages []llm.Message, idx int) int {
	for idx < len(messages) && messages[idx].Role == llm.RoleTool {
		idx++
	}
	return idx
}

// alignBoundaryBackward 将压缩结束边界向后拉，避免分割 tool_call/result 组。
func (c *Compressor) alignBoundaryBackward(messages []llm.Message, idx int) int {
	if idx <= 0 || idx >= len(messages) {
		return idx
	}
	check := idx - 1
	for check >= 0 && messages[check].Role == llm.RoleTool {
		check--
	}
	if check >= 0 && messages[check].Role == llm.RoleAssistant && len(messages[check].ToolCalls) > 0 {
		idx = check
	}
	return idx
}

// findTailCutByTokens 从消息尾部向前累计 token，直到超出预算。
func (c *Compressor) findTailCutByTokens(messages []llm.Message, headEnd int, tokenBudget int) int {
	n := len(messages)
	minTail := 3
	if n-headEnd-1 < minTail {
		minTail = n - headEnd - 1
	}
	if minTail <= 0 {
		minTail = 0
	}

	softCeiling := int(float64(tokenBudget) * 1.5)
	accumulated := 0
	cutIdx := n

	for i := n - 1; i >= headEnd; i-- {
		msgTokens := len(messages[i].Content)/charsPerTokenEstimate + 10
		for _, tc := range messages[i].ToolCalls {
			msgTokens += len(tc.Arguments) / charsPerTokenEstimate
		}
		if accumulated+msgTokens > softCeiling && (n-i) >= minTail {
			break
		}
		accumulated += msgTokens
		cutIdx = i
	}

	if fallbackCut := n - minTail; cutIdx > fallbackCut {
		cutIdx = fallbackCut
	}
	if cutIdx <= headEnd {
		cutIdx = headEnd + 1
	}

	cutIdx = c.alignBoundaryBackward(messages, cutIdx)
	cutIdx = c.ensureLastUserMessageInTail(messages, cutIdx, headEnd)

	if cutIdx <= headEnd {
		cutIdx = headEnd + 1
	}
	return cutIdx
}

// ensureLastUserMessageInTail 确保最近的用户消息在受保护的尾部中。
func (c *Compressor) ensureLastUserMessageInTail(messages []llm.Message, cutIdx int, headEnd int) int {
	lastUserIdx := -1
	for i := len(messages) - 1; i >= headEnd; i-- {
		if messages[i].Role == llm.RoleUser {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx < 0 || lastUserIdx >= cutIdx {
		return cutIdx
	}
	slog.Debug("anchoring tail cut to final user message",
		"last_user_message_idx", lastUserIdx,
		"original_cut_idx", cutIdx,
	)
	if lastUserIdx > headEnd {
		return lastUserIdx
	}
	return cutIdx
}

// ───────────────────────────── 孤儿工具对清理 ─────────────────────────────

// sanitizeToolPairs 修复压缩后孤立的 tool_call / tool_result 配对。
func (c *Compressor) sanitizeToolPairs(messages []llm.Message) []llm.Message {
	survivingCallIDs := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role == llm.RoleAssistant {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					survivingCallIDs[tc.ID] = true
				}
			}
		}
	}

	resultCallIDs := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role == llm.RoleTool && msg.ToolCallID != "" {
			resultCallIDs[msg.ToolCallID] = true
		}
	}

	orphanedResults := make(map[string]bool)
	for cid := range resultCallIDs {
		if !survivingCallIDs[cid] {
			orphanedResults[cid] = true
		}
	}

	if len(orphanedResults) > 0 {
		filtered := make([]llm.Message, 0, len(messages))
		for _, msg := range messages {
			if msg.Role == llm.RoleTool && orphanedResults[msg.ToolCallID] {
				continue
			}
			filtered = append(filtered, msg)
		}
		messages = filtered
		slog.Info("compression cleanup: removed orphaned tool_result", "count", len(orphanedResults))
	}

	missingResults := make(map[string]bool)
	for cid := range survivingCallIDs {
		if !resultCallIDs[cid] {
			missingResults[cid] = true
		}
	}

	if len(missingResults) > 0 {
		patched := make([]llm.Message, 0, len(messages)+len(missingResults))
		for _, msg := range messages {
			patched = append(patched, msg)
			if msg.Role == llm.RoleAssistant {
				for _, tc := range msg.ToolCalls {
					if missingResults[tc.ID] {
						patched = append(patched, llm.Message{
							Role:       llm.RoleTool,
							Content:    "[之前对话的结果 — 见上方上下文总结]",
							ToolCallID: tc.ID,
						})
					}
				}
			}
		}
		messages = patched
		slog.Info("compression cleanup: added stub tool_result", "count", len(missingResults))
	}

	return messages
}
