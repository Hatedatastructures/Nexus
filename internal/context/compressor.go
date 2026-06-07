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
func (c *Compressor) ShouldCompress(contextLimit, totalTokens int) bool {
	if c.antiThrashCooldown > 0 {
		c.antiThrashCooldown--
		slog.Debug("compression cooldown ticking", "remaining", c.antiThrashCooldown)
		return false
	}

	if c.consecutiveSummaries >= summaryAntiThrashThreshold {
		c.consecutiveSummaries = 0
		c.antiThrashCooldown = 3
		slog.Debug("entering compression cooldown", "cooldown", 3)
		return false
	}

	threshold := int(float64(contextLimit) * c.thresholdPercent)
	return totalTokens > threshold
}

// recordCompressionResult 记录压缩结果，更新 anti-thrash 计数器。
func (c *Compressor) recordCompressionResult(beforeTokens, afterTokens int) {
	if beforeTokens <= 0 {
		c.consecutiveSummaries = 0
		return
	}
	reduction := float64(beforeTokens-afterTokens) / float64(beforeTokens)
	if reduction < 0.15 {
		c.consecutiveSummaries++
		slog.Debug("ineffective compression: insufficient token reduction",
			"reduction", reduction,
			"consecutive", c.consecutiveSummaries,
		)
	} else {
		c.consecutiveSummaries = 0
	}
}

// estimateImageTokens 估算图像内容占用的 token 数。
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
func (c *Compressor) Compress(ctx context.Context, messages []llm.Message, auxProvider llm.Provider, focusTopic string) ([]llm.Message, error) {
	nMessages := len(messages)
	minForCompress := c.protectFirstN + 3 + 1
	if nMessages <= minForCompress {
		slog.Warn("too few messages to compress",
			"messages", nMessages,
			"min_for_compress", minForCompress,
		)
		return messages, nil
	}

	displayTokens := estimateTokensRough(messages)

	// Phase 1: 工具输出修剪
	prunedMessages, prunedCount := c.pruneOldToolResults(messages, 20, c.tailTokenBudget)
	if prunedCount > 0 {
		slog.Info("pre-compression: pruned old tool results", "pruned_count", prunedCount)
	}
	messages = prunedMessages

	// Phase 2: 确定边界
	compressStart := c.protectFirstN
	compressStart = c.alignBoundaryForward(messages, compressStart)
	compressEnd := c.findTailCutByTokens(messages, compressStart, c.tailTokenBudget)

	if compressStart >= compressEnd || compressEnd > len(messages) {
		return messages, nil
	}

	turnsToSummarize := messages[compressStart:compressEnd]

	slog.Info("context compression triggered",
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
			slog.Warn("summary generation used degraded strategy", "err", err)
		}
	}

	// Phase 4: 组装
	compressed := make([]llm.Message, 0, compressStart+1+(len(messages)-compressEnd))

	for i := 0; i < compressStart; i++ {
		msg := messages[i]
		if i == 0 && msg.Role == llm.RoleSystem {
			compressionNote := "\n\n[Note: 之前的对话回合已被压缩为交接总结以节省上下文空间。当前的会话状态可能仍反映之前的工作，请基于总结和当前状态继续，不要重复已完成的工作。]"
			if !containsText(msg.Content, "[Note:") {
				msg.Content = msg.Content + compressionNote
			}
		}
		compressed = append(compressed, msg)
	}

	if summary != "" {
		compressed = append(compressed, llm.Message{
			Role:    c.pickSummaryRole(messages, compressStart, compressEnd),
			Content: summary,
		})
	} else {
		turnsToFormat := messages[compressStart:compressEnd]
		if structuredSummary := FormatSummary(turnsToFormat); structuredSummary != "" {
			compressed = append(compressed, llm.Message{
				Role:    llm.RoleUser,
				Content: SummaryPrefix + "\n" + structuredSummary,
			})
		} else {
			nDropped := compressEnd - compressStart
			compressed = append(compressed, llm.Message{
				Role:    llm.RoleUser,
				Content: fmt.Sprintf("%s\n总结生成不可用。%d 条消息已被移除以释放上下文空间，但无法总结。基于下方的最近消息和文件/资源的当前状态继续。", SummaryPrefix, nDropped),
			})
		}
	}

	for i := compressEnd; i < len(messages); i++ {
		compressed = append(compressed, messages[i])
	}

	// Phase 5: 孤儿工具对清理
	compressed = c.sanitizeToolPairs(compressed)
	c.recordCompressionResult(displayTokens, estimateTokensRough(compressed))

	slog.Info("compression completed",
		"original_messages", nMessages,
		"compressed_messages", len(compressed),
	)

	return compressed, nil
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
	if lastHeadRole == llm.RoleAssistant || lastHeadRole == llm.RoleTool {
		return llm.RoleUser
	}
	if llm.RoleAssistant == firstTailRole {
		return llm.RoleUser
	}
	return llm.RoleAssistant
}

// estimateTokensRough 使用 llm 包的公共 token 估算函数。
func estimateTokensRough(messages []llm.Message) int {
	return llm.EstimateTokensRough(messages)
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
