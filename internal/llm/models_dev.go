// Package llm 提供 LLM 提供者抽象层。
// models_dev.go 实现 Models.dev 模型数据库客户端，提供模型元数据查询能力。
// 当 Models.dev API 不可用时，自动回退到内置的常用模型硬编码列表。
package llm

import (
	"context"
	"log/slog"
	"net/http"
	pkgerrors "nexus-agent/internal/errors"
	"sync"
	"time"
)

// ───────────────────────────── 模型元数据结构 ─────────────────────────────

// ModelDevInfo 描述来自 Models.dev 的模型元数据。
type ModelDevInfo struct {
	ID            string  `json:"id"`             // 模型唯一标识
	Provider      string  `json:"provider"`       // 提供者名称 (anthropic / openai / google / deepseek 等)
	ContextWindow int     `json:"context_window"` // 上下文窗口大小 (token 数)
	MaxOutput     int     `json:"max_output"`     // 最大输出 token 数
	Vision        bool    `json:"vision"`         // 是否支持视觉输入
	Reasoning     bool    `json:"reasoning"`      // 是否支持推理/思维链
	InputPrice    float64 `json:"input_price"`    // 输入价格 (每百万 token，美元)
	OutputPrice   float64 `json:"output_price"`   // 输出价格 (每百万 token，美元)
}

// ───────────────────────────── API 客户端 ─────────────────────────────

// modelsDevAPIURL 是 Models.dev API 的基础地址。
const modelsDevAPIURL = "https://models.dev/api/models"

// ModelsDevClient 是 Models.dev API 客户端，提供模型元数据查询能力。
// 内置 LRU 缓存（默认 1 小时 TTL），API 不可用时自动回退到硬编码列表。
type ModelsDevClient struct {
	cache     map[string]*ModelDevInfo // 模型 ID → 元数据
	cacheTime time.Time                // 缓存写入时间
	cacheTTL  time.Duration            // 缓存过期时间
	mu        sync.RWMutex             // 保护 cache 的读写锁
	refreshMu sync.Mutex               // 防止并发刷新
	refreshCh chan struct{}            // 刷新信号（容量 1，合并多次未命中）
	http      *http.Client             // HTTP 客户端
}

// NewModelsDevClient 创建 Models.dev 客户端实例。
// 默认缓存 TTL 为 1 小时，首次查询时自动加载数据。
func NewModelsDevClient() *ModelsDevClient {
	c := &ModelsDevClient{
		cache:     make(map[string]*ModelDevInfo),
		cacheTTL:  1 * time.Hour,
		refreshCh: make(chan struct{}, 1),
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}

	// 预加载内置模型列表作为初始数据
	c.loadBuiltinModels()

	return c
}

// GetModel 根据模型 ID 获取元数据信息。
// 优先从缓存查询，缓存过期时尝试刷新。返回 nil 表示未找到。
func (c *ModelsDevClient) GetModel(modelID string) *ModelDevInfo {
	c.mu.RLock()
	info, ok := c.cache[modelID]
	c.mu.RUnlock()

	if ok {
		return info
	}

	// 缓存未命中，尝试刷新（异步，不阻塞调用者）
	select {
	case c.refreshCh <- struct{}{}:
	default:
		// 已有待处理的刷新请求，跳过
	}
	go func() {
		if !c.refreshMu.TryLock() {
			return // another refresh already in progress
		}
		defer c.refreshMu.Unlock()

		// 刷新频率限制：距上次刷新不到 30 秒则跳过
		c.mu.RLock()
		lastRefresh := c.cacheTime
		c.mu.RUnlock()
		if time.Since(lastRefresh) < 30*time.Second {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.Refresh(ctx); err != nil {
			slog.Debug("Models.dev cache refresh failed", "error", err)
		}
	}()

	return nil
}

// ListModels 列出指定提供者的所有模型。
// provider 为空字符串时返回全部模型。
func (c *ModelsDevClient) ListModels(provider string) []*ModelDevInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []*ModelDevInfo
	for _, info := range c.cache {
		if provider == "" || info.Provider == provider {
			// 创建副本，避免外部修改缓存
			copy := *info
			result = append(result, &copy)
		}
	}
	return result
}

// IsVisionModel 检查指定模型是否支持视觉输入。
func (c *ModelsDevClient) IsVisionModel(modelID string) bool {
	info := c.GetModel(modelID)
	if info == nil {
		return false
	}
	return info.Vision
}

// Refresh 强制从 Models.dev API 刷新缓存。
// 如果 API 请求失败，保留现有缓存数据不变。
func (c *ModelsDevClient) Refresh(ctx context.Context) error {
	// 检查缓存是否仍在有效期内
	c.mu.RLock()
	age := time.Since(c.cacheTime)
	c.mu.RUnlock()

	if age < c.cacheTTL {
		return nil // 缓存仍有效，无需刷新
	}

	fetched, err := c.fetchFromAPI(ctx)
	if err != nil {
		slog.Warn("Models.dev API request failed, keeping existing cache", "error", err)
		return pkgerrors.Wrap(pkgerrors.NetworkHTTP, "models.dev API 请求失败", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 合并：API 数据覆盖内置数据，但保留 API 中没有的内置模型
	for id, info := range fetched {
		c.cache[id] = info
	}
	c.cacheTime = time.Now()

	slog.Info("Models.dev cache refreshed", "count", len(c.cache))
	return nil
}

