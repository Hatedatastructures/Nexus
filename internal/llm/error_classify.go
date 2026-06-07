// Package llm 提供 LLM 提供者抽象层。
// error_classify.go 实现错误分类逻辑：按 HTTP 状态码和消息模式匹配将 API 错误归类。
package llm

import (
	"strings"
)

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
// 仅匹配独立的三位数字 (前后不能有其他数字)，避免误匹配更大数字中的子串。
func ExtractHTTPStatus(errStr string) int {
	for i := 0; i+2 < len(errStr); i++ {
		if !isDigit(errStr[i]) {
			continue
		}
		if i > 0 && isDigit(errStr[i-1]) {
			continue
		}
		if i+3 < len(errStr) && isDigit(errStr[i+3]) {
			continue
		}
		if isDigit(errStr[i+1]) && isDigit(errStr[i+2]) {
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
		if containsAny(bodyLower, []string{"invalid_request", "request_validation", "invalid x-api-key", "invalid api key"}) {
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
	if containsAny(bodyLower, contextOverflowPatterns) {
		return &ClassifiedError{
			Reason:         ReasonContextOverflow,
			StatusCode:     statusCode,
			Retryable:      true,
			ShouldCompress: true,
			Message:        msg,
		}
	}

	if containsAny(bodyLower, modelNotFoundPatterns) {
		return &ClassifiedError{
			Reason:         ReasonModelNotFound,
			StatusCode:     statusCode,
			Retryable:      false,
			ShouldFallback: true,
			Message:        msg,
		}
	}

	if containsAny(bodyLower, rateLimitPatterns) {
		return &ClassifiedError{
			Reason:         ReasonRateLimit,
			StatusCode:     statusCode,
			Retryable:      true,
			ShouldFallback: true,
			Message:        msg,
		}
	}

	if containsAny(bodyLower, billingPatterns) {
		return &ClassifiedError{
			Reason:         ReasonBilling,
			StatusCode:     statusCode,
			Retryable:      false,
			ShouldFallback: true,
			Message:        msg,
		}
	}

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
	if containsAny(bodyLower, billingPatterns) {
		return &ClassifiedError{
			Reason:         ReasonBilling,
			Retryable:      false,
			ShouldFallback: true,
		}
	}

	if containsAny(bodyLower, rateLimitPatterns) {
		return &ClassifiedError{
			Reason:         ReasonRateLimit,
			Retryable:      true,
			ShouldFallback: true,
		}
	}

	if containsAny(bodyLower, contextOverflowPatterns) {
		return &ClassifiedError{
			Reason:         ReasonContextOverflow,
			Retryable:      true,
			ShouldCompress: true,
		}
	}

	if containsAny(bodyLower, authPatterns) {
		return &ClassifiedError{
			Reason:         ReasonAuth,
			Retryable:      false,
			ShouldFallback: true,
		}
	}

	if containsAny(bodyLower, modelNotFoundPatterns) {
		return &ClassifiedError{
			Reason:         ReasonModelNotFound,
			Retryable:      false,
			ShouldFallback: true,
		}
	}

	return nil
}
