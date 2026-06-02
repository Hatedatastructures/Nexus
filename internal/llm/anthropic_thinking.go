// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"strings"
)

// ── 思维链配置 ────────────────────────────────────────────────────────────

// ThinkingType 思维链模式类型。
const (
	ThinkingTypeEnabled  = "enabled"  // 手动模式：需要指定 budget_tokens
	ThinkingTypeAuto     = "auto"     // 自适应模式：Claude 4.6+ 自动分配
	ThinkingTypeDisabled = "disabled" // 禁用思维链
)

// Effort 等级（用于自适应思维模式）。
const (
	EffortLow     = "low"
	EffortMedium  = "medium"
	EffortHigh    = "high"
	EffortXHigh   = "xhigh"
	EffortMax     = "max"
)

// ThinkingBudget 预定义的 token 预算映射。
var ThinkingBudget = map[string]int{
	EffortMax:   32000,
	EffortXHigh: 32000,
	EffortHigh:  16000,
	EffortMedium: 8000,
	EffortLow:    4000,
}

// AdaptiveEffortMap 将 Nexus effort 映射到 Anthropic 自适应 effort 级别。
var AdaptiveEffortMap = map[string]string{
	EffortMax:    "max",
	EffortXHigh:  "xhigh",
	EffortHigh:   "high",
	EffortMedium: "medium",
	EffortLow:    "low",
	"minimal":    "low",
}

// ── 模型检测 ──────────────────────────────────────────────────────────────

// adaptiveThinkingModels 列出支持自适应思维的模型子串（Claude 4.6+）。
var adaptiveThinkingModels = []string{
	"4-6", "4.6",
	"4-7", "4.7",
}

// xhighEffortModels 列出支持 xhigh effort 级别的模型子串（Claude 4.7+）。
var xhighEffortModels = []string{
	"4-7", "4.7",
}

// noSamplingParamsModels 列出禁止非默认 temperature/top_p 的模型（Claude 4.7+）。
var noSamplingParamsModels = []string{
	"4-7", "4.7",
}

// supportsAdaptiveThinking 检测模型是否支持自适应思维（Claude 4.6+）。
func supportsAdaptiveThinking(model string) bool {
	modelLower := strings.ToLower(model)
	for _, v := range adaptiveThinkingModels {
		if strings.Contains(modelLower, v) {
			return true
		}
	}
	return false
}

// supportsXHighEffort 检测模型是否支持 xhigh effort 级别（Claude 4.7+）。
func supportsXHighEffort(model string) bool {
	modelLower := strings.ToLower(model)
	for _, v := range xhighEffortModels {
		if strings.Contains(modelLower, v) {
			return true
		}
	}
	return false
}

// forbidsSamplingParams 检测模型是否禁止非默认采样参数。
func forbidsSamplingParams(model string) bool {
	modelLower := strings.ToLower(model)
	for _, v := range noSamplingParamsModels {
		if strings.Contains(modelLower, v) {
			return true
		}
	}
	return false
}

// SupportsExtendedThinking 检测模型是否支持扩展思维（仅限 Claude 非 Haiku 模型）。
func SupportsExtendedThinking(model string) bool {
	modelLower := strings.ToLower(model)
	if !strings.Contains(modelLower, "claude") {
		return false
	}
	if strings.Contains(modelLower, "haiku") {
		return false
	}
	return true
}

// ── ThinkingConfig ────────────────────────────────────────────────────────

// ThinkingConfig Anthropic 思维链配置。
type ThinkingConfig struct {
	// Type 思维链类型："enabled" / "auto" / "disabled"
	Type string `json:"type"`

	// BudgetTokens 手动模式下的 token 预算。
	BudgetTokens int `json:"budget_tokens,omitempty"`

	// Display 思维内容显示模式："summarized" / "omitted"（仅限自适应模式）。
	Display string `json:"display,omitempty"`

	// Effort 自适应思维 effort 等级："low" / "medium" / "high" / "xhigh" / "max"
	Effort string `json:"effort,omitempty"`
}

// BuildThinkingParam 根据 ThinkingConfig 和模型名称构建 Anthropic thinking 参数。
//
// 模型检测规则：
//   - Claude 4.7+: 自适应思维 (thinking: {type: "auto"}, output_config: {effort: "..."})
//   - Claude 4.6: 自适应思维（不支持 xhigh，降级为 max）
//   - Claude 4.0-4.5: 手动模式 (thinking: {type: "enabled", budget_tokens: N})
//   - Claude 3.x: 手动模式
//   - Haiku 系列: 不支持扩展思维
func BuildThinkingParam(cfg *ThinkingConfig, model string) map[string]any {
	if cfg == nil || cfg.Type == ThinkingTypeDisabled {
		return nil
	}

	if !SupportsExtendedThinking(model) {
		return nil
	}

	if supportsAdaptiveThinking(model) {
		return buildAdaptiveThinkingParam(cfg, model)
	}

	return buildManualThinkingParam(cfg)
}

// buildAdaptiveThinkingParam 构建自适应思维参数（Claude 4.6+）。
func buildAdaptiveThinkingParam(cfg *ThinkingConfig, model string) map[string]any {
	thinking := map[string]any{
		"type":    "auto",
		"display": "summarized",
	}

	if cfg.Display != "" {
		thinking["display"] = cfg.Display
	}

	// 确定 effort 等级
	effort := EffortMedium
	if cfg.Effort != "" {
		effort = strings.ToLower(cfg.Effort)
	}

	adaptiveEffort, ok := AdaptiveEffortMap[effort]
	if !ok {
		adaptiveEffort = "medium"
	}

	// 在不支持 xhigh 的模型上降级
	if adaptiveEffort == "xhigh" && !supportsXHighEffort(model) {
		adaptiveEffort = "max"
	}

	return map[string]any{
		"thinking":       thinking,
		"output_config": map[string]any{
			"effort": adaptiveEffort,
		},
	}
}

// buildManualThinkingParam 构建手动思维参数（Claude 4.5 及更早）。
func buildManualThinkingParam(cfg *ThinkingConfig) map[string]any {
	budget := cfg.BudgetTokens
	if budget <= 0 {
		// 从 Effort 推断预算
		effort := strings.ToLower(cfg.Effort)
		if budgetTokens, ok := ThinkingBudget[effort]; ok {
			budget = budgetTokens
		}
	}
	if budget <= 0 {
		budget = 8000 // 默认中等预算
	}

	return map[string]any{
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": budget,
		},
	}
}

// NewThinkingConfig 创建默认的思维链配置。
func NewThinkingConfig() *ThinkingConfig {
	return &ThinkingConfig{
		Type:   ThinkingTypeEnabled,
		Effort: EffortMedium,
	}
}

// WithAdaptiveThinking 创建自适应思维配置。
func WithAdaptiveThinking(effort string) *ThinkingConfig {
	if effort == "" {
		effort = EffortMedium
	}
	return &ThinkingConfig{
		Type:   ThinkingTypeAuto,
		Effort: effort,
	}
}

// WithManualThinking 创建手动思维配置。
func WithManualThinking(budgetTokens int) *ThinkingConfig {
	return &ThinkingConfig{
		Type:         ThinkingTypeEnabled,
		BudgetTokens: budgetTokens,
	}
}

// ResolveEffort 解析 effort 字符串，确保其在有效范围内。
func ResolveEffort(effort string) string {
	effort = strings.ToLower(effort)
	if _, ok := AdaptiveEffortMap[effort]; ok {
		return effort
	}
	return EffortMedium
}
