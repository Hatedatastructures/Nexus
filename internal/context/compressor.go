// Package context 提供上下文压缩的编排逻辑。
// Compressor 在对话 token 用量超过阈值时自动触发，通过修剪旧工具输出、
// 保护头部和尾部消息、以及调用辅助 LLM 总结中间部分来实现压缩。
package context

import (
	"context"
	"fmt"
	"log/slog"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	// charsPerToken 粗略的字符数/token 估计值
	charsPerTokenEstimate = 4

	// summaryAntiThrashThreshold 反抖动阈值 — 两次无效压缩后跳过压缩
	summaryAntiThrashThreshold = 2
)

// ───────────────────────────── 公共方法 ─────────────────────────────

// ShouldCompress 根据当前 token 数判断是否需要压缩。
// 当 totalTokens > contextLimit * thresholdPercent 时返回 true。
// 包含反抖动保护: 连续两次无效压缩后跳过。
func (c *Compressor) ShouldCompress(contextLimit, totalTokens int) bool {
	// Anti-thrash: 如果连续两次压缩未显著减少 token，跳过
	if c.consecutiveSummaries >= summaryAntiThrashThreshold {
		slog.Debug("跳过压缩: 连续无效压缩",
			"consecutive", c.consecutiveSummaries,
		)
		return false
	}

	threshold := int(float64(contextLimit) * c.thresholdPercent)
	if totalTokens <= threshold {
		return false
	}
	return true
}

// recordCompressionResult 记录压缩结果，更新 anti-thrash 计数器。
// 如果压缩后 token 减少不足 15%，视为无效压缩，计数器加 1。
// 连续达到 summaryAntiThrashThreshold (2) 次无效压缩后，ShouldCompress 将跳过压缩。
// 当压缩有效 (减少 >= 15%) 时，重置计数器。
func (c *Compressor) recordCompressionResult(beforeTokens, afterTokens int) {
	if beforeTokens <= 0 {
		c.consecutiveSummaries = 0
		return
	}
	reduction := float64(beforeTokens-afterTokens) / float64(beforeTokens)
	if reduction < 0.15 {
		c.consecutiveSummaries++
		slog.Debug("无效压缩: token 减少不足",
			"reduction", reduction,
			"consecutive", c.consecutiveSummaries,
		)
	} else {
		c.consecutiveSummaries = 0
	}
}

// estimateImageTokens 估算图像内容占用的 token 数。
// 每张图像估算为 1600 token，用于多模态上下文的容量规划。
func estimateImageTokens(imageCount int) int {
	if imageCount <= 0 {
		return 0
	}
	return imageCount * 1600
}

// Compress 执行上下文压缩。
//
// 压缩流程:
//  1. 工具输出修剪 — 替换旧 tool 消息为 1 行摘要 (去重相同 hash)
//  2. 边界确定 — 头保护 N 条 + 尾保护 token 预算
//  3. 调用辅助 LLM 总结中间对话
//  4. 组装: head + summary + tail
//  5. 孤儿工具对清理 — 确保 tool_call / tool_result 配对正确
//
// 返回压缩后的消息列表。
func (c *Compressor) Compress(ctx context.Context, messages []llm.Message, auxProvider llm.Provider, focusTopic string) ([]llm.Message, error) {
	nMessages := len(messages)
	minForCompress := c.protectFirstN + 3 + 1
	if nMessages <= minForCompress {
		slog.Warn("消息太少无法压缩",
			"messages", nMessages,
			"min_for_compress", minForCompress,
		)
		return messages, nil
	}

	// 估算当前 token 数
	displayTokens := estimateTokensRough(messages)

	// Phase 1: 工具输出修剪 (廉价预处理，无 LLM 调用)
	prunedMessages, prunedCount := c.pruneOldToolResults(messages, 20, c.tailTokenBudget)
	if prunedCount > 0 {
		slog.Info("预压缩: 修剪了旧工具结果",
			"pruned_count", prunedCount,
		)
	}
	messages = prunedMessages

	// Phase 2: 确定边界
	compressStart := c.protectFirstN
	compressStart = c.alignBoundaryForward(messages, compressStart)

	// 使用 token 预算确定尾部边界
	compressEnd := c.findTailCutByTokens(messages, compressStart, c.tailTokenBudget)

	if compressStart >= compressEnd || compressEnd > len(messages) {
		// 没有需要压缩的中间部分
		return messages, nil
	}

	turnsToSummarize := messages[compressStart:compressEnd]

	slog.Info("上下文压缩触发",
		"tokens", displayTokens,
		"threshold", int(float64(c.tailTokenBudget)*c.thresholdPercent),
		"compress_start", compressStart,
		"compress_end", compressEnd,
		"turns_to_summarize", len(turnsToSummarize),
	)

	// Phase 3: 生成结构化总结
	summary := ""
	if auxProvider != nil {
		var err error
		summary, err = c.generateSummary(ctx, turnsToSummarize, auxProvider, "", focusTopic)
		if err != nil {
			// generateSummary 已返回降级总结，仅记录日志
			slog.Warn("总结生成使用了降级策略", "err", err)
		}
	}

	// Phase 4: 组装压缩后的消息列表
	compressed := make([]llm.Message, 0, compressStart+1+(len(messages)-compressEnd))

	// 头部消息
	for i := 0; i < compressStart; i++ {
		msg := messages[i]
		// 在系统消息中加入压缩提示
		if i == 0 && msg.Role == llm.RoleSystem {
			compressionNote := "\n\n[Note: 之前的对话回合已被压缩为交接总结以节省上下文空间。当前的会话状态可能仍反映之前的工作，请基于总结和当前状态继续，不要重复已完成的工作。]"
			if !containsText(msg.Content, "[Note:") {
				msg.Content = msg.Content + compressionNote
			}
		}
		compressed = append(compressed, msg)
	}

	// 注入总结 (generateSummary 在失败时已返回降级总结)
	if summary != "" {
		summaryMsg := llm.Message{
			Role:    c.pickSummaryRole(messages, compressStart, compressEnd),
			Content: summary,
		}
		compressed = append(compressed, summaryMsg)
	} else {
		// 无辅助 LLM 提供者时的最终降级
		nDropped := compressEnd - compressStart
		fallbackSummary := fmt.Sprintf(
			"%s\n总结生成不可用。%d 条消息已被移除以释放上下文空间，但无法总结。"+
				"基于下方的最近消息和文件/资源的当前状态继续。",
			SummaryPrefix, nDropped,
		)
		compressed = append(compressed, llm.Message{
			Role:    llm.RoleUser,
			Content: fallbackSummary,
		})
	}

	// 尾部消息
	for i := compressEnd; i < len(messages); i++ {
		compressed = append(compressed, messages[i])
	}

	// Phase 5: 孤儿工具对清理
	compressed = c.sanitizeToolPairs(compressed)

	// 更新 anti-thrash 计数器
	c.recordCompressionResult(displayTokens, estimateTokensRough(compressed))

	slog.Info("压缩完成",
		"original_messages", nMessages,
		"compressed_messages", len(compressed),
	)

	return compressed, nil
}

// ───────────────────────────── 边界对齐 ─────────────────────────────

// alignBoundaryForward 将压缩起始边界向前推移，跳过孤立的工具结果消息。
// 确保压缩从非 tool 消息开始，避免分割 tool_call/result 组。
func (c *Compressor) alignBoundaryForward(messages []llm.Message, idx int) int {
	for idx < len(messages) && messages[idx].Role == llm.RoleTool {
		idx++
	}
	return idx
}

// alignBoundaryBackward 将压缩结束边界向后拉，避免分割 tool_call/result 组。
//
// 如果边界落在 tool 消息组中间，向后移到对应的助手消息，
// 确保整个 assistant + tool_results 组一起被压缩。
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
// 返回尾部起始索引。始终保留头 + 至少 3 条尾部消息。
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

	// 至少保留 minTail 条消息
	fallbackCut := n - minTail
	if cutIdx > fallbackCut {
		cutIdx = fallbackCut
	}

	// 如果 token 预算能保护所有内容 (对话很小)，强制在头部之后切割
	if cutIdx <= headEnd {
		cutIdx = headEnd + 1
	}

	// 对齐以避免分割工具组
	cutIdx = c.alignBoundaryBackward(messages, cutIdx)

	// 确保最近用户消息在尾部
	cutIdx = c.ensureLastUserMessageInTail(messages, cutIdx, headEnd)

	if cutIdx <= headEnd {
		cutIdx = headEnd + 1
	}
	return cutIdx
}

// ensureLastUserMessageInTail 确保最近的用户消息在受保护的尾部中。
// 如果最终用户消息因边界对齐被移到压缩区域，向后拉边界。
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

	// 最终用户消息在压缩区域中 — 将其拉入尾部
	slog.Debug("锚定尾部切割到最终用户消息",
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
//
// 两个故障模式:
//  1. tool_result 引用的 call_id 对应的 assistant tool_call 已删除 → 移除该 result
//  2. assistant message 有 tool_calls 但结果被删除 → 插入存根 result
func (c *Compressor) sanitizeToolPairs(messages []llm.Message) []llm.Message {
	// 收集所有幸存的 tool_call ID
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

	// 收集所有 tool_result 的 call_id
	resultCallIDs := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role == llm.RoleTool {
			if msg.ToolCallID != "" {
				resultCallIDs[msg.ToolCallID] = true
			}
		}
	}

	// 1. 移除孤立的 tool_result (没有对应的 tool_call)
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
		slog.Info("压缩清理: 移除了孤立的 tool_result",
			"count", len(orphanedResults),
		)
	}

	// 2. 为孤立的 tool_call 添加存根 result
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
		slog.Info("压缩清理: 添加了存根 tool_result",
			"count", len(missingResults),
		)
	}

	return messages
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// pickSummaryRole 为总结消息选择角色，避免连续相同角色。
func (c *Compressor) pickSummaryRole(messages []llm.Message, compressStart, compressEnd int) llm.MessageRole {
	if compressStart <= 0 {
		return llm.RoleUser
	}
	lastHeadRole := messages[compressStart-1].Role
	firstTailRole := llm.RoleUser
	if compressEnd < len(messages) {
		firstTailRole = messages[compressEnd].Role
	}

	// 优先避免与头部冲突
	if lastHeadRole == llm.RoleAssistant || lastHeadRole == llm.RoleTool {
		return llm.RoleUser
	}

	// 如果选择 assistant 与尾部相同，则选择 user (如果与头部不冲突)
	if llm.RoleAssistant == firstTailRole {
		return llm.RoleUser
	}

	return llm.RoleAssistant
}

// estimateTokensRough 粗略估算消息列表的 token 数。
func estimateTokensRough(messages []llm.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content)/charsPerTokenEstimate + 10
		for _, tc := range msg.ToolCalls {
			total += len(tc.Arguments) / charsPerTokenEstimate
		}
	}
	return total
}

// containsText 检查字符串 s 中是否包含子串 substr。
func containsText(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

// searchSubstring 简单子串搜索。
func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
