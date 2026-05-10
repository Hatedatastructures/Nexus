package agent

import (
	"errors"
	"testing"
)

// ───────────────────────────── TestClassifyAndRecoverContextOverflow ─────────────────────────────

func TestClassifyAndRecoverContextOverflow(t *testing.T) {
	tests := []struct {
		name       string
		errMsg     string
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
		name       string
		errMsg     string
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
		name       string
		errMsg     string
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
		name       string
		errMsg     string
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
