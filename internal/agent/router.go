// Package agent 提供 AI 代理的构建选项。
// 使用函数式选项模式 (Functional Options Pattern) 配置 AIAgent。
// 每个 With* 函数返回一个 AgentOption，用于在 NewAgent 中设置对应字段。
package agent

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── 健康检查 ─────────────────────────────

// ProviderEntry 描述一个带优先级的 LLM 提供者。
type ProviderEntry struct {
	Provider llm.Provider // LLM 提供者实例
	Model    string       // 使用的模型名称
	Priority int          // 优先级（数字越小优先级越高）
	Healthy  atomic.Bool  // 是否健康（原子操作保证并发安全）
	LastErr  atomic.Value // 最后一次错误时间（存储 time.Time）
}

// ProviderRouter 是基于优先级的多提供者路由。
// 按优先级尝试每个提供者，失败时自动切换到下一个。
// 支持健康检查和配额耗尽检测。
type ProviderRouter struct {
	entries []*ProviderEntry
	mu      sync.RWMutex

	// 健康检查参数
	healthInterval time.Duration // 健康检查间隔
	healthTimeout  time.Duration // 健康检查超时
	stopCh         chan struct{} // 停止信号
	stopOnce       sync.Once
}

// ProviderRouterConfig 定义路由器的配置参数。
type ProviderRouterConfig struct {
	// HealthInterval 为健康检查周期，0 表示禁用自动健康检查。
	HealthInterval time.Duration

	// HealthTimeout 为健康检查请求超时，0 表示使用默认值 30s。
	HealthTimeout time.Duration
}

// DefaultProviderRouterConfig 返回路由器默认配置。
func DefaultProviderRouterConfig() *ProviderRouterConfig {
	return &ProviderRouterConfig{
		HealthInterval: 5 * time.Minute,
		HealthTimeout:  30 * time.Second,
	}
}

// NewProviderRouter 创建并返回一个 ProviderRouter。
// entries 按优先级排序后存储；会自动启动周期性健康检查（如果配置了 HealthInterval）。
func NewProviderRouter(entries []*ProviderEntry) *ProviderRouter {
	cfg := DefaultProviderRouterConfig()
	return newProviderRouter(entries, cfg)
}

// NewProviderRouterWithConfig 使用自定义配置创建路由器。
func NewProviderRouterWithConfig(entries []*ProviderRouterConfigEntry, cfg *ProviderRouterConfig) *ProviderRouter {
	// 转换为 ProviderEntry 列表
	providerEntries := make([]*ProviderEntry, 0, len(entries))
	for _, e := range entries {
		providerEntries = append(providerEntries, &ProviderEntry{
			Provider: e.Provider,
			Model:    e.Model,
			Priority: e.Priority,
			
		})
	}
	return newProviderRouter(providerEntries, cfg)
}

// ProviderRouterConfigEntry 用于带配置的构造函数。
type ProviderRouterConfigEntry struct {
	Provider llm.Provider
	Model    string
	Priority int
}

func newProviderRouter(entries []*ProviderEntry, cfg *ProviderRouterConfig) *ProviderRouter {
	if cfg == nil {
		cfg = DefaultProviderRouterConfig()
	}

	r := &ProviderRouter{
		entries:        entries,
		healthInterval: cfg.HealthInterval,
		healthTimeout:  cfg.HealthTimeout,
		stopCh:         make(chan struct{}),
	}

	// 初始标记所有条目为健康
	for _, e := range r.entries {
		e.Healthy.Store(true)
	}

	// 按优先级排序（简单选择排序）
	for i := 0; i < len(r.entries); i++ {
		minIdx := i
		for j := i + 1; j < len(r.entries); j++ {
			if r.entries[j].Priority < r.entries[minIdx].Priority {
				minIdx = j
			}
		}
		if minIdx != i {
			r.entries[i], r.entries[minIdx] = r.entries[minIdx], r.entries[i]
		}
	}

	// 启动健康检查协程
	if r.healthInterval > 0 && len(r.entries) > 0 {
		go r.healthCheckLoop()
	}

	slog.Info("ProviderRouter initialized", "providerCount", len(r.entries))
	return r
}

// ChatCompletion 按优先级尝试所有提供者，直到成功或全部失败。
// 自动跳过不健康的提供者。
func (r *ProviderRouter) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	r.mu.RLock()
	ordered := make([]*ProviderEntry, len(r.entries))
	copy(ordered, r.entries)
	r.mu.RUnlock()

	var lastErr error
	for _, entry := range ordered {
		// 跳过不健康的提供者
		if !entry.Healthy.Load() {
			slog.Debug("skipping unhealthy provider", "provider", entry.Provider.Name(), "model", entry.Model)
			continue
		}

		// 使用浅拷贝避免修改原始请求的 Model 字段。
		// 原实现直接修改 req.Model，当 Provider 调用失败时
		// 错误路径会跳过恢复逻辑，导致请求状态污染。
		reqCopy := *req
		reqCopy.Model = entry.Model

		resp, err := entry.Provider.CreateChatCompletion(ctx, &reqCopy)

		if err == nil {
			// 成功：标记健康
			r.MarkHealthy(entry.Provider.Name(), true)
			slog.Debug("provider request succeeded", "provider", entry.Provider.Name(), "model", entry.Model)
			return resp, nil
		}

		// 失败：分类错误
		lastErr = err
		shouldFallback := r.shouldFallback(err)

		if shouldFallback {
			// 需要降级：标记不健康
			r.MarkHealthy(entry.Provider.Name(), false)
			slog.Warn("provider failed, trying next",
				"provider", entry.Provider.Name(),
				"model", entry.Model,
				"error", err.Error(),
			)
			continue
		}

		// 不可降级的错误（如认证失败），直接返回
		slog.Error("provider unrecoverable error, aborting routing",
			"provider", entry.Provider.Name(),
			"model", entry.Model,
			"error", err.Error(),
		)
		return nil, err
	}

	if lastErr == nil {
		return nil, &noHealthyProviderError{}
	}

	return nil, lastErr
}

// ChatCompletionStream 按优先级尝试所有提供者，返回第一个成功的流。
// 流式请求一旦开始就不降级（避免消费部分流后切换）。
func (r *ProviderRouter) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamDelta, error) {
	r.mu.RLock()
	ordered := make([]*ProviderEntry, len(r.entries))
	copy(ordered, r.entries)
	r.mu.RUnlock()

	var lastErr error
	for _, entry := range ordered {
		if !entry.Healthy.Load() {
			slog.Debug("skipping unhealthy provider (streaming)", "provider", entry.Provider.Name(), "model", entry.Model)
			continue
		}

		// 使用浅拷贝避免修改原始请求的 Model 字段。
		// 原实现直接修改 req.Model，当 Provider 调用失败时
		// 错误路径会跳过恢复逻辑，导致请求状态污染。
		reqCopy := *req
		reqCopy.Model = entry.Model

		ch, err := entry.Provider.CreateChatCompletionStream(ctx, &reqCopy)

		if err == nil {
			r.MarkHealthy(entry.Provider.Name(), true)
			return ch, nil
		}

		lastErr = err
		if r.shouldFallback(err) {
			r.MarkHealthy(entry.Provider.Name(), false)
			slog.Warn("streaming provider failed, trying next",
				"provider", entry.Provider.Name(),
				"model", entry.Model,
				"error", err.Error(),
			)
			continue
		}

		return nil, err
	}

	if lastErr == nil {
		return nil, &noHealthyProviderError{}
	}

	return nil, lastErr
}

// MarkHealthy 标记指定提供者的健康状态。
func (r *ProviderRouter) MarkHealthy(name string, healthy bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, entry := range r.entries {
		if entry.Provider.Name() == name {
			entry.Healthy.Store(healthy)
			if !healthy {
				entry.LastErr.Store(time.Now())
			}
			slog.Debug("marking provider health status",
				"provider", name,
				"healthy", healthy,
			)
			return
		}
	}
}

// GetHealthyProvider 返回当前健康的、优先级最高的提供者。
func (r *ProviderRouter) GetHealthyProvider() (llm.Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, entry := range r.entries {
		if entry.Healthy.Load() {
			return entry.Provider, nil
		}
	}

	return nil, &noHealthyProviderError{}
}

// GetEntries 返回所有提供者条目的快照（按当前优先级排序）。
func (r *ProviderRouter) GetEntries() []*ProviderEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*ProviderEntry, len(r.entries))
	copy(result, r.entries)
	return result
}

// Stop 停止健康检查协程。
func (r *ProviderRouter) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		slog.Info("ProviderRouter stopped")
	})
}

// ───────────────────────────── 健康检查 ─────────────────────────────

// healthCheckLoop 周期性对所有提供者执行健康检查。
func (r *ProviderRouter) healthCheckLoop() {
	ticker := time.NewTicker(r.healthInterval)
	defer ticker.Stop()

	slog.Info("health check goroutine started", "interval", r.healthInterval.String())

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.runHealthChecks()
		}
	}
}

// runHealthChecks 对所有不健康的提供者执行一次健康检查。
// 使用简单的 ListModels 调用来测试可用性。
func (r *ProviderRouter) runHealthChecks() {
	r.mu.RLock()
	unhealthy := make([]*ProviderEntry, 0)
	for _, entry := range r.entries {
		if !entry.Healthy.Load() {
			unhealthy = append(unhealthy, entry)
		}
	}
	r.mu.RUnlock()

	if len(unhealthy) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.healthTimeout)
	defer cancel()

	for _, entry := range unhealthy {
		// 使用 ListModels 做轻量级探测
		_, err := entry.Provider.ListModels(ctx)
		if err == nil {
			r.MarkHealthy(entry.Provider.Name(), true)
			slog.Info("health check passed, restoring provider",
				"provider", entry.Provider.Name(),
			)
		} else {
			slog.Debug("health check failed, remaining unhealthy",
				"provider", entry.Provider.Name(),
				"error", err.Error(),
			)
		}
	}
}

// ───────────────────────────── 错误分类 ─────────────────────────────

// shouldFallback 判断给定错误是否应该触发降级（切换到下一个提供者）。
// 使用统一的 llm.ClassifyFromError 进行分类。
func (r *ProviderRouter) shouldFallback(err error) bool {
	if err == nil {
		return false
	}
	classified := llm.ClassifyFromError(err)
	// 上下文溢出不应降级（需要压缩而非切换）
	if classified.Reason == llm.ReasonContextOverflow {
		return false
	}
	// 格式错误不应降级（是请求本身的问题）
	if classified.Reason == llm.ReasonFormatError {
		return false
	}
	return classified.ShouldFallback
}

// noHealthyProviderError 表示没有健康的提供者可用。
type noHealthyProviderError struct{}

func (e *noHealthyProviderError) Error() string {
	return "没有健康的 LLM 提供者可用"
}

// ───────────────────────────── 工具函数 ─────────────────────────────

// RetryDelay 根据 HTTP 状态码和建议的重试时间计算等待间隔。
// 429 通常带有 Retry-After 头，这里做简单估算。
func RetryDelay(statusCode int) time.Duration {
	switch statusCode {
	case 429:
		return 10 * time.Second // 速率限制，等待 10s
	case 500, 502:
		return 5 * time.Second // 服务端错误，等待 5s
	case 503, 529:
		return 15 * time.Second // 服务过载，等待 15s
	default:
		return 0
	}
}

// ExponentialBackoff 计算指数退避等待时间。
func ExponentialBackoff(attempt int, base time.Duration, max time.Duration) time.Duration {
	delay := time.Duration(float64(base) * math.Pow(2, float64(attempt)))
	if delay > max {
		return max
	}
	return delay
}
