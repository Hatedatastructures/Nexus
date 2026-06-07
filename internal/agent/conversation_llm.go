package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	ictx "nexus-agent/internal/context"
	"nexus-agent/internal/llm"
)

// ───────────────────────────── 系统提示词 ─────────────────────────────

func (a *AIAgent) buildSystemPrompt(ctx context.Context, systemMessage string) (string, error) {
	a.mu.Lock()
	cached := a.cachedSystemPrompt
	a.mu.Unlock()

	if cached != "" {
		return cached, nil
	}

	if a.contextBuilder == nil {
		fallback := "你是一个 AI 助手。使用可用的工具完成任务。提供清晰、有帮助的回复。"
		if systemMessage != "" {
			fallback = systemMessage
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		a.cachedSystemPrompt = fallback
		return fallback, nil
	}

	prompt, err := a.contextBuilder.Build(ctx, &ictx.BuildOptions{
		SystemMessage: systemMessage,
		SessionID:     a.sessionID,
		Model:         a.model,
	})
	if err != nil {
		return "", err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.cachedSystemPrompt = prompt

	return prompt, nil
}

func (a *AIAgent) invalidateSystemPrompt() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cachedSystemPrompt = ""
}

// ───────────────────────────── 预压缩 ─────────────────────────────

func (a *AIAgent) preflightCompress(ctx context.Context, messages []llm.Message, systemPrompt string) ([]llm.Message, string) {
	if a.compressor == nil {
		return messages, systemPrompt
	}

	const minForCompress = 7
	if len(messages) <= minForCompress {
		return messages, systemPrompt
	}

	estimatedTokens := estimateTokensRough(messages)
	if !a.compressor.ShouldCompress(a.compressor.TailTokenBudget(), estimatedTokens) {
		return messages, systemPrompt
	}

	slog.Info("pre-compression: context exceeded threshold",
		"session_id", a.sessionID,
		"estimated_tokens", estimatedTokens,
	)

	compressed, err := a.compressor.Compress(ctx, messages, a.provider, "")
	if err != nil {
		slog.Warn("pre-compression failed", "session_id", a.sessionID, "err", err)
		return messages, systemPrompt
	}

	a.invalidateSystemPrompt()
	return compressed, systemPrompt
}

// performCompress 执行上下文压缩并返回压缩后的消息。
func (a *AIAgent) performCompress(ctx context.Context, messages []llm.Message, systemPrompt string) ([]llm.Message, string) {
	slog.Info("context compression triggered",
		"session_id", a.sessionID,
		"message_count", len(messages),
	)

	compressed, err := a.compressor.Compress(ctx, messages, a.provider, "")
	if err != nil {
		slog.Warn("compression failed", "session_id", a.sessionID, "err", err)
		return messages, systemPrompt
	}

	a.invalidateSystemPrompt()
	return compressed, systemPrompt
}

// ───────────────────────────── API 请求构建 ─────────────────────────────

func (a *AIAgent) buildAPIRequest(messages []llm.Message, _ string) *llm.ChatRequest {
	var tools []llm.ToolSchema
	if a.registry != nil {
		defs := a.registry.GetDefinitions(nil)
		for _, d := range defs {
			tools = append(tools, llm.ToolSchema{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.Parameters,
			})
		}
	}

	req := &llm.ChatRequest{
		Model:     a.model,
		Messages:  messages,
		Tools:     tools,
		MaxTokens: a.maxTokens,
	}

	if a.reasoningCfg != nil && a.reasoningCfg.Enabled {
		thinkingCfg := &llm.ThinkingConfig{
			Type:         "enabled",
			BudgetTokens: a.reasoningCfg.BudgetTokens,
			Effort:       a.reasoningCfg.Effort,
		}
		if a.reasoningCfg.BudgetTokens > 0 {
			thinkingCfg.Type = "enabled"
		} else {
			thinkingCfg.Type = "auto"
		}
		if thinkingParams := llm.BuildThinkingParam(thinkingCfg, a.model); thinkingParams != nil {
			req.Metadata = thinkingParams
		}
	}

	return req
}

// ───────────────────────────── LLM 调用 (带重试) ─────────────────────────────

func (a *AIAgent) callLLMWithRetry(ctx context.Context, req *llm.ChatRequest, messages *[]llm.Message) (*llm.ChatResponse, error) {
	a.mu.Lock()
	maxRetries := a.maxRetries
	a.mu.Unlock()
	if maxRetries <= 0 {
		maxRetries = 3
	}

	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		var resp *llm.ChatResponse
		var err error

		if a.streamCallback != nil && a.provider != nil {
			resp, err = a.handleStreamCall(ctx, req)
		} else if a.provider != nil {
			resp, err = a.provider.CreateChatCompletion(ctx, req)
		} else {
			return nil, fmt.Errorf("LLM 提供者未设置")
		}

		if err == nil {
			return resp, nil
		}

		lastErr = err

		// 分类错误并执行恢复策略
		classified := llm.ClassifyFromError(err)
		slog.Warn("LLM call failed",
			"session_id", a.sessionID,
			"attempt", attempt+1,
			"max_retries", maxRetries,
			"reason", classified.Reason,
			"err", err,
		)

		// 不可重试的错误
		if !classified.Retryable {
			return nil, err
		}

		// 认证/计费错误: 尝试轮换凭证
		if (classified.Reason == llm.ReasonAuth || classified.Reason == llm.ReasonBilling) && a.credentialPool != nil {
			a.credentialPool.MarkExhausted(ctx, classified.StatusCode, err.Error())
		}

		// 上下文溢出: 触发压缩
		if classified.ShouldCompress {
			if a.compressor != nil {
				// 使用调用方传入的消息列表（而非 a.messages），
				// 因为主循环的增量消息只追加到局部 messages 变量。
				compressed, compressErr := a.compressor.Compress(ctx, *messages, a.provider, "")
				if compressErr == nil {
					a.mu.Lock()
					a.messages = compressed
					a.cachedSystemPrompt = ""
					a.mu.Unlock()

					// 同步更新调用方的消息列表和请求，使下一次重试使用压缩后的数据
					*messages = compressed
					req.Messages = compressed
				}
			}
		}

		// 退避等待
		if attempt < maxRetries-1 {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			slog.Debug("backoff waiting", "duration", backoff)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
	}

	// 所有重试均失败: 尝试回退链 (Router → FallbackChain → 旧版 fallback)
	if lastErr != nil {
		if resp, fbErr := a.tryFallbackChain(ctx, lastErr, req); fbErr == nil {
			return resp, nil
		}

		// 旧版 fallback 兜底 (当既无 Router 也无 FallbackChain 时)
		if a.fallbackProvider != nil {
			if resp, fbErr := a.tryFallback(ctx, req); fbErr == nil && resp != nil {
				return resp, nil
			}
		}
	}

	return nil, fmt.Errorf("重试 %d 次后仍失败: %w", maxRetries, lastErr)
}

// handleStreamCall 处理流式 API 调用。
func (a *AIAgent) handleStreamCall(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	deltaCh, err := a.provider.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}

	var (
		contentBuilder   strings.Builder
		toolCalls        []llm.ToolCall
		finalUsage       *llm.TokenUsage
		reasoningBuilder strings.Builder
	)

Loop:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case delta, ok := <-deltaCh:
			if !ok {
				break Loop
			}
			if delta.Error != nil {
				return nil, delta.Error
			}

			if delta.Done {
				if len(delta.ToolCalls) > 0 {
					toolCalls = delta.ToolCalls
				}
				if delta.Usage != nil {
					finalUsage = delta.Usage
				}
				if delta.Reasoning != "" {
					reasoningBuilder.WriteString(delta.Reasoning)
				}
				break Loop
			}

			if delta.Content != "" {
				contentBuilder.WriteString(delta.Content)
				a.mu.Lock()
				streamCb := a.streamCallback
				a.mu.Unlock()

				if streamCb != nil {
					streamCb(delta.Content)
				}
			}

			if delta.Reasoning != "" {
				reasoningBuilder.WriteString(delta.Reasoning)
			}

			if len(delta.ToolCalls) > 0 {
				toolCalls = delta.ToolCalls
			}

			if delta.Usage != nil {
				finalUsage = delta.Usage
			}
		}
	}

	// 使用 LLM 返回的 stop reason，仅在没有工具调用时回退
	stopReason := llm.StopEndTurn
	if len(toolCalls) > 0 {
		stopReason = llm.StopToolUse
	}

	return &llm.ChatResponse{
		Content:    contentBuilder.String(),
		ToolCalls:  toolCalls,
		StopReason: stopReason,
		Usage:      finalUsage,
		Reasoning:  reasoningBuilder.String(),
	}, nil
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// 使用 llm 包的公共 token 估算函数，避免与 compressor.go 中的实现重复
func estimateTokensRough(messages []llm.Message) int {
	return llm.EstimateTokensRough(messages)
}
