// Package agent 提供故障转移机制。
// 当主 Provider 不可用时，按回退链优先级自动切换到备选 Provider。
// 使用 llm.ClassifyFromError 进行统一的错误分类，取代原有的字符串匹配。
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── 回退链配置 ─────────────────────────────

// FallbackEntry 描述回退链中的一个提供者条目。
// Priority 越小优先级越高，回退时按升序依次尝试。
type FallbackEntry struct {
	Provider string `yaml:"provider"` // 提供者名称 (对应 config.Providers 中的 key)
	Model    string `yaml:"model"`    // 使用的模型名称
	Priority int    `yaml:"priority"` // 优先级 (数字越小越优先)
}

// FallbackChain 管理多提供者的优先级回退链。
// 当主提供者失败后，按 Priority 升序依次尝试回退链中的条目。
// 与 ProviderRouter 不同，FallbackChain 仅在主提供者重试耗尽后触发，
// 而 ProviderRouter 可作为独立的多提供者路由使用。
type FallbackChain struct {
	entries []*fallbackEntryRuntime // 按优先级排序的运行时条目
}

// fallbackEntryRuntime 是 FallbackEntry 的运行时表示，持有实际的 Provider 实例。
type fallbackEntryRuntime struct {
	provider llm.Provider // LLM 提供者实例
	model    string       // 模型名称
	priority int          // 优先级
}

// NewFallbackChain 创建回退链。
// entries 参数不要求预排序，内部会按 Priority 升序排列。
func NewFallbackChain(entries []*FallbackEntry, providerMap map[string]llm.Provider) *FallbackChain {
	if len(entries) == 0 {
		return &FallbackChain{}
	}

	runtime := make([]*fallbackEntryRuntime, 0, len(entries))
	for _, e := range entries {
		p, ok := providerMap[e.Provider]
		if !ok {
			slog.Warn("回退链: 提供者未找到，跳过",
				"provider", e.Provider,
				"model", e.Model,
			)
			continue
		}
		runtime = append(runtime, &fallbackEntryRuntime{
			provider: p,
			model:    e.Model,
			priority: e.Priority,
		})
	}

	// 按优先级升序排序
	sort.Slice(runtime, func(i, j int) bool {
		return runtime[i].priority < runtime[j].priority
	})

	slog.Info("回退链已初始化", "entry_count", len(runtime))
	return &FallbackChain{entries: runtime}
}

// ───────────────────────────── 错误分类 ─────────────────────────────

// shouldFallback 判断错误是否应该触发故障转移。
// 使用 llm.ClassifyFromError 进行统一分类，排除上下文溢出和格式错误
// (这两种错误应通过压缩或修正请求处理，而非切换提供者)。
func shouldFallback(err error) bool {
	if err == nil {
		return false
	}
	classified := llm.ClassifyFromError(err)

	// 上下文溢出: 应压缩上下文，而非切换提供者
	if classified.Reason == llm.ReasonContextOverflow {
		return false
	}
	// 格式错误: 是请求本身的问题，切换提供者无法解决
	if classified.Reason == llm.ReasonFormatError {
		return false
	}

	return classified.ShouldFallback
}

// ───────────────────────────── 回退链执行 ─────────────────────────────

// tryFallbackChain 按优先级依次尝试回退链中的提供者。
// 仅当 shouldFallback(err) 返回 true 时才执行回退。
// 返回第一个成功的响应，或全部失败时返回最后一个错误。
func (a *AIAgent) tryFallbackChain(ctx context.Context, err error, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if !shouldFallback(err) {
		return nil, err
	}

	// 优先使用 ProviderRouter (如果已配置)
	if a.router != nil {
		slog.Warn("主提供者失败，委托 ProviderRouter 进行回退",
			"session_id", a.sessionID,
			"original_err", err.Error(),
		)
		resp, routerErr := a.router.ChatCompletion(ctx, req)
		if routerErr == nil {
			return resp, nil
		}
		slog.Warn("ProviderRouter 回退也失败，尝试回退链",
			"session_id", a.sessionID,
			"router_err", routerErr.Error(),
		)
	}

	// 使用回退链
	if a.fallbackChain == nil || len(a.fallbackChain.entries) == 0 {
		return nil, err
	}

	classified := llm.ClassifyFromError(err)
	slog.Warn("启动回退链",
		"session_id", a.sessionID,
		"reason", classified.Reason,
		"entry_count", len(a.fallbackChain.entries),
	)

	var lastErr error
	for _, entry := range a.fallbackChain.entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		slog.Warn("回退链: 尝试提供者",
			"provider", entry.provider.Name(),
			"model", entry.model,
		)

		// 临时替换模型名称
		originalModel := req.Model
		req.Model = entry.model

		resp, tryErr := entry.provider.CreateChatCompletion(ctx, req)

		// 恢复原始模型
		req.Model = originalModel

		if tryErr == nil {
			slog.Info("回退链: 提供者成功",
				"provider", entry.provider.Name(),
				"model", entry.model,
			)
			return resp, nil
		}

		lastErr = tryErr
		slog.Warn("回退链: 提供者失败",
			"provider", entry.provider.Name(),
			"model", entry.model,
			"error", tryErr.Error(),
		)

		// 如果该错误不应继续回退 (如认证错误在不同提供者可能不同)
		// 但由于是不同提供者，我们仍然继续尝试下一个
	}

	return nil, fmt.Errorf("回退链所有提供者均失败 (共 %d 个): %w", len(a.fallbackChain.entries), lastErr)
}

// ───────────────────────────── 旧版兼容 ─────────────────────────────

// tryFallback 使用备选 Provider 执行请求 (保留向后兼容)。
// 当未配置回退链且未配置 ProviderRouter 时使用此方法。
func (a *AIAgent) tryFallback(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if a.fallbackProvider == nil {
		return nil, nil
	}

	slog.Warn("切换到备选 Provider (旧版)",
		"from_model", a.model,
		"to_model", a.fallbackModel,
	)

	return a.fallbackProvider.CreateChatCompletion(ctx, req)
}

// ───────────────────────────── 健康恢复扫描 ─────────────────────────────

// recoverUnhealthyProviders 定期尝试恢复不健康的提供者。
// 作为 ProviderRouter 健康检查的补充，在回退链层面也做轻量探测。
func (a *AIAgent) recoverUnhealthyProviders(ctx context.Context) {
	if a.router == nil {
		return
	}

	entries := a.router.GetEntries()
	for _, entry := range entries {
		if entry.Healthy.Load() {
			continue
		}

		// 距离上次错误超过 5 分钟才尝试恢复
		if time.Since(entry.LastErr.Load().(time.Time)) < 5*time.Minute {
			continue
		}

		checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := entry.Provider.ListModels(checkCtx)
		cancel()

		if err == nil {
			a.router.MarkHealthy(entry.Provider.Name(), true)
			slog.Info("回退恢复: 提供者已恢复",
				"provider", entry.Provider.Name(),
			)
		}
	}
}
