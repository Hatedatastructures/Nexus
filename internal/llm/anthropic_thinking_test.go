package llm

import (
	"testing"
)

// ───────────────────────────── supportsAdaptiveThinking ─────────────────────────────

func TestSupportsAdaptiveThinking(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4-6-20250514", true},
		{"claude-sonnet-4.6", true},
		{"claude-opus-4-7-20250610", true},
		{"claude-opus-4.7", true},
		{"claude-3-5-sonnet", false},
		{"claude-3-opus", false},
		{"gpt-4o", false},
		{"", false},
	}
	for _, tt := range tests {
		got := supportsAdaptiveThinking(tt.model)
		if got != tt.want {
			t.Errorf("supportsAdaptiveThinking(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

// ───────────────────────────── supportsXHighEffort ─────────────────────────────

func TestSupportsXHighEffort(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-opus-4-7-20250610", true},
		{"claude-opus-4.7", true},
		{"claude-sonnet-4-6-20250514", false},
		{"claude-3-5-sonnet", false},
	}
	for _, tt := range tests {
		got := supportsXHighEffort(tt.model)
		if got != tt.want {
			t.Errorf("supportsXHighEffort(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

// ───────────────────────────── forbidsSamplingParams ─────────────────────────────

func TestForbidsSamplingParams(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-opus-4-7-20250610", true},
		{"claude-opus-4.7", true},
		{"claude-sonnet-4-6-20250514", false},
		{"claude-3-5-sonnet", false},
	}
	for _, tt := range tests {
		got := forbidsSamplingParams(tt.model)
		if got != tt.want {
			t.Errorf("forbidsSamplingParams(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

// ───────────────────────────── SupportsExtendedThinking ─────────────────────────────

func TestSupportsExtendedThinking(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4-6-20250514", true},
		{"claude-opus-4-7-20250610", true},
		{"claude-3-5-sonnet", true},
		{"claude-haiku-4-5", false},
		{"claude-3-haiku", false},
		{"Claude-Haiku-3", false},
		{"gpt-4o", false},
		{"deepseek-chat", false},
		{"", false},
	}
	for _, tt := range tests {
		got := SupportsExtendedThinking(tt.model)
		if got != tt.want {
			t.Errorf("SupportsExtendedThinking(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

// ───────────────────────────── BuildThinkingParam ─────────────────────────────

func TestBuildThinkingParam_Nil(t *testing.T) {
	result := BuildThinkingParam(nil, "claude-sonnet-4-6")
	if result != nil {
		t.Error("nil config should return nil")
	}
}

func TestBuildThinkingParam_Disabled(t *testing.T) {
	cfg := &ThinkingConfig{Type: ThinkingTypeDisabled}
	result := BuildThinkingParam(cfg, "claude-sonnet-4-6")
	if result != nil {
		t.Error("disabled config should return nil")
	}
}

func TestBuildThinkingParam_NonClaude(t *testing.T) {
	cfg := NewThinkingConfig()
	result := BuildThinkingParam(cfg, "gpt-4o")
	if result != nil {
		t.Error("non-Claude model should return nil")
	}
}

func TestBuildThinkingParam_AdaptiveModel(t *testing.T) {
	cfg := WithAdaptiveThinking(EffortHigh)
	result := BuildThinkingParam(cfg, "claude-sonnet-4-6")
	if result == nil {
		t.Fatal("should not be nil for adaptive model")
	}
	thinking, ok := result["thinking"].(map[string]any)
	if !ok {
		t.Fatal("missing thinking key")
	}
	if thinking["type"] != "auto" {
		t.Errorf("thinking type = %v, want auto", thinking["type"])
	}
	outputCfg, ok := result["output_config"].(map[string]any)
	if !ok {
		t.Fatal("missing output_config key")
	}
	if outputCfg["effort"] != "high" {
		t.Errorf("effort = %v, want high", outputCfg["effort"])
	}
}

func TestBuildThinkingParam_AdaptiveXHighDowngrade(t *testing.T) {
	cfg := WithAdaptiveThinking(EffortXHigh)
	result := BuildThinkingParam(cfg, "claude-sonnet-4-6")
	if result == nil {
		t.Fatal("should not be nil")
	}
	outputCfg := result["output_config"].(map[string]any)
	if outputCfg["effort"] != "max" {
		t.Errorf("xhigh on 4.6 should downgrade to max, got %v", outputCfg["effort"])
	}
}

func TestBuildThinkingParam_AdaptiveXHighSupported(t *testing.T) {
	cfg := WithAdaptiveThinking(EffortXHigh)
	result := BuildThinkingParam(cfg, "claude-opus-4-7")
	if result == nil {
		t.Fatal("should not be nil")
	}
	outputCfg := result["output_config"].(map[string]any)
	if outputCfg["effort"] != "xhigh" {
		t.Errorf("xhigh on 4.7 should stay xhigh, got %v", outputCfg["effort"])
	}
}

func TestBuildThinkingParam_ManualModel(t *testing.T) {
	cfg := WithManualThinking(10000)
	result := BuildThinkingParam(cfg, "claude-3-5-sonnet")
	if result == nil {
		t.Fatal("should not be nil for manual model")
	}
	thinking := result["thinking"].(map[string]any)
	if thinking["type"] != "enabled" {
		t.Errorf("type = %v, want enabled", thinking["type"])
	}
	if thinking["budget_tokens"] != 10000 {
		t.Errorf("budget = %v, want 10000", thinking["budget_tokens"])
	}
}

func TestBuildThinkingParam_ManualFromEffort(t *testing.T) {
	cfg := &ThinkingConfig{
		Type:   ThinkingTypeEnabled,
		Effort: EffortHigh,
	}
	result := BuildThinkingParam(cfg, "claude-3-5-sonnet")
	if result == nil {
		t.Fatal("should not be nil")
	}
	thinking := result["thinking"].(map[string]any)
	if thinking["budget_tokens"] != 16000 {
		t.Errorf("budget from effort high = %v, want 16000", thinking["budget_tokens"])
	}
}

func TestBuildThinkingParam_ManualDefaultBudget(t *testing.T) {
	cfg := &ThinkingConfig{
		Type: ThinkingTypeEnabled,
	}
	result := BuildThinkingParam(cfg, "claude-3-5-sonnet")
	if result == nil {
		t.Fatal("should not be nil")
	}
	thinking := result["thinking"].(map[string]any)
	if thinking["budget_tokens"] != 8000 {
		t.Errorf("default budget = %v, want 8000", thinking["budget_tokens"])
	}
}

func TestBuildThinkingParam_AdaptiveDisplayOverride(t *testing.T) {
	cfg := &ThinkingConfig{
		Type:    ThinkingTypeAuto,
		Effort:  EffortMedium,
		Display: "omitted",
	}
	result := BuildThinkingParam(cfg, "claude-sonnet-4-6")
	thinking := result["thinking"].(map[string]any)
	if thinking["display"] != "omitted" {
		t.Errorf("display = %v, want omitted", thinking["display"])
	}
}

func TestBuildThinkingParam_AdaptiveInvalidEffort(t *testing.T) {
	cfg := &ThinkingConfig{
		Type:   ThinkingTypeAuto,
		Effort: "invalid",
	}
	result := BuildThinkingParam(cfg, "claude-sonnet-4-6")
	outputCfg := result["output_config"].(map[string]any)
	if outputCfg["effort"] != "medium" {
		t.Errorf("invalid effort should fallback to medium, got %v", outputCfg["effort"])
	}
}

// ───────────────────────────── 构造函数 ─────────────────────────────

func TestNewThinkingConfig(t *testing.T) {
	cfg := NewThinkingConfig()
	if cfg.Type != ThinkingTypeEnabled {
		t.Errorf("Type = %q, want %q", cfg.Type, ThinkingTypeEnabled)
	}
	if cfg.Effort != EffortMedium {
		t.Errorf("Effort = %q, want %q", cfg.Effort, EffortMedium)
	}
}

func TestWithAdaptiveThinking(t *testing.T) {
	cfg := WithAdaptiveThinking(EffortHigh)
	if cfg.Type != ThinkingTypeAuto {
		t.Errorf("Type = %q, want %q", cfg.Type, ThinkingTypeAuto)
	}
	if cfg.Effort != EffortHigh {
		t.Errorf("Effort = %q, want %q", cfg.Effort, EffortHigh)
	}
}

func TestWithAdaptiveThinking_Empty(t *testing.T) {
	cfg := WithAdaptiveThinking("")
	if cfg.Effort != EffortMedium {
		t.Errorf("empty effort should default to medium, got %q", cfg.Effort)
	}
}

func TestWithManualThinking(t *testing.T) {
	cfg := WithManualThinking(16000)
	if cfg.Type != ThinkingTypeEnabled {
		t.Errorf("Type = %q, want %q", cfg.Type, ThinkingTypeEnabled)
	}
	if cfg.BudgetTokens != 16000 {
		t.Errorf("BudgetTokens = %d, want 16000", cfg.BudgetTokens)
	}
}

// ───────────────────────────── ResolveEffort ─────────────────────────────

func TestResolveEffort(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"high", "high"},
		{"HIGH", "high"},
		{"medium", "medium"},
		{"low", "low"},
		{"max", "max"},
		{"xhigh", "xhigh"},
		{"minimal", "minimal"},
		{"invalid", EffortMedium},
		{"", EffortMedium},
	}
	for _, tt := range tests {
		got := ResolveEffort(tt.input)
		if got != tt.want {
			t.Errorf("ResolveEffort(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
