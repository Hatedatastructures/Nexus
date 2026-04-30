// Package agent 提供 AI 代理的构建选项。
// 本文件包含 AuxiliaryClient：多模型代理客户端，在主提供者失败时自动降级到备选。
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── 辅助客户端 ─────────────────────────────

// AuxiliaryClient 是多模型代理客户端。
// 当主提供者失败时自动降级到备选提供者，并根据错误类型智能路由。
//
// 错误路由策略:
//   - 配额耗尽 (402/billing) → 立即切换下一个提供者
//   - 速率限制 (429/rate_limit) → 短暂等待后重试当前提供者，仍失败则切换
//   - 网络错误 → 在当前提供者上重试
//   - 模型不存在 (404) → 尝试其他提供者
//   - 服务端错误 (5xx) → 重试当前提供者，仍失败则切换
type AuxiliaryClient struct {
	router     *ProviderRouter // 提供者路由器
	retryCount int             // 每个提供者的最大重试次数
}

// AuxiliaryClientConfig 定义辅助客户端的配置。
type AuxiliaryClientConfig struct {
	// RetryCount 为每个提供者的最大重试次数（默认 2）。
	RetryCount int
}

// DefaultAuxiliaryClientConfig 返回默认配置。
func DefaultAuxiliaryClientConfig() *AuxiliaryClientConfig {
	return &AuxiliaryClientConfig{
		RetryCount: 2,
	}
}

// NewAuxiliaryClient 创建一个新的辅助客户端。
func NewAuxiliaryClient(router *ProviderRouter) *AuxiliaryClient {
	return NewAuxiliaryClientWithConfig(router, DefaultAuxiliaryClientConfig())
}

// NewAuxiliaryClientWithConfig 使用自定义配置创建辅助客户端。
func NewAuxiliaryClientWithConfig(router *ProviderRouter, cfg *AuxiliaryClientConfig) *AuxiliaryClient {
	if cfg == nil {
		cfg = DefaultAuxiliaryClientConfig()
	}

	client := &AuxiliaryClient{
		router:     router,
		retryCount: cfg.RetryCount,
	}

	slog.Info("AuxiliaryClient 已初始化", "retryCount", client.retryCount)
	return client
}

// ChatCompletion 发送聊天补全请求，带自动降级逻辑。
// 按路由器中的优先级顺序尝试，每个提供者失败后智能决定重试或降级。
func (c *AuxiliaryClient) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	return c.chatCompletionWithStrategy(ctx, req, false)
}

// ChatCompletionStream 发送流式聊天补全请求，带自动降级逻辑。
// 流式请求一旦开始就不在当前提供者内重试，直接降级到下一个。
func (c *AuxiliaryClient) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamDelta, error) {
	return c.chatCompletionStreamWithStrategy(ctx, req, false)
}

// ───────────────────────────── 内部实现 ─────────────────────────────

// chatCompletionWithStrategy 实现带策略的聊天补全。
func (c *AuxiliaryClient) chatCompletionWithStrategy(ctx context.Context, req *llm.ChatRequest, isRetry bool) (*llm.ChatResponse, error) {
	entries := c.router.GetEntries()

	var lastErr error
	var attempted int

	for _, entry := range entries {
		// 跳过不健康的提供者（除非是首次调用且所有都不健康）
		if !entry.Healthy && !isRetry {
			continue
		}

		slog.Debug("尝试提供者",
			"provider", entry.Provider.Name(),
			"model", entry.Model,
			"attempt", attempted+1,
		)

		resp, err := c.tryProvider(ctx, entry, req)
		if err == nil {
			c.router.MarkHealthy(entry.Provider.Name(), true)
			slog.Info("聊天补全成功",
				"provider", entry.Provider.Name(),
				"model", entry.Model,
				"stopReason", resp.StopReason,
			)
			return resp, nil
		}

		lastErr = err
		attempted++

		// 分类错误，决定重试还是降级
		action := c.classifyErrorAction(err)

		switch action {
		case actionRetry:
			// 在当前提供者上重试
			slog.Warn("在当前提供者上重试",
				"provider", entry.Provider.Name(),
				"error", err.Error(),
			)
			// 重试已经在 tryProvider 内部处理
			continue

		case actionRetryThenFallback:
			// 标记不健康，尝试下一个
			c.router.MarkHealthy(entry.Provider.Name(), false)
			slog.Warn("提供者失败，重试耗尽后降级",
				"provider", entry.Provider.Name(),
				"error", err.Error(),
			)
			continue

		case actionImmediateFallback:
			// 立即降级到下一个提供者
			c.router.MarkHealthy(entry.Provider.Name(), false)
			slog.Warn("立即降级到下一个提供者",
				"provider", entry.Provider.Name(),
				"error", err.Error(),
			)
			continue

		case actionAbort:
			// 不可恢复的错误，直接返回
			slog.Error("不可恢复的错误，终止降级",
				"provider", entry.Provider.Name(),
				"error", err.Error(),
			)
			return nil, err

		default:
			// 默认降级
			c.router.MarkHealthy(entry.Provider.Name(), false)
			continue
		}
	}

	if lastErr == nil {
		return nil, &noHealthyProviderError{}
	}

	return nil, fmt.Errorf("所有提供者均失败（尝试了 %d 个）: %w", attempted, lastErr)
}

// chatCompletionStreamWithStrategy 实现带策略的流式聊天补全。
func (c *AuxiliaryClient) chatCompletionStreamWithStrategy(ctx context.Context, req *llm.ChatRequest, isRetry bool) (<-chan *llm.StreamDelta, error) {
	entries := c.router.GetEntries()

	var lastErr error
	var attempted int

	for _, entry := range entries {
		if !entry.Healthy && !isRetry {
			continue
		}

		slog.Debug("尝试提供者（流式）",
			"provider", entry.Provider.Name(),
			"model", entry.Model,
		)

		originalModel := req.Model
		req.Model = entry.Model

		ch, err := entry.Provider.CreateChatCompletionStream(ctx, req)
		req.Model = originalModel

		if err == nil {
			c.router.MarkHealthy(entry.Provider.Name(), true)
			return ch, nil
		}

		lastErr = err
		attempted++

		action := c.classifyErrorAction(err)
		switch action {
		case actionImmediateFallback, actionRetryThenFallback:
			c.router.MarkHealthy(entry.Provider.Name(), false)
			continue
		case actionAbort:
			return nil, err
		default:
			c.router.MarkHealthy(entry.Provider.Name(), false)
			continue
		}
	}

	if lastErr == nil {
		return nil, &noHealthyProviderError{}
	}

	return nil, fmt.Errorf("所有提供者均失败（流式，尝试了 %d 个）: %w", attempted, lastErr)
}

// tryProvider 在单个提供者上尝试请求，内部包含重试逻辑。
func (c *AuxiliaryClient) tryProvider(ctx context.Context, entry *ProviderEntry, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	originalModel := req.Model
	req.Model = entry.Model

	var lastErr error
	for attempt := 0; attempt <= c.retryCount; attempt++ {
		if attempt > 0 {
			// 重试前短暂等待
			delay := ExponentialBackoff(attempt-1, 1*time.Second, 30*time.Second)
			slog.Debug("重试等待", "attempt", attempt, "delay", delay.String())

			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				req.Model = originalModel
				return nil, ctx.Err()
			case <-timer.C:
			}
		}

		resp, err := entry.Provider.CreateChatCompletion(ctx, req)
		if err == nil {
			req.Model = originalModel
			return resp, nil
		}

		lastErr = err
		action := c.classifyErrorAction(err)

		// 如果是不可重试的错误，立即退出
		if action == actionAbort {
			req.Model = originalModel
			return nil, err
		}

		// 如果是需要立即降级的错误，也退出重试循环
		if action == actionImmediateFallback {
			req.Model = originalModel
			return nil, err
		}
	}

	req.Model = originalModel
	return nil, lastErr
}

// ───────────────────────────── 错误动作分类 ─────────────────────────────

// errorAction 定义对错误的处理动作。
type errorAction int

const (
	actionRetry          errorAction = iota // 在当前提供者上重试
	actionRetryThenFallback                 // 重试后降级
	actionImmediateFallback                 // 立即降级到下一个提供者
	actionAbort                             // 不可恢复，终止降级
)

// classifyErrorAction 根据错误类型决定应采取的动作。
func (c *AuxiliaryClient) classifyErrorAction(err error) errorAction {
	if err == nil {
		return actionRetry
	}

	errMsg := err.Error()
	errMsgLower := errMsg

	statusCode := extractStatusCode(err)

	// 如果有状态码，使用内置分类器
	if statusCode > 0 {
		classified := llm.ClassifyError(statusCode, errMsgLower)

		switch classified.Reason {
		case llm.ReasonBilling:
			// 计费耗尽，立即切换提供者
			return actionImmediateFallback

		case llm.ReasonAuth:
			// 认证失败，不可恢复
			return actionAbort

		case llm.ReasonRateLimit:
			// 速率限制，重试后降级
			return actionRetryThenFallback

		case llm.ReasonModelNotFound:
			// 模型不存在，尝试其他提供者
			return actionImmediateFallback

		case llm.ReasonContextOverflow:
			// 上下文溢出，不应降级（需要压缩）
			return actionAbort

		case llm.ReasonServerError, llm.ReasonOverloaded:
			// 服务端错误/过载，重试后降级
			return actionRetryThenFallback

		case llm.ReasonFormatError:
			// 格式错误，不应降级（是请求本身的问题）
			return actionAbort

		default:
			return actionImmediateFallback
		}
	}

	// 无状态码，通过消息内容判断
	return classifyMessageAction(errMsgLower)
}

// classifyMessageAction 通过消息内容决定错误动作。
func classifyMessageAction(msgLower string) errorAction {
	// 计费耗尽 → 立即降级
	billingKeywords := []string{
		"insufficient credits", "insufficient_quota", "credit balance",
		"credits have been exhausted", "top up your credits",
		"payment required", "billing hard limit", "exceeded your current quota",
	}
	for _, kw := range billingKeywords {
		if containsStr(msgLower, kw) {
			return actionImmediateFallback
		}
	}

	// 认证错误 → 终止
	authKeywords := []string{
		"invalid api key", "invalid_api_key", "unauthorized",
		"forbidden", "invalid token", "token expired",
	}
	for _, kw := range authKeywords {
		if containsStr(msgLower, kw) {
			return actionAbort
		}
	}

	// 模型不存在 → 立即降级
	modelKeywords := []string{
		"is not a valid model", "invalid model", "model not found",
		"model_not_found", "unknown model",
	}
	for _, kw := range modelKeywords {
		if containsStr(msgLower, kw) {
			return actionImmediateFallback
		}
	}

	// 上下文溢出 → 终止（需要压缩）
	contextKeywords := []string{
		"context length", "maximum context", "token limit",
		"too many tokens", "prompt is too long",
	}
	for _, kw := range contextKeywords {
		if containsStr(msgLower, kw) {
			return actionAbort
		}
	}

	// 速率限制 → 重试后降级
	rateLimitKeywords := []string{
		"rate limit", "rate_limit", "too many requests", "throttled",
		"resource_exhausted",
	}
	for _, kw := range rateLimitKeywords {
		if containsStr(msgLower, kw) {
			return actionRetryThenFallback
		}
	}

	// 过载 → 重试后降级
	overloadKeywords := []string{
		"overloaded", "service unavailable", "model is overloaded",
	}
	for _, kw := range overloadKeywords {
		if containsStr(msgLower, kw) {
			return actionRetryThenFallback
		}
	}

	// 网络错误 → 重试
	networkKeywords := []string{
		"connection refused", "connection reset", "timeout",
		"no such host", "network is unreachable", "i/o timeout",
		"context deadline exceeded",
	}
	for _, kw := range networkKeywords {
		if containsStr(msgLower, kw) {
			return actionRetry
		}
	}

	// 默认：立即降级
	return actionImmediateFallback
}

// containsStr 是 strings.Contains 的简洁别名。
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// ───────────────────────────── OpenRouter 兼容模式 ─────────────────────────────

// OpenRouterCompatibleClient 是对 AuxiliaryClient 的包装，提供 OpenRouter 兼容接口。
// 适用于需要通过 OpenRouter API 访问多个模型的场景。
type OpenRouterCompatibleClient struct {
	aux *AuxiliaryClient
}

// NewOpenRouterCompatibleClient 创建一个 OpenRouter 兼容的辅助客户端。
func NewOpenRouterCompatibleClient(aux *AuxiliaryClient) *OpenRouterCompatibleClient {
	return &OpenRouterCompatibleClient{aux: aux}
}

// Chat 发送聊天请求（OpenRouter 兼容命名）。
func (c *OpenRouterCompatibleClient) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	return c.aux.ChatCompletion(ctx, req)
}

// ChatStream 发送流式聊天请求（OpenRouter 兼容命名）。
func (c *OpenRouterCompatibleClient) ChatStream(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamDelta, error) {
	return c.aux.ChatCompletionStream(ctx, req)
}

// WithFallbackModel 创建一个指定备选模型的请求。
// 当主模型不可用时，自动使用备选模型。
func (c *OpenRouterCompatibleClient) WithFallbackModel(primary, fallback string) func(req *llm.ChatRequest) {
	return func(req *llm.ChatRequest) {
		req.Model = primary
		// 在路由器中，如果 primary 模型所在的提供者失败，
		// 会自动尝试其他提供者的 fallback 模型
	}
}
