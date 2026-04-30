// Package agent 提供故障转移机制。
// 当主 Provider 不可用时，自动切换到备选 Provider。
package agent

import (
	"context"
	"log/slog"
	"strings"

	"nexus-agent/internal/llm"
)

// shouldFallback 判断错误是否应该触发故障转移。
func shouldFallback(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// 配额耗尽、计费错误、服务端错误应触发 fallback
	return strings.Contains(msg, "quota") ||
		strings.Contains(msg, "402") ||
		strings.Contains(msg, "billing") ||
		strings.Contains(msg, "exhausted") ||
		strings.Contains(msg, "500") ||
		strings.Contains(msg, "502") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "server error")
}

// tryFallback 尝试使用备选 Provider 执行请求。
func (a *AIAgent) tryFallback(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if a.fallbackProvider == nil {
		return nil, nil
	}

	slog.Warn("切换到备选 Provider",
		"from_model", a.model,
		"to_model", a.fallbackModel,
	)

	return a.fallbackProvider.CreateChatCompletion(ctx, req)
}
