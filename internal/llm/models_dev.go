// Package llm 提供 LLM 提供者抽象层。
// models_dev.go 实现 Models.dev 模型数据库客户端，提供模型元数据查询能力。
// 当 Models.dev API 不可用时，自动回退到内置的常用模型硬编码列表。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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
	http      *http.Client             // HTTP 客户端
}

// NewModelsDevClient 创建 Models.dev 客户端实例。
// 默认缓存 TTL 为 1 小时，首次查询时自动加载数据。
func NewModelsDevClient() *ModelsDevClient {
	c := &ModelsDevClient{
		cache:    make(map[string]*ModelDevInfo),
		cacheTTL: 1 * time.Hour,
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
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.Refresh(ctx); err != nil {
			slog.Debug("Models.dev 缓存刷新失败", "error", err)
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
		slog.Warn("Models.dev API 请求失败，保留现有缓存", "error", err)
		return fmt.Errorf("models.dev API 请求失败: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 合并：API 数据覆盖内置数据，但保留 API 中没有的内置模型
	for id, info := range fetched {
		c.cache[id] = info
	}
	c.cacheTime = time.Now()

	slog.Info("Models.dev 缓存已刷新", "count", len(c.cache))
	return nil
}

// ───────────────────────────── API 请求 ─────────────────────────────

// apiModelEntry 描述 Models.dev API 返回的原始模型结构。
type apiModelEntry struct {
	ID            string  `json:"id"`
	Provider      string  `json:"provider"`
	ContextWindow int     `json:"context_window"`
	MaxOutput     int     `json:"max_output"`
	Vision        bool    `json:"vision"`
	Reasoning     bool    `json:"reasoning"`
	InputPrice    float64 `json:"input_price"`
	OutputPrice   float64 `json:"output_price"`
}

// fetchFromAPI 从 Models.dev API 拉取全量模型列表。
func (c *ModelsDevClient) fetchFromAPI(ctx context.Context) (map[string]*ModelDevInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevAPIURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构建请求失败: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API 返回非 200 状态码: %d", resp.StatusCode)
	}

	var entries []apiModelEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}

	result := make(map[string]*ModelDevInfo, len(entries))
	for _, e := range entries {
		result[e.ID] = &ModelDevInfo{
			ID:            e.ID,
			Provider:      e.Provider,
			ContextWindow: e.ContextWindow,
			MaxOutput:     e.MaxOutput,
			Vision:        e.Vision,
			Reasoning:     e.Reasoning,
			InputPrice:    e.InputPrice,
			OutputPrice:   e.OutputPrice,
		}
	}
	return result, nil
}

// ───────────────────────────── 内置模型列表 ─────────────────────────────

// loadBuiltinModels 加载内置的常用模型列表到缓存。
// 当 Models.dev API 不可用时，此列表作为回退数据源。
func (c *ModelsDevClient) loadBuiltinModels() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, m := range builtinModels {
		info := m // 创建副本
		c.cache[m.ID] = &info
	}
	c.cacheTime = time.Now()
}

// builtinModels 是内置的常用模型列表。
// 覆盖 Anthropic、OpenAI、Google、DeepSeek 等主流提供者的常用模型。
var builtinModels = []ModelDevInfo{
	// ── Anthropic ──────────────────────────────────────────────────
	{
		ID:            "claude-opus-4-20250514",
		Provider:      "anthropic",
		ContextWindow: 200000,
		MaxOutput:     32000,
		Vision:        true,
		Reasoning:     true,
		InputPrice:    15.0,
		OutputPrice:   75.0,
	},
	{
		ID:            "claude-sonnet-4-20250514",
		Provider:      "anthropic",
		ContextWindow: 200000,
		MaxOutput:     16000,
		Vision:        true,
		Reasoning:     true,
		InputPrice:    3.0,
		OutputPrice:   15.0,
	},
	{
		ID:            "claude-3-5-haiku-20241022",
		Provider:      "anthropic",
		ContextWindow: 200000,
		MaxOutput:     8192,
		Vision:        true,
		Reasoning:     false,
		InputPrice:    1.0,
		OutputPrice:   5.0,
	},
	{
		ID:            "claude-3-5-sonnet-20241022",
		Provider:      "anthropic",
		ContextWindow: 200000,
		MaxOutput:     8192,
		Vision:        true,
		Reasoning:     false,
		InputPrice:    3.0,
		OutputPrice:   15.0,
	},
	{
		ID:            "claude-3-opus-20240229",
		Provider:      "anthropic",
		ContextWindow: 200000,
		MaxOutput:     4096,
		Vision:        true,
		Reasoning:     false,
		InputPrice:    15.0,
		OutputPrice:   75.0,
	},
	{
		ID:            "claude-3-haiku-20240307",
		Provider:      "anthropic",
		ContextWindow: 200000,
		MaxOutput:     4096,
		Vision:        true,
		Reasoning:     false,
		InputPrice:    0.25,
		OutputPrice:   1.25,
	},

	// ── OpenAI ─────────────────────────────────────────────────────
	{
		ID:            "gpt-4o",
		Provider:      "openai",
		ContextWindow: 128000,
		MaxOutput:     16384,
		Vision:        true,
		Reasoning:     false,
		InputPrice:    2.5,
		OutputPrice:   10.0,
	},
	{
		ID:            "gpt-4o-mini",
		Provider:      "openai",
		ContextWindow: 128000,
		MaxOutput:     16384,
		Vision:        true,
		Reasoning:     false,
		InputPrice:    0.15,
		OutputPrice:   0.6,
	},
	{
		ID:            "gpt-4-turbo",
		Provider:      "openai",
		ContextWindow: 128000,
		MaxOutput:     4096,
		Vision:        true,
		Reasoning:     false,
		InputPrice:    10.0,
		OutputPrice:   30.0,
	},
	{
		ID:            "o1",
		Provider:      "openai",
		ContextWindow: 200000,
		MaxOutput:     100000,
		Vision:        true,
		Reasoning:     true,
		InputPrice:    15.0,
		OutputPrice:   60.0,
	},
	{
		ID:            "o1-mini",
		Provider:      "openai",
		ContextWindow: 128000,
		MaxOutput:     65536,
		Vision:        false,
		Reasoning:     true,
		InputPrice:    3.0,
		OutputPrice:   12.0,
	},
	{
		ID:            "o3",
		Provider:      "openai",
		ContextWindow: 200000,
		MaxOutput:     100000,
		Vision:        true,
		Reasoning:     true,
		InputPrice:    10.0,
		OutputPrice:   40.0,
	},
	{
		ID:            "o3-mini",
		Provider:      "openai",
		ContextWindow: 200000,
		MaxOutput:     100000,
		Vision:        false,
		Reasoning:     true,
		InputPrice:    1.1,
		OutputPrice:   4.4,
	},
	{
		ID:            "o4-mini",
		Provider:      "openai",
		ContextWindow: 200000,
		MaxOutput:     100000,
		Vision:        true,
		Reasoning:     true,
		InputPrice:    1.1,
		OutputPrice:   4.4,
	},

	// ── Google ─────────────────────────────────────────────────────
	{
		ID:            "gemini-2.5-pro",
		Provider:      "google",
		ContextWindow: 1048576,
		MaxOutput:     65536,
		Vision:        true,
		Reasoning:     true,
		InputPrice:    1.25,
		OutputPrice:   10.0,
	},
	{
		ID:            "gemini-2.5-flash",
		Provider:      "google",
		ContextWindow: 1048576,
		MaxOutput:     65536,
		Vision:        true,
		Reasoning:     true,
		InputPrice:    0.15,
		OutputPrice:   0.6,
	},
	{
		ID:            "gemini-2.0-flash",
		Provider:      "google",
		ContextWindow: 1048576,
		MaxOutput:     8192,
		Vision:        true,
		Reasoning:     false,
		InputPrice:    0.1,
		OutputPrice:   0.4,
	},
	{
		ID:            "gemini-1.5-pro",
		Provider:      "google",
		ContextWindow: 2097152,
		MaxOutput:     8192,
		Vision:        true,
		Reasoning:     false,
		InputPrice:    1.25,
		OutputPrice:   5.0,
	},
	{
		ID:            "gemini-1.5-flash",
		Provider:      "google",
		ContextWindow: 1048576,
		MaxOutput:     8192,
		Vision:        true,
		Reasoning:     false,
		InputPrice:    0.075,
		OutputPrice:   0.3,
	},

	// ── DeepSeek ───────────────────────────────────────────────────
	{
		ID:            "deepseek-chat",
		Provider:      "deepseek",
		ContextWindow: 65536,
		MaxOutput:     8192,
		Vision:        false,
		Reasoning:     false,
		InputPrice:    0.14,
		OutputPrice:   0.28,
	},
	{
		ID:            "deepseek-reasoner",
		Provider:      "deepseek",
		ContextWindow: 65536,
		MaxOutput:     16384,
		Vision:        false,
		Reasoning:     true,
		InputPrice:    0.55,
		OutputPrice:   2.19,
	},

	// ── Mistral ────────────────────────────────────────────────────
	{
		ID:            "mistral-large-latest",
		Provider:      "mistral",
		ContextWindow: 131072,
		MaxOutput:     8192,
		Vision:        false,
		Reasoning:     false,
		InputPrice:    2.0,
		OutputPrice:   6.0,
	},
	{
		ID:            "mistral-small-latest",
		Provider:      "mistral",
		ContextWindow: 131072,
		MaxOutput:     8192,
		Vision:        false,
		Reasoning:     false,
		InputPrice:    0.1,
		OutputPrice:   0.3,
	},
	{
		ID:            "codestral-latest",
		Provider:      "mistral",
		ContextWindow: 131072,
		MaxOutput:     8192,
		Vision:        false,
		Reasoning:     false,
		InputPrice:    0.3,
		OutputPrice:   0.9,
	},
}
