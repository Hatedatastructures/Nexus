package agent

import (
	"testing"
)

func TestNormalizeUsage_Anthropic(t *testing.T) {
	raw := map[string]any{
		"input_tokens":                float64(1000),
		"output_tokens":               float64(500),
		"cache_creation_input_tokens": float64(200),
		"cache_read_input_tokens":     float64(300),
	}

	u := NormalizeUsage("anthropic", raw)
	if u.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", u.InputTokens)
	}
	if u.OutputTokens != 500 {
		t.Errorf("OutputTokens = %d, want 500", u.OutputTokens)
	}
	if u.CacheCreationTokens != 200 {
		t.Errorf("CacheCreationTokens = %d, want 200", u.CacheCreationTokens)
	}
	if u.CacheReadTokens != 300 {
		t.Errorf("CacheReadTokens = %d, want 300", u.CacheReadTokens)
	}
}

func TestNormalizeUsage_OpenAI(t *testing.T) {
	raw := map[string]any{
		"prompt_tokens":     float64(800),
		"completion_tokens": float64(400),
		"prompt_tokens_details": map[string]any{
			"cached_tokens": float64(100),
		},
	}

	u := NormalizeUsage("openai", raw)
	if u.InputTokens != 800 {
		t.Errorf("InputTokens = %d, want 800", u.InputTokens)
	}
	if u.OutputTokens != 400 {
		t.Errorf("OutputTokens = %d, want 400", u.OutputTokens)
	}
	if u.CacheReadTokens != 100 {
		t.Errorf("CacheReadTokens = %d, want 100", u.CacheReadTokens)
	}
}

func TestEstimateCost(t *testing.T) {
	usage := CanonicalUsage{
		InputTokens:  1000000, // 1M tokens
		OutputTokens: 1000000,
	}

	result, err := EstimateCost("anthropic", "claude-sonnet-4-20250514", usage)
	if err != nil {
		t.Fatal(err)
	}

	// 1M input * $3/M = $3
	if result.InputCost < 2.9 || result.InputCost > 3.1 {
		t.Errorf("InputCost = %f, want ~3.0", result.InputCost)
	}
	// 1M output * $15/M = $15
	if result.OutputCost < 14.9 || result.OutputCost > 15.1 {
		t.Errorf("OutputCost = %f, want ~15.0", result.OutputCost)
	}
	// Total = $18
	if result.TotalCost < 17.9 || result.TotalCost > 18.1 {
		t.Errorf("TotalCost = %f, want ~18.0", result.TotalCost)
	}
}

func TestEstimateCost_UnknownModel(t *testing.T) {
	usage := CanonicalUsage{InputTokens: 1000}
	_, err := EstimateCost("unknown", "nonexistent-model", usage)
	if err == nil {
		t.Error("expected error for unknown model")
	}
}

func TestEstimateCost_CacheTokens(t *testing.T) {
	usage := CanonicalUsage{
		InputTokens:         1000000,
		OutputTokens:        0,
		CacheReadTokens:     1000000,
		CacheCreationTokens: 0,
	}

	result, err := EstimateCost("anthropic", "claude-sonnet-4-20250514", usage)
	if err != nil {
		t.Fatal(err)
	}

	// 1M input * $3/M = $3
	// 1M cache_read * $0.30/M = $0.30
	expected := 3.0 + 0.30
	if result.TotalCost < expected-0.01 || result.TotalCost > expected+0.01 {
		t.Errorf("TotalCost = %f, want ~%f", result.TotalCost, expected)
	}
}

func TestResolveBillingRoute(t *testing.T) {
	tests := []struct {
		name           string
		provider       string
		model          string
		baseURL        string
		wantOpenRouter bool
	}{
		{"openrouter", "openrouter", "gpt-4o", "https://openrouter.ai/api/v1", true},
		{"direct", "openai", "gpt-4o", "https://api.openai.com/v1", false},
		{"openrouter prefix", "openrouter", "openrouter/gpt-4o", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := ResolveBillingRoute(tt.provider, tt.model, tt.baseURL)
			if route.IsOpenRouter != tt.wantOpenRouter {
				t.Errorf("IsOpenRouter = %v, want %v", route.IsOpenRouter, tt.wantOpenRouter)
			}
		})
	}
}

func TestFormatCost(t *testing.T) {
	result := CostResult{
		InputCost:  1.5,
		OutputCost: 3.0,
		TotalCost:  4.5,
	}
	s := FormatCost(result)
	if s == "" {
		t.Error("expected non-empty formatted cost")
	}
}
