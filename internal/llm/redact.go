package llm

import (
	"regexp"
	"strings"
)

const maxErrorBodyLen = 500

var redactPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[a-zA-Z0-9\-_]{8,}`),
	regexp.MustCompile(`(?i)Bearer\s+[^\s"']+`),
	regexp.MustCompile(`(?i)["']?api[_-]?key["']?\s*[=:]\s*["']?[^\s"']+`),
	regexp.MustCompile(`(?i)["']?secret[_-]?key["']?\s*[=:]\s*["']?[^\s"']+`),
	regexp.MustCompile(`(?i)["']?access[_-]?token["']?\s*[=:]\s*["']?[^\s"']+`),
	regexp.MustCompile(`(?i)key[=:]\s*[a-zA-Z0-9\-_]{16,}`),
}

// RedactErrorBody 对 HTTP 响应体进行脱敏处理，
// 移除可能被 API 服务端回传的密钥和凭证信息。
func RedactErrorBody(body string) string {
	if len(body) > maxErrorBodyLen {
		body = body[:maxErrorBodyLen] + "...(truncated)"
	}
	for _, p := range redactPatterns {
		body = p.ReplaceAllString(body, "[REDACTED]")
	}
	return body
}

// safeBody 返回脱敏后的响应体，用于错误消息。
func safeBody(body []byte) string {
	return RedactErrorBody(string(body))
}

// classifyAndRedact 对 HTTP 错误响应进行分类并脱敏。
func classifyAndRedact(statusCode int, body []byte) (reason string, safeBodyStr string) {
	bodyStr := string(body)
	classified := ClassifyError(statusCode, bodyStr)
	return classified.Reason, RedactErrorBody(bodyStr)
}

// containsString 检查切片中是否包含指定字符串。
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
