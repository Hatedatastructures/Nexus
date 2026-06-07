// Package agent 提供 AI 代理的对话循环实现。
// RunConversation 是代理的主入口，编排从系统提示词构建到工具调用执行的完整对话生命周期。
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"nexus-agent/internal/llm"
	"nexus-agent/internal/state"
	"nexus-agent/internal/telemetry"
)

// ───────────────────────────── 工具结果 ─────────────────────────────

// toolResult 表示单个工具调用的执行结果。
type toolResult struct {
	CallID string // 工具调用 ID (对应 ToolCall.ID)
	Name   string // 工具名称
	Result string // JSON 格式的执行结果
	Error  error  // 执行错误 (非 nil 时 Result 仍为有效 JSON)
}

// toolCall 带解析参数的内部类型
type toolCall struct {
	call llm.ToolCall
	args map[string]any
}

// ───────────────────────────── 对话循环 ─────────────────────────────

// RunConversation 执行单轮对话循环。
//
// 流程:
//  1. 构建/缓存系统提示词
//  2. 组装消息列表: [systemPrompt] + history + [userMsg]
//  3. 预检查上下文压缩
//  4. 主循环: LLM调用 → 错误恢复 → 响应处理 → 工具执行 → 压缩检查
//  5. 同步记忆，返回结果
func (a *AIAgent) RunConversation(ctx context.Context, userMessage string, history []llm.Message, systemMessage string) (*TurnResult, error) {
	startTime := time.Now()

	slog.Info("conversation turn started",
		"session_id", a.sessionID,
		"model", a.model,
		"platform", a.platform,
		"history_len", len(history),
	)

	telemetry.RecordSimple(telemetry.EventTurnStarted, a.sessionID, map[string]any{
		"model":    a.model,
		"platform": a.platform,
	})

	// 会话持久化: 记录用户输入
	if a.persister != nil {
		if err := a.persister.RecordPromptHistory(userMessage); err != nil {
			slog.Warn("failed to record prompt history", "session_id", a.sessionID, "err", err)
		}
	}

	// ── 初始化 ──
	a.mu.Lock()
	a.maxRetries = 3
	a.mu.Unlock()

	// 重置工具调用护栏状态 (每轮新消息开始时)
	if a.guardrails != nil {
		a.guardrails.Reset()
	}

	result := &TurnResult{
		Completed: false,
	}

	// ── 1. 构建/缓存系统提示词 ──
	systemPrompt, err := a.buildSystemPrompt(ctx, systemMessage)
	if err != nil {
		result.Error = fmt.Errorf("构建系统提示词失败: %w", err)
		return result, result.Error
	}

	// 1.5. 记忆预取: 将相关记忆注入系统提示词
	if a.memoryManager != nil {
		prefetched, pfErr := a.memoryManager.PrefetchAll(ctx, userMessage)
		if pfErr == nil && prefetched != "" {
			systemPrompt += "\n\n# 相关记忆\n\n" + prefetched
		}
	}

	// ── 2. 组装消息列表 ──
	// 2.5. 恢复模式: 从 state.Store 加载历史
	if a.resumeMode && a.state != nil && a.sessionID != "" && len(history) == 0 {
		if records, err := a.state.GetMessages(ctx, a.sessionID, 50, 0); err == nil && len(records) > 0 {
			for _, r := range records {
				// 跳过纯工具调用条目 (role=tool 或有 tool_calls 但无用户可见内容)
				if r.Role == "tool" || (r.ToolCalls != "" && r.ToolCalls != "null" && r.ToolCalls != "[]" && r.Content == "") {
					continue
				}
				history = append(history, llm.Message{
					Role:    llm.MessageRole(r.Role),
					Content: r.Content,
				})
			}
		}
	}

	messages := make([]llm.Message, 0, len(history)+2)
	messages = append(messages, llm.Message{
		Role:    llm.RoleSystem,
		Content: systemPrompt,
	})
	messages = append(messages, history...)
	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: userMessage,
	})

	a.mu.Lock()
	a.messages = messages
	a.mu.Unlock()

	// ── 3. 预检查上下文压缩 ──
	messages, systemPrompt = a.preflightCompress(ctx, messages, systemPrompt)

	// ── 4. 主循环 ──
	apiCallCount := 0
	toolCallCount := 0
	var finalResponse string
	compressionAttempts := 0
	const maxCompressionAttempts = 3

	for a.iterationBudget.Consume() {
		apiCallCount++

		slog.Debug("LLM call",
			"session_id", a.sessionID,
			"iteration", apiCallCount,
			"remaining", a.iterationBudget.Remaining(),
			"messages", len(messages),
		)

		// ── 4a. 构建 API 请求 ──
		req := a.buildAPIRequest(messages, systemPrompt)

		// ── 4b. 调用 LLM (带重试和错误恢复) ──
		resp, err := a.callLLMWithRetry(ctx, req, &messages)
		if err != nil {
			if llm.ClassifyFromError(err).ShouldCompress && compressionAttempts < maxCompressionAttempts {
				compressionAttempts++
				slog.Warn("context overflow, triggering compression",
					"session_id", a.sessionID,
					"attempt", compressionAttempts,
				)
				messages, systemPrompt = a.performCompress(ctx, messages, systemPrompt)
				continue
			}

			result.Error = fmt.Errorf("LLM 调用失败: %w", err)
			result.Completed = false
			telemetry.RecordSimple(telemetry.EventTurnFailed, a.sessionID, map[string]any{
				"error": err.Error(),
			})
			return result, result.Error
		}

		// 追踪 token 用量
		if resp.Usage != nil {
			result.TotalTokens += int64(resp.Usage.TotalTokens)
			result.CachedTokens += int64(resp.Usage.CacheReadTokens)

			// 成本计算
			usage := CanonicalUsage{
				InputTokens:         int64(resp.Usage.PromptTokens),
				OutputTokens:        int64(resp.Usage.CompletionTokens),
				CacheReadTokens:     int64(resp.Usage.CacheReadTokens),
				CacheCreationTokens: int64(resp.Usage.CacheWriteTokens),
			}
			providerName := ""
			if a.provider != nil {
				providerName = a.provider.Name()
			}
			if costResult, err := EstimateCost(providerName, a.model, usage); err == nil {
				result.CostUSD += costResult.TotalCost
			}
		}

		// 推理过程回调
		if resp.Reasoning != "" && a.reasoningCallback != nil {
			a.reasoningCallback(resp.Reasoning)
		}

		// ── 4c. 处理响应 ──
		stopReason := resp.StopReason

		assistantMsg := llm.Message{
			Role:             llm.RoleAssistant,
			Content:          resp.Content,
			ToolCalls:        resp.ToolCalls,
			ReasoningContent: resp.Reasoning,
		}
		messages = append(messages, assistantMsg)

		// 有工具调用: 进入工具执行阶段 (无论 stop_reason 是什么)
		if len(resp.ToolCalls) > 0 {
			// Anthropic 的 tool_use stop reason 显式处理
			if stopReason == llm.StopToolUse || stopReason == "tool_use" {
				slog.Debug("LLM returned tool_use stop", "session_id", a.sessionID, "tool_count", len(resp.ToolCalls))
			}
		} else if stopReason == llm.StopEndTurn || stopReason == "stop" || stopReason == "" {
			// end_turn / stop / 空: 无工具调用，退出循环
			finalResponse = resp.Content
			result.Completed = true
			break
		} else if stopReason == llm.StopMaxTokens || stopReason == llm.StopLength {
			// max_tokens / length: 文本被截断，注入续接提示
			messages = append(messages, llm.Message{
				Role:    llm.RoleUser,
				Content: "Please continue from where you left off.",
			})
			continue
		} else {
			// 未知 stop reason，视为结束
			finalResponse = resp.Content
			result.Completed = true
			break
		}

		// ── 4d. 工具执行 ──
		if len(resp.ToolCalls) > 0 {
			// 护栏检查: 在执行前检测重复/固着等异常模式
			if a.guardrails != nil {
				filtered := a.applyGuardrails(resp.ToolCalls)
				if len(filtered) == 0 {
					// 所有工具调用均被护栏拦截，注入错误消息让 LLM 知晓
					for _, tc := range resp.ToolCalls {
						messages = append(messages, llm.Message{
							Role:       llm.RoleTool,
							Content:    `{"error": "工具调用被安全护栏拦截: 检测到重复或固着模式，请尝试不同的方法"}`,
							ToolCallID: tc.ID,
						})
					}
					continue
				}
				resp.ToolCalls = filtered
			}

			// 遥测: 工具执行开始
			for _, tc := range resp.ToolCalls {
				telemetry.RecordSimple(telemetry.EventToolStarted, a.sessionID, map[string]any{
					"tool": tc.Name,
				})
			}

			toolResults := a.executeToolCalls(ctx, resp.ToolCalls)
			toolCallCount += len(resp.ToolCalls)

			// 遥测: 工具执行结束
			for _, tr := range toolResults {
				data := map[string]any{"tool": tr.Name}
				if tr.Error != nil {
					data["error"] = tr.Error.Error()
				}
				telemetry.RecordSimple(telemetry.EventToolFinished, a.sessionID, data)
			}

			// Check 已同时更新护栏状态（历史记录、连续重复计数），
			// 不再需要单独调用 Record，否则会导致双重计数使拦截过早触发。
			// Record 方法保留用于向后兼容，但不应用于与 Check 配对的场景。

			for _, tr := range toolResults {
				messages = append(messages, llm.Message{
					Role:       llm.RoleTool,
					Content:    tr.Result,
					ToolCallID: tr.CallID,
				})
			}
		}

		// ── 4e. 上下文压缩检查 ──
		if a.compressor != nil && compressionAttempts < maxCompressionAttempts {
			estimatedTokens := estimateTokensRough(messages)
			if a.compressor.ShouldCompress(a.compressor.TailTokenBudget(), estimatedTokens) {
				compressionAttempts++
				telemetry.RecordSimple(telemetry.EventCompTriggered, a.sessionID, map[string]any{
					"estimated_tokens": estimatedTokens,
				})
				messages, systemPrompt = a.performCompress(ctx, messages, systemPrompt)
				telemetry.RecordSimple(telemetry.EventCompCompleted, a.sessionID, map[string]any{
					"message_count": len(messages),
				})
			}
		}
	}

	// ── 预算耗尽时的兜底 ──
	if !result.Completed && finalResponse == "" {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == llm.RoleAssistant {
				finalResponse = messages[i].Content
				break
			}
		}
	}

	result.FinalResponse = finalResponse
	result.Messages = messages
	result.APICalls = apiCallCount
	result.ToolCalls = toolCallCount
	result.Duration = time.Since(startTime)

	// ── 5. 同步记忆 (异步，30s 超时) ──
	if a.memoryManager != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("memory sync goroutine panic",
						"session_id", a.sessionID,
						"panic", r,
					)
				}
			}()
			done := make(chan struct{})
			go func() {
				_ = a.memoryManager.SystemPromptBlock()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				slog.Warn("memory sync timed out", "session_id", a.sessionID)
			}
		}()
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = messages

	slog.Info("conversation turn completed",
		"session_id", a.sessionID,
		"api_calls", apiCallCount,
		"tool_calls", toolCallCount,
		"tokens", result.TotalTokens,
		"duration_ms", result.Duration.Milliseconds(),
	)

	telemetry.RecordSimple(telemetry.EventTurnCompleted, a.sessionID, map[string]any{
		"api_calls":  apiCallCount,
		"tool_calls": toolCallCount,
		"tokens":     result.TotalTokens,
		"completed":  result.Completed,
	})

	// 会话持久化: 记录助手回复
	if a.persister != nil && result.FinalResponse != "" {
		if err := a.persister.RecordMessage(&state.MessageRecord{
			SessionID: a.sessionID,
			Role:      "assistant",
			Content:   result.FinalResponse,
		}); err != nil {
			slog.Warn("failed to record assistant message", "session_id", a.sessionID, "err", err)
		}

	}
	// state.Store: 持久化用户消息和助手回复
	if a.state != nil && a.sessionID != "" {
		if err := a.state.InsertMessage(ctx, &state.MessageRecord{
			SessionID: a.sessionID,
			Role:      "user",
			Content:   userMessage,
		}); err != nil {
			slog.Warn("failed to persist user message to state store", "session_id", a.sessionID, "err", err)
		}
		if result.FinalResponse != "" {
			if err := a.state.InsertMessage(ctx, &state.MessageRecord{
				SessionID: a.sessionID,
				Role:      "assistant",
				Content:   result.FinalResponse,
			}); err != nil {
				slog.Warn("failed to persist assistant message to state store", "session_id", a.sessionID, "err", err)
			}
		}
	}

	return result, nil
}
