// Package agent 提供 AI 代理的对话循环实现。
// RunConversation 是代理的主入口，编排从系统提示词构建到工具调用执行的完整对话生命周期。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"nexus-agent/internal/llm"
	ictx "nexus-agent/internal/context"
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

	slog.Info("对话轮次开始",
		"session_id", a.sessionID,
		"model", a.model,
		"platform", a.platform,
		"history_len", len(history),
	)

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

	// ── 2. 组装消息列表 ──
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

		slog.Debug("LLM 调用",
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
				slog.Warn("上下文溢出，触发压缩",
					"session_id", a.sessionID,
					"attempt", compressionAttempts,
				)
				messages, systemPrompt = a.performCompress(ctx, messages, systemPrompt)
				continue
			}

			result.Error = fmt.Errorf("LLM 调用失败: %w", err)
			result.Completed = false
			return result, result.Error
		}

		// 追踪 token 用量
		if resp.Usage != nil {
			result.TotalTokens += int64(resp.Usage.TotalTokens)
			result.CachedTokens += int64(resp.Usage.CacheReadTokens)
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
				slog.Debug("LLM 返回 tool_use 停止", "session_id", a.sessionID, "tool_count", len(resp.ToolCalls))
			}
		} else if stopReason == llm.StopEndTurn || stopReason == "stop" || stopReason == "" {
			// end_turn / stop / 空: 无工具调用，退出循环
			finalResponse = resp.Content
			result.Completed = true
			break
		} else if stopReason == llm.StopMaxTokens || stopReason == llm.StopLength {
			// max_tokens / length: 文本被截断，继续生成
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

			toolResults := a.executeToolCalls(ctx, resp.ToolCalls)
			toolCallCount += len(resp.ToolCalls)

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
				messages, systemPrompt = a.performCompress(ctx, messages, systemPrompt)
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

	// ── 5. 同步记忆 (异步) ──
	if a.memoryManager != nil {
		// 异步同步记忆上下文，添加 panic recover 防止 goroutine 崩溃导致进程退出
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("记忆同步 goroutine panic",
						"session_id", a.sessionID,
						"panic", r,
					)
				}
			}()
			a.memoryManager.SystemPromptBlock()
		}()
	}

	a.mu.Lock()
	a.messages = messages
	a.mu.Unlock()

	slog.Info("对话轮次完成",
		"session_id", a.sessionID,
		"api_calls", apiCallCount,
		"tool_calls", toolCallCount,
		"tokens", result.TotalTokens,
		"duration_ms", result.Duration.Milliseconds(),
	)

	return result, nil
}

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
		a.cachedSystemPrompt = fallback
		a.mu.Unlock()
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
	a.cachedSystemPrompt = prompt
	a.mu.Unlock()

	return prompt, nil
}

func (a *AIAgent) invalidateSystemPrompt() {
	a.mu.Lock()
	a.cachedSystemPrompt = ""
	a.mu.Unlock()
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

	slog.Info("预压缩: 上下文已超阈值",
		"session_id", a.sessionID,
		"estimated_tokens", estimatedTokens,
	)

	compressed, err := a.compressor.Compress(ctx, messages, a.provider, "")
	if err != nil {
		slog.Warn("预压缩失败", "session_id", a.sessionID, "err", err)
		return messages, systemPrompt
	}

	a.invalidateSystemPrompt()
	return compressed, systemPrompt
}

// performCompress 执行上下文压缩并返回压缩后的消息。
func (a *AIAgent) performCompress(ctx context.Context, messages []llm.Message, systemPrompt string) ([]llm.Message, string) {
	slog.Info("上下文压缩触发",
		"session_id", a.sessionID,
		"message_count", len(messages),
	)

	compressed, err := a.compressor.Compress(ctx, messages, a.provider, "")
	if err != nil {
		slog.Warn("压缩失败", "session_id", a.sessionID, "err", err)
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
		if a.reasoningCfg.BudgetTokens > 0 {
			req.Metadata = map[string]any{
				"thinking": map[string]any{
					"type":          "enabled",
					"budget_tokens": a.reasoningCfg.BudgetTokens,
				},
			}
		}
	}

	return req
}

// ───────────────────────────── LLM 调用 (带重试) ─────────────────────────────

func (a *AIAgent) callLLMWithRetry(ctx context.Context, req *llm.ChatRequest, messages *[]llm.Message) (*llm.ChatResponse, error) {
	maxRetries := a.maxRetries
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
		slog.Warn("LLM 调用失败",
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
					// 内联清空缓存，避免调用 invalidateSystemPrompt 导致死锁
					// （当前已持有 a.mu 锁，invalidateSystemPrompt 内部会再次 Lock）
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
			slog.Debug("退避等待", "duration", backoff)
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
		contentBuilder  strings.Builder
		toolCalls       []llm.ToolCall
		finalUsage      *llm.TokenUsage
		reasoningBuilder strings.Builder
	)

	for delta := range deltaCh {
		if delta.Error != nil {
			return nil, delta.Error
		}

		if delta.Content != "" {
			contentBuilder.WriteString(delta.Content)
			if a.streamCallback != nil {
				a.streamCallback(delta.Content)
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

	// 根据是否有工具调用动态设置 stop reason
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

// ───────────────────────────── 工具执行 ─────────────────────────────

func (a *AIAgent) executeToolCalls(ctx context.Context, toolCalls []llm.ToolCall) []toolResult {
	if len(toolCalls) == 0 {
		return nil
	}

	// 解析所有工具调用的参数
	parsed := make([]toolCall, len(toolCalls))
	for i, tc := range toolCalls {
		parsed[i] = toolCall{call: tc, args: parseToolArguments(tc.Arguments)}
	}

	// 判断是否可以并行执行
	shouldParallel := a.shouldParallelize(toolCalls)

	if shouldParallel && len(toolCalls) > 1 {
		return a.executeParallel(ctx, parsed)
	}
	return a.executeSequential(ctx, parsed)
}

func (a *AIAgent) executeParallel(ctx context.Context, calls []toolCall) []toolResult {
	results := make([]toolResult, len(calls))
	var wg sync.WaitGroup

	for i, pc := range calls {
		wg.Add(1)
		go func(idx int, c toolCall) {
			defer wg.Done()

			// 命令安全审批（与 executeSequential 保持一致，防止绕过审批检查）
			if a.approvalChecker != nil {
				approved, reason := a.approvalChecker.CheckTool(ctx, c.call.Name, c.args)
				if approved != 0 { // 0 = Approved
					results[idx] = toolResult{
						CallID: c.call.ID,
						Name:   c.call.Name,
						Result: fmt.Sprintf(`{"error": "工具调用被拒绝: %s", "tool": "%s"}`, reason, c.call.Name),
						Error:  fmt.Errorf("审批未通过: %s", reason),
					}
					if a.toolCallback != nil {
						a.toolCallback(c.call.Name, c.args)
					}
					return
				}
			}

			result, err := a.dispatchTool(ctx, c.call.Name, c.args)
			results[idx] = toolResult{
				CallID: c.call.ID,
				Name:   c.call.Name,
				Result: result,
				Error:  err,
			}
			if a.toolCallback != nil {
				a.toolCallback(c.call.Name, c.args)
			}
		}(i, pc)
	}

	wg.Wait()
	return results
}

func (a *AIAgent) executeSequential(ctx context.Context, calls []toolCall) []toolResult {
	results := make([]toolResult, len(calls))

	for i, pc := range calls {
		// 命令安全审批
		if a.approvalChecker != nil {
			approved, reason := a.approvalChecker.CheckTool(ctx, pc.call.Name, pc.args)
			if approved != 0 { // 0 = Approved
				result := fmt.Sprintf(`{"error": "工具调用被拒绝: %s", "tool": "%s"}`, reason, pc.call.Name)
				results[i] = toolResult{
					CallID: pc.call.ID,
					Name:   pc.call.Name,
					Result: result,
					Error:  fmt.Errorf("审批未通过: %s", reason),
				}
				if a.toolCallback != nil {
					a.toolCallback(pc.call.Name, pc.args)
				}
				continue
			}
		}

		result, err := a.dispatchTool(ctx, pc.call.Name, pc.args)
		results[i] = toolResult{
			CallID: pc.call.ID,
			Name:   pc.call.Name,
			Result: result,
			Error:  err,
		}

		if a.toolCallback != nil {
			a.toolCallback(pc.call.Name, pc.args)
		}
	}

	return results
}

// applyGuardrails 对工具调用列表应用安全护栏检查。
// 过滤掉被护栏拦截的工具调用，返回允许执行的子集。
func (a *AIAgent) applyGuardrails(toolCalls []llm.ToolCall) []llm.ToolCall {
	var filtered []llm.ToolCall
	for _, tc := range toolCalls {
		args := parseToolArguments(tc.Arguments)
		allowed, reason := a.guardrails.Check(tc.Name, args)
		if allowed {
			filtered = append(filtered, tc)
		} else {
			slog.Warn("工具调用被护栏拦截",
				"session_id", a.sessionID,
				"tool", tc.Name,
				"reason", reason,
			)
		}
	}
	return filtered
}

func (a *AIAgent) dispatchTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if a.registry == nil {
		return `{"error": "工具注册中心未初始化"}`, fmt.Errorf("工具注册中心未初始化")
	}

	// 文件写入安全检查: 对 file_write/file_edit/patch 工具进行二次防护
	if a.fileSafety != nil && isFileWriteTool(name) {
		if path, ok := args["path"].(string); ok && path != "" {
			// 计算写入内容大小
			var contentSize int64
			if content, ok := args["content"].(string); ok {
				contentSize = int64(len(content))
			}
			if newText, ok := args["new_text"].(string); ok {
				contentSize = int64(len(newText))
			}

			allowed, reason := a.fileSafety.CheckWrite(path, contentSize)
			if !allowed {
				slog.Warn("文件写入被安全检查器拦截",
					"session_id", a.sessionID,
					"tool", name,
					"path", path,
					"reason", reason,
				)
				return fmt.Sprintf(`{"error": "文件写入被安全策略拦截: %s"}`, reason), nil
			}
		}
	}

	result, err := a.registry.Dispatch(ctx, name, args)
	if err != nil {
		return fmt.Sprintf(`{"error": "工具执行失败: %s"}`, err.Error()), err
	}
	return result, nil
}

// isFileWriteTool 判断工具名称是否为文件写入类工具。
func isFileWriteTool(name string) bool {
	switch name {
	case "file_write", "file_edit", "patch":
		return true
	default:
		return false
	}
}

func (a *AIAgent) shouldParallelize(toolCalls []llm.ToolCall) bool {
	if len(toolCalls) <= 1 {
		return false
	}

	parallelSafe := map[string]bool{
		"read_file":        true,
		"search_files":     true,
		"web_search":       true,
		"web_extract":      true,
		"browser_snapshot": true,
		"vision_analyze":   true,
		"skills_list":      true,
		"skill_view":       true,
		"list_directory":   true,
	}

	for _, tc := range toolCalls {
		if !parallelSafe[tc.Name] {
			return false
		}
	}
	return true
}

// ───────────────────────────── 工具参数解析 ─────────────────────────────

func parseToolArguments(argsJSON string) map[string]any {
	if argsJSON == "" {
		return make(map[string]any)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		slog.Warn("工具参数 JSON 解析失败", "err", err)
		return make(map[string]any)
	}
	return args
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// 使用 llm 包的公共 token 估算函数，避免与 compressor.go 中的实现重复
func estimateTokensRough(messages []llm.Message) int {
	return llm.EstimateTokensRough(messages)
}

