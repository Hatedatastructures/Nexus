package llm

import (
	"regexp"
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
