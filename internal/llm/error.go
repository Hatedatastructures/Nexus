// Package llm 提供 LLM 提供者抽象层。
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

// ── 分类函数 ──────────────────────────────────────────────────────────────

// ClassifyFromError 从 error 中提取 HTTP 状态码并分类。
// 这是统一的错误分类入口，内部调用 ClassifyError。
func ClassifyFromError(err error) *ClassifiedError {
	if err == nil {
		return &ClassifiedError{Reason: ReasonUnknown, Retryable: true}
	}
	statusCode := ExtractHTTPStatus(err.Error())
	return ClassifyError(statusCode, err.Error())
}

// ExtractHTTPStatus 从错误字符串中提取 HTTP 状态码。
// 匹配 400-599 范围内的三位数字。
func ExtractHTTPStatus(errStr string) int {
	for i := 0; i+2 < len(errStr); i++ {
		if isDigit(errStr[i]) && isDigit(errStr[i+1]) && isDigit(errStr[i+2]) {
			code := (int(errStr[i]-'0'))*100 + (int(errStr[i+1]-'0'))*10 + (int(errStr[i+2]) - '0')
			if code >= 400 && code < 600 {
				return code
			}
		}
	}
	return 0
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// ClassifyError 按 HTTP 状态码 + 消息内容模式匹配分类 API 错误。
//
// 分类优先级:
//  1. Anthropic 特有模式（thinking signature、long context tier）
//  2. HTTP 状态码分类
//  3. 消息体模式匹配
//  4. 未知错误（默认可重试）
func ClassifyError(statusCode int, body string) *ClassifiedError {
	bodyLower := strings.ToLower(body)

	return classifyErrorInternal(statusCode, bodyLower)
}

// classifyErrorInternal 内部分类逻辑。
func classifyErrorInternal(statusCode int, bodyLower string) *ClassifiedError {
	msg := truncate(bodyLower, 500)

	// ── 1. Anthropic 特有模式 ──────────────────────────────────────
	if statusCode == 400 {
		if containsAll(bodyLower, thinkingSigPatterns) {
			return &ClassifiedError{
				Reason:         ReasonFormatError,
				StatusCode:     statusCode,
				Retryable:      true,
				ShouldCompress: false,
				Message:        msg,
			}
		}
	}

	if statusCode == 429 {
		if containsAll(bodyLower, longContextTierPatterns) {
			return &ClassifiedError{
				Reason:         ReasonRateLimit,
				StatusCode:     statusCode,
				Retryable:      true,
				ShouldCompress: true,
				Message:        msg,
			}
		}
	}

	// ── 2. HTTP 状态码分类 ────────────────────────────────────────

	switch statusCode {
	case 401:
		return &ClassifiedError{
			Reason:         ReasonAuth,
			StatusCode:     statusCode,
			Retryable:      false,
			ShouldFallback: true,
			Message:        msg,
		}

	case 403:
		return &ClassifiedError{
			Reason:         ReasonAuth,
			StatusCode:     statusCode,
			Retryable:      false,
			ShouldFallback: true,
			Message:        msg,
		}

	case 402:
		return classify402(bodyLower, statusCode, msg)

	case 404:
		return classify404(bodyLower, statusCode, msg)

	case 413:
		return &ClassifiedError{
			Reason:         ReasonPayloadTooLarge,
			StatusCode:     statusCode,
			Retryable:      true,
			ShouldCompress: true,
			Message:        msg,
		}

	case 429:
		return &ClassifiedError{
			Reason:         ReasonRateLimit,
			StatusCode:     statusCode,
			Retryable:      true,
			ShouldFallback: true,
			Message:        msg,
		}

	case 400:
		return classify400(bodyLower, statusCode, msg)

	case 500, 502:
		return &ClassifiedError{
			Reason:     ReasonServerError,
			StatusCode: statusCode,
			Retryable:  true,
			Message:    msg,
		}

	case 503, 529:
		return &ClassifiedError{
			Reason:     ReasonOverloaded,
			StatusCode: statusCode,
			Retryable:  true,
			Message:    msg,
		}
	}

	// 其他 4xx
	if statusCode >= 400 && statusCode < 500 {
		return &ClassifiedError{
			Reason:         ReasonFormatError,
			StatusCode:     statusCode,
			Retryable:      false,
			ShouldFallback: true,
			Message:        msg,
		}
	}

	// 其他 5xx: 检查是否为请求验证错误
	if statusCode >= 500 && statusCode < 600 {
		// 某些提供者对无效请求返回 500 而非 400
		if strings.Contains(bodyLower, "invalid_request") ||
			strings.Contains(bodyLower, "request_validation") ||
			strings.Contains(bodyLower, "invalid x-api-key") ||
			strings.Contains(bodyLower, "invalid api key") {
			return &ClassifiedError{
				Reason:         ReasonFormatError,
				StatusCode:     statusCode,
				Retryable:      false,
				ShouldFallback: false,
				Message:        msg,
			}
		}
		return &ClassifiedError{
			Reason:     ReasonServerError,
			StatusCode: statusCode,
			Retryable:  true,
			Message:    msg,
		}
	}

	// ── 3. 消息体模式匹配（无状态码时） ──────────────────────────

	if ce := classifyByMessage(bodyLower); ce != nil {
		ce.StatusCode = statusCode
		if ce.Message == "" {
			ce.Message = msg
		}
		return ce
	}

	// ── 4. 未知错误 ──────────────────────────────────────────────

	return &ClassifiedError{
		Reason:     ReasonUnknown,
		StatusCode: statusCode,
		Retryable:  true,
		Message:    msg,
	}
}

// classify402 区分 402：计费耗尽 vs 临时配额限制。
func classify402(bodyLower string, statusCode int, msg string) *ClassifiedError {
	// 检查是否有临时信号（"try again", "reset" 等）
	usageLimitWords := []string{"usage limit", "quota", "limit exceeded"}
	hasUsageLimit := containsAny(bodyLower, usageLimitWords)
	transientWords := []string{"try again", "retry", "resets at", "reset in", "wait", "periodic", "window"}
	hasTransient := containsAny(bodyLower, transientWords)

	if hasUsageLimit && hasTransient {
		return &ClassifiedError{
			Reason:         ReasonRateLimit,
			StatusCode:     statusCode,
			Retryable:      true,
			ShouldFallback: true,
			Message:        msg,
		}
	}

	return &ClassifiedError{
		Reason:         ReasonBilling,
		StatusCode:     statusCode,
		Retryable:      false,
		ShouldFallback: true,
		Message:        msg,
	}
}

// classify404 分类 404 错误。
func classify404(bodyLower string, statusCode int, msg string) *ClassifiedError {
	if containsAny(bodyLower, modelNotFoundPatterns) {
		return &ClassifiedError{
			Reason:         ReasonModelNotFound,
			StatusCode:     statusCode,
			Retryable:      false,
			ShouldFallback: true,
			Message:        msg,
		}
	}
	return &ClassifiedError{
		Reason:     ReasonUnknown,
		StatusCode: statusCode,
		Retryable:  true,
		Message:    msg,
	}
}

// classify400 分类 400 错误。
func classify400(bodyLower string, statusCode int, msg string) *ClassifiedError {
	// 上下文溢出
	if containsAny(bodyLower, contextOverflowPatterns) {
		return &ClassifiedError{
			Reason:         ReasonContextOverflow,
			StatusCode:     statusCode,
			Retryable:      true,
			ShouldCompress: true,
			Message:        msg,
		}
	}

	// 模型不存在（某些提供者返回 400）
	if containsAny(bodyLower, modelNotFoundPatterns) {
		return &ClassifiedError{
			Reason:         ReasonModelNotFound,
			StatusCode:     statusCode,
			Retryable:      false,
			ShouldFallback: true,
			Message:        msg,
		}
	}

	// 速率限制（某些提供者返回 400）
	if containsAny(bodyLower, rateLimitPatterns) {
		return &ClassifiedError{
			Reason:         ReasonRateLimit,
			StatusCode:     statusCode,
			Retryable:      true,
			ShouldFallback: true,
			Message:        msg,
		}
	}

	// 计费错误（某些提供者返回 400）
	if containsAny(bodyLower, billingPatterns) {
		return &ClassifiedError{
			Reason:         ReasonBilling,
			StatusCode:     statusCode,
			Retryable:      false,
			ShouldFallback: true,
			Message:        msg,
		}
	}

	// 通用格式错误
	return &ClassifiedError{
		Reason:         ReasonFormatError,
		StatusCode:     statusCode,
		Retryable:      false,
		ShouldFallback: true,
		Message:        msg,
	}
}

// classifyByMessage 纯消息模式匹配分类（无状态码时使用）。
func classifyByMessage(bodyLower string) *ClassifiedError {
	// 计费
	if containsAny(bodyLower, billingPatterns) {
		return &ClassifiedError{
			Reason:     ReasonBilling,
			Retryable:  false,
			ShouldFallback: true,
		}
	}

	// 速率限制
	if containsAny(bodyLower, rateLimitPatterns) {
		return &ClassifiedError{
			Reason:     ReasonRateLimit,
			Retryable:  true,
			ShouldFallback: true,
		}
	}

	// 上下文溢出
	if containsAny(bodyLower, contextOverflowPatterns) {
		return &ClassifiedError{
			Reason:         ReasonContextOverflow,
			Retryable:      true,
			ShouldCompress: true,
		}
	}

	// 认证
	if containsAny(bodyLower, authPatterns) {
		return &ClassifiedError{
			Reason:     ReasonAuth,
			Retryable:  false,
			ShouldFallback: true,
		}
	}

	// 模型不存在
	if containsAny(bodyLower, modelNotFoundPatterns) {
		return &ClassifiedError{
			Reason:     ReasonModelNotFound,
			Retryable:  false,
			ShouldFallback: true,
		}
	}

	return nil
}

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
