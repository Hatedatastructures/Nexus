package agent

import (
	"errors"
	"testing"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── TestClassifyAndRecoverContextOverflow ─────────────────────────────

func TestClassifyAndRecoverContextOverflow(t *testing.T) {
	tests := []struct {
		name         string
		errMsg       string
		wantStrategy string
	}{
		{
			name:         "context length exceeded",
			errMsg:       "400: context length exceeded",
			wantStrategy: StrategyCompressAndRetry,
		},
		{
			name:         "token limit",
			errMsg:       "400: too many tokens",
			wantStrategy: StrategyCompressAndRetry,
		},
		{
			name:         "prompt is too long",
			errMsg:       "400: prompt is too long",
			wantStrategy: StrategyCompressAndRetry,
		},
		{
			name:         "context window exceeded",
			errMsg:       "400: context window exceeded",
			wantStrategy: StrategyCompressAndRetry,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := NewRecoveryEngine()
			action := engine.ClassifyAndRecover(errors.New(tt.errMsg))

			if action.Strategy != tt.wantStrategy {
				t.Errorf("策略 = %q, want %q (message: %s)", action.Strategy, tt.wantStrategy, action.Message)
			}
		})
	}
}

// ───────────────────────────── TestClassifyAndRecoverRateLimit ─────────────────────────────

func TestClassifyAndRecoverRateLimit(t *testing.T) {
	tests := []struct {
		name         string
		errMsg       string
		wantStrategy string
	}{
		{
			name:         "rate limit 429",
			errMsg:       "429: rate limit exceeded",
			wantStrategy: StrategyWaitAndRetry,
		},
		{
			name:         "too many requests",
			errMsg:       "429: too many requests",
			wantStrategy: StrategyWaitAndRetry,
		},
		{
			name:         "throttled",
			errMsg:       "429: throttled",
			wantStrategy: StrategyWaitAndRetry,
		},
		{
			name:         "retry after",
			errMsg:       "429: try again in 30 seconds",
			wantStrategy: StrategyWaitAndRetry,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := NewRecoveryEngine()
			action := engine.ClassifyAndRecover(errors.New(tt.errMsg))

			if action.Strategy != tt.wantStrategy {
				t.Errorf("策略 = %q, want %q (message: %s)", action.Strategy, tt.wantStrategy, action.Message)
			}
		})
	}
}

// ───────────────────────────── TestClassifyAndRecoverAuth ─────────────────────────────

func TestClassifyAndRecoverAuth(t *testing.T) {
	tests := []struct {
		name         string
		errMsg       string
		wantStrategy string
	}{
		{
			name:         "invalid api key",
			errMsg:       "401: invalid api key",
			wantStrategy: StrategyRotateCredential,
		},
		{
			name:         "unauthorized",
			errMsg:       "401: unauthorized",
			wantStrategy: StrategyRotateCredential,
		},
		{
			name:         "forbidden",
			errMsg:       "403: forbidden",
			wantStrategy: StrategyRotateCredential,
		},
		{
			name:         "authentication failed",
			errMsg:       "401: authentication failed",
			wantStrategy: StrategyRotateCredential,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := NewRecoveryEngine()
			action := engine.ClassifyAndRecover(errors.New(tt.errMsg))

			if action.Strategy != tt.wantStrategy {
				t.Errorf("策略 = %q, want %q (message: %s)", action.Strategy, tt.wantStrategy, action.Message)
			}
		})
	}
}

// ───────────────────────────── TestClassifyAndRecoverUnknown ─────────────────────────────

func TestClassifyAndRecoverUnknown(t *testing.T) {
	tests := []struct {
		name         string
		errMsg       string
		wantStrategy string
	}{
		{
			name:         "完全未知的错误",
			errMsg:       "something completely unexpected",
			wantStrategy: StrategyAbort,
		},
		{
			name:         "无法分类的错误",
			errMsg:       "random error message without any pattern",
			wantStrategy: StrategyAbort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := NewRecoveryEngine()
			action := engine.ClassifyAndRecover(errors.New(tt.errMsg))

			if action.Strategy != tt.wantStrategy {
				t.Errorf("策略 = %q, want %q (message: %s)", action.Strategy, tt.wantStrategy, action.Message)
			}
		})
	}

	t.Run("nil 错误返回 abort", func(t *testing.T) {
		engine := NewRecoveryEngine()
		action := engine.ClassifyAndRecover(nil)

		if action.Strategy != StrategyAbort {
			t.Errorf("nil 错误策略 = %q, want %q", action.Strategy, StrategyAbort)
		}
	})
}

// ───────────────────────────── TestClassifyAndRecoverOthers ─────────────────────────────

func TestClassifyAndRecoverOthers(t *testing.T) {
	tests := []struct {
		name         string
		errMsg       string
		wantStrategy string
	}{
		{
			name:         "服务器错误 500",
			errMsg:       "500: internal server error",
			wantStrategy: StrategyWaitAndRetry,
		},
		{
			name:         "服务过载 503",
			errMsg:       "503: service overloaded",
			wantStrategy: StrategyWaitAndRetry,
		},
		{
			name:         "payload too large 413",
			errMsg:       "413: request entity too large",
			wantStrategy: StrategyTruncateAndRetry,
		},
		{
			name:         "模型不存在",
			errMsg:       "404: model not found",
			wantStrategy: StrategyFallbackModel,
		},
		{
			name:         "计费耗尽",
			errMsg:       "402: insufficient credits",
			wantStrategy: StrategyRotateCredential,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := NewRecoveryEngine()
			action := engine.ClassifyAndRecover(errors.New(tt.errMsg))

			if action.Strategy != tt.wantStrategy {
				t.Errorf("策略 = %q, want %q (message: %s)", action.Strategy, tt.wantStrategy, action.Message)
			}
		})
	}
}

// ───────────────────────── AddRecipe 测试 ─────────────────────────

func TestAddRecipe(t *testing.T) {
	engine := NewRecoveryEngine()
	baseCount := len(engine.recipes)

	custom := RecoveryRecipe{
		Name:     "custom_handler",
		Priority: 0, // 最高优先级
		Condition: func(_ error, classified *llm.ClassifiedError) bool {
			return classified.Reason == llm.ReasonUnknown
		},
		Build: func(_ error, classified *llm.ClassifiedError) RecoveryAction {
			return RecoveryAction{Strategy: StrategyAbort, Message: "custom handled"}
		},
	}
	engine.AddRecipe(custom)

	if len(engine.recipes) != baseCount+1 {
		t.Fatalf("recipes = %d, want %d", len(engine.recipes), baseCount+1)
	}

	// 自定义配方优先级最高 (Priority=0)，应排在最前面
	if engine.recipes[0].Name != "custom_handler" {
		t.Errorf("first recipe = %q, want custom_handler", engine.recipes[0].Name)
	}
}

func TestAddRecipe_SortedByPriority(t *testing.T) {
	engine := NewRecoveryEngine()

	engine.AddRecipe(RecoveryRecipe{Name: "low", Priority: 100})
	engine.AddRecipe(RecoveryRecipe{Name: "high", Priority: 1})
	engine.AddRecipe(RecoveryRecipe{Name: "mid", Priority: 50})

	// 找到自定义配方，验证相对顺序
	var names []string
	for _, r := range engine.recipes {
		if r.Name == "low" || r.Name == "high" || r.Name == "mid" {
			names = append(names, r.Name)
		}
	}
	if len(names) != 3 {
		t.Fatalf("custom recipes = %v, want 3 entries", names)
	}
	if names[0] != "high" || names[1] != "mid" || names[2] != "low" {
		t.Errorf("order = %v, want [high mid low]", names)
	}
}

func TestAddRecipe_CustomRecipeMatches(t *testing.T) {
	engine := NewRecoveryEngine()
	called := false

	engine.AddRecipe(RecoveryRecipe{
		Name:     "catch_all",
		Priority: -1, // 比所有内置配方优先级更高
		Condition: func(_ error, classified *llm.ClassifiedError) bool {
			return true
		},
		Build: func(_ error, classified *llm.ClassifiedError) RecoveryAction {
			called = true
			return RecoveryAction{Strategy: StrategyAbort, Message: "custom"}
		},
	})

	action := engine.ClassifyAndRecover(errors.New("any error"))
	if !called {
		t.Error("custom recipe should have been called")
	}
	if action.Message != "custom" {
		t.Errorf("message = %q, want custom", action.Message)
	}
}
