// Package agent 提供 Token 用量定价计算功能。
// 统一处理 Anthropic / OpenAI / Codex 等不同 API 的 usage 格式，
// 并基于静态定价表计算 USD 成本。
package agent

import (
	"fmt"
	"strings"
)

// ───────────────────────────── 数据结构 ─────────────────────────────

// CanonicalUsage 统一格式的 Token 用量。
type CanonicalUsage struct {
	InputTokens         int64 // 输入 token 数
	OutputTokens        int64 // 输出 token 数
	CacheCreationTokens int64 // 缓存创建 token 数
	CacheReadTokens     int64 // 缓存读取 token 数
}

// PricingEntry 单个模型的定价条目（每百万 token 的 USD 价格）。
type PricingEntry struct {
	InputPerMToken         float64
	OutputPerMToken        float64
	CacheReadPerMToken     float64
	CacheCreationPerMToken float64
}

// CostResult 成本计算结果。
type CostResult struct {
	InputCost         float64
	OutputCost        float64
	CacheCreationCost float64
	CacheReadCost     float64
	TotalCost         float64
	Pricing           PricingEntry
}

// BillingRoute 计费路由信息。
type BillingRoute struct {
	Provider     string
	Model        string
	BaseURL      string
	IsOpenRouter bool
}

// ───────────────────────────── 定价表 ─────────────────────────────

// officialDocsPricing 基于官方文档的静态定价快照（每百万 token USD 价格）。
var officialDocsPricing = map[string]PricingEntry{
	// Anthropic
	"anthropic:claude-sonnet-4-20250514":   {InputPerMToken: 3.0, OutputPerMToken: 15.0, CacheReadPerMToken: 0.30, CacheCreationPerMToken: 3.75},
	"anthropic:claude-opus-4-20250514":     {InputPerMToken: 15.0, OutputPerMToken: 75.0, CacheReadPerMToken: 1.50, CacheCreationPerMToken: 18.75},
	"anthropic:claude-haiku-4-5-20251001":  {InputPerMToken: 0.80, OutputPerMToken: 4.0, CacheReadPerMToken: 0.08, CacheCreationPerMToken: 1.0},
	"anthropic:claude-3-5-sonnet-20241022": {InputPerMToken: 3.0, OutputPerMToken: 15.0, CacheReadPerMToken: 0.30, CacheCreationPerMToken: 3.75},
	"anthropic:claude-3-5-haiku-20241022":  {InputPerMToken: 0.80, OutputPerMToken: 4.0, CacheReadPerMToken: 0.08, CacheCreationPerMToken: 1.0},
	"anthropic:claude-3-opus-20240229":     {InputPerMToken: 15.0, OutputPerMToken: 75.0, CacheReadPerMToken: 1.50, CacheCreationPerMToken: 18.75},

	// OpenAI
	"openai:gpt-4o":        {InputPerMToken: 2.50, OutputPerMToken: 10.0},
	"openai:gpt-4o-mini":   {InputPerMToken: 0.15, OutputPerMToken: 0.60},
	"openai:gpt-4-turbo":   {InputPerMToken: 10.0, OutputPerMToken: 30.0},
	"openai:gpt-4":         {InputPerMToken: 30.0, OutputPerMToken: 60.0},
	"openai:gpt-3.5-turbo": {InputPerMToken: 0.50, OutputPerMToken: 1.50},
	"openai:o1":            {InputPerMToken: 15.0, OutputPerMToken: 60.0},
	"openai:o1-mini":       {InputPerMToken: 3.0, OutputPerMToken: 12.0},
	"openai:o3-mini":       {InputPerMToken: 1.10, OutputPerMToken: 4.40},

	// Google
	"google:gemini-2.5-pro":   {InputPerMToken: 1.25, OutputPerMToken: 10.0},
	"google:gemini-2.5-flash": {InputPerMToken: 0.15, OutputPerMToken: 0.60},
	"google:gemini-2.0-flash": {InputPerMToken: 0.10, OutputPerMToken: 0.40},

	// DeepSeek
	"deepseek:deepseek-chat":     {InputPerMToken: 0.14, OutputPerMToken: 0.28},
	"deepseek:deepseek-reasoner": {InputPerMToken: 0.55, OutputPerMToken: 2.19},

	// Qwen
	"qwen:qwen-max":   {InputPerMToken: 1.60, OutputPerMToken: 6.40},
	"qwen:qwen-plus":  {InputPerMToken: 0.40, OutputPerMToken: 1.20},
	"qwen:qwen-turbo": {InputPerMToken: 0.05, OutputPerMToken: 0.20},

	// Moonshot
	"moonshot:moonshot-v1-128k": {InputPerMToken: 1.26, OutputPerMToken: 1.26},

	// GLM
	"zhipu:glm-4-plus": {InputPerMToken: 0.70, OutputPerMToken: 0.70},

	// Codex
	"codex:codex-mini": {InputPerMToken: 1.50, OutputPerMToken: 6.0},
}

// ───────────────────────────── 核心函数 ─────────────────────────────

// NormalizeUsage 将不同 API 的 usage 响应统一为 CanonicalUsage。
func NormalizeUsage(provider string, raw map[string]any) CanonicalUsage {
	u := CanonicalUsage{}

	switch {
	case strings.Contains(provider, "anthropic"):
		u.InputTokens = int64(getFloat(raw, "input_tokens"))
		u.OutputTokens = int64(getFloat(raw, "output_tokens"))
		if cache, ok := raw["cache_creation_input_tokens"]; ok {
			u.CacheCreationTokens = int64(getFloat(cache))
		}
		if cache, ok := raw["cache_read_input_tokens"]; ok {
			u.CacheReadTokens = int64(getFloat(cache))
		}

	default: // OpenAI 兼容格式
		u.InputTokens = int64(getFloat(raw, "prompt_tokens"))
		u.OutputTokens = int64(getFloat(raw, "completion_tokens"))
		if cache, ok := raw["prompt_tokens_details"]; ok {
			if details, ok := cache.(map[string]any); ok {
				u.CacheReadTokens = int64(getFloat(details, "cached_tokens"))
			}
		}
	}

	return u
}

// ResolveBillingRoute 解析计费路由。
func ResolveBillingRoute(provider, model, baseURL string) BillingRoute {
	route := BillingRoute{
		Provider: provider,
		Model:    model,
		BaseURL:  baseURL,
	}

	// OpenRouter 检测
	if strings.Contains(baseURL, "openrouter.ai") || strings.HasPrefix(model, "openrouter/") {
		route.IsOpenRouter = true
	}

	// Bedrock ARN 解析
	if strings.Contains(provider, "bedrock") && strings.Contains(model, "arn:") {
		// 从 ARN 中提取模型名
		parts := strings.Split(model, "/")
		if len(parts) > 0 {
			route.Model = parts[len(parts)-1]
		}
	}

	return route
}

// EstimateCost 计算 Token 用量的 USD 成本。
func EstimateCost(provider, model string, usage CanonicalUsage) (CostResult, error) {
	key := provider + ":" + model

	// 精确匹配
	entry, ok := officialDocsPricing[key]
	if !ok {
		// 模糊匹配: 逐步去掉最后的 "-" 后缀，优先最长匹配
		baseModel := model
		for {
			idx := strings.LastIndex(baseModel, "-")
			if idx <= 0 {
				break
			}
			baseModel = baseModel[:idx]
			entry, ok = officialDocsPricing[provider+":"+baseModel]
			if ok {
				break
			}
		}
		if !ok {
			return CostResult{}, fmt.Errorf("未找到模型 %s:%s 的定价信息", provider, model)
		}
	}

	return calculateCost(entry, usage), nil
}

func calculateCost(entry PricingEntry, usage CanonicalUsage) CostResult {
	r := CostResult{Pricing: entry}

	r.InputCost = float64(usage.InputTokens) / 1e6 * entry.InputPerMToken
	r.OutputCost = float64(usage.OutputTokens) / 1e6 * entry.OutputPerMToken
	r.CacheCreationCost = float64(usage.CacheCreationTokens) / 1e6 * entry.CacheCreationPerMToken
	r.CacheReadCost = float64(usage.CacheReadTokens) / 1e6 * entry.CacheReadPerMToken
	r.TotalCost = r.InputCost + r.OutputCost + r.CacheCreationCost + r.CacheReadCost

	return r
}

// FormatCost 将成本结果格式化为可读字符串。
func FormatCost(r CostResult) string {
	return fmt.Sprintf("$%.6f (input: $%.6f, output: $%.6f, cache_read: $%.6f, cache_create: $%.6f)",
		r.TotalCost, r.InputCost, r.OutputCost, r.CacheReadCost, r.CacheCreationCost)
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func getFloat(v any, keys ...string) float64 {
	current := v
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return 0
		}
		current = m[key]
	}

	switch val := current.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	default:
		return 0
	}
}
