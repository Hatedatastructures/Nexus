// Package llm 提供 LLM 提供者抽象层。
// models_dev_helpers.go 包含 Models.dev API 请求逻辑和内置模型数据表。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	pkgerrors "nexus-agent/internal/errors"
	"time"
)

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
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "构建请求失败", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "HTTP 请求失败", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, pkgerrors.New(pkgerrors.NetworkHTTP, fmt.Sprintf("API 返回非 200 状态码: %d", resp.StatusCode))
	}

	var entries []apiModelEntry
	if err := json.NewDecoder(io.LimitReader(resp.Body, 50<<20)).Decode(&entries); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "JSON 解析失败", err)
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

	// ── Qwen (通义千问) ────────────────────────────────────────────────
	{
		ID:            "qwen-plus",
		Provider:      "qwen",
		ContextWindow: 131072,
		MaxOutput:     8192,
		Vision:        true,
		Reasoning:     false,
		InputPrice:    0.4,
		OutputPrice:   1.2,
	},
	{
		ID:            "qwen-max",
		Provider:      "qwen",
		ContextWindow: 32768,
		MaxOutput:     8192,
		Vision:        true,
		Reasoning:     false,
		InputPrice:    2.4,
		OutputPrice:   9.6,
	},
	{
		ID:            "qwen3-plus",
		Provider:      "qwen",
		ContextWindow: 131072,
		MaxOutput:     8192,
		Vision:        true,
		Reasoning:     true,
		InputPrice:    0.4,
		OutputPrice:   1.2,
	},
	{
		ID:            "qwen3-max",
		Provider:      "qwen",
		ContextWindow: 32768,
		MaxOutput:     8192,
		Vision:        true,
		Reasoning:     true,
		InputPrice:    2.4,
		OutputPrice:   9.6,
	},
	{
		ID:            "qwq-plus",
		Provider:      "qwen",
		ContextWindow: 131072,
		MaxOutput:     16384,
		Vision:        false,
		Reasoning:     true,
		InputPrice:    0.4,
		OutputPrice:   1.2,
	},
}
