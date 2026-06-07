// Package llm 提供 LLM 提供者抽象层。
// error.go 定义错误分类类型、常量、模式匹配列表及辅助函数。
package llm

import (
	"strings"
)

// ClassifiedError 分类后的 API 错误，包含恢复建议。
type ClassifiedError struct {
	// Reason 为分类原因标识（"auth" / "rate_limit" / "context_overflow" / "server_error" / "billing" / "unknown" 等）。
	Reason string

	// StatusCode 为 HTTP 状态码（0 表示无）。
	StatusCode int

	// Retryable 表示该错误是否可重试。
	Retryable bool

	// ShouldCompress 表示是否需要压缩上下文。
	ShouldCompress bool

	// ShouldFallback 表示是否应切换备选提供者。
	ShouldFallback bool

	// Message 为原始错误消息（截断至 500 字符）。
	Message string
}

// 分类原因常量
const (
	ReasonAuth            = "auth"
	ReasonBilling         = "billing"
	ReasonRateLimit       = "rate_limit"
	ReasonServerError     = "server_error"
	ReasonOverloaded      = "overloaded"
	ReasonTimeout         = "timeout"
	ReasonContextOverflow = "context_overflow"
	ReasonPayloadTooLarge = "payload_too_large"
	ReasonModelNotFound   = "model_not_found"
	ReasonFormatError     = "format_error"
	ReasonUnknown         = "unknown"
)

// ── 模式匹配列表 ──────────────────────────────────────────────────────────

// 计费耗尽模式
var billingPatterns = []string{
	"insufficient credits",
	"insufficient_quota",
	"credit balance",
	"credits have been exhausted",
	"top up your credits",
	"payment required",
	"billing hard limit",
	"exceeded your current quota",
	"account is deactivated",
	"plan does not include",
}

// 速率限制模式
var rateLimitPatterns = []string{
	"rate limit",
	"rate_limit",
	"too many requests",
	"throttled",
	"requests per minute",
	"tokens per minute",
	"requests per day",
	"try again in",
	"please retry after",
	"resource_exhausted",
	"throttlingexception",
	"too many concurrent requests",
	"servicequotaexceededexception",
}

// 上下文溢出模式
var contextOverflowPatterns = []string{
	"context length",
	"context size",
	"maximum context",
	"token limit",
	"too many tokens",
	"reduce the length",
	"exceeds the limit",
	"context window",
	"prompt is too long",
	"prompt exceeds max length",
	"max_tokens",
	"maximum number of tokens",
	"exceeds the max_model_len",
	"max_model_len",
	"prompt length",
	"input is too long",
	"maximum model length",
	"context length exceeded",
	"truncating input",
	"slot context",
	"n_ctx_slot",
	"exceeds the maximum number of input tokens",
}

// 认证错误模式
var authPatterns = []string{
	"invalid api key",
	"invalid_api_key",
	"authentication",
	"unauthorized",
	"forbidden",
	"invalid token",
	"token expired",
	"token revoked",
	"access denied",
}

// 模型不存在模式
var modelNotFoundPatterns = []string{
	"is not a valid model",
	"invalid model",
	"model not found",
	"model_not_found",
	"does not exist",
	"no such model",
	"unknown model",
	"unsupported model",
}

// Anthropic 特有思维链签名错误
var thinkingSigPatterns = []string{"signature", "thinking"}
var longContextTierPatterns = []string{"extra usage", "long context"}

// ── 辅助函数 ──────────────────────────────────────────────────────────────

// containsAny 检查字符串是否包含任意一个模式。
func containsAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// containsAll 检查字符串是否包含所有模式。
func containsAll(s string, patterns []string) bool {
	for _, p := range patterns {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return len(patterns) > 0
}

// truncate 截断字符串至指定最大长度。
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
