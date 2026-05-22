// Package agent 提供密钥脱敏功能。
// 自动检测并遮蔽日志和输出中的 API 密钥、令牌、密码等敏感信息。
package agent

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
)

// ───────────────────────────── 配置 ─────────────────────────────

// redactEnabled 使用原子操作保证并发安全。
var redactEnabled atomic.Bool

func init() {
	redactEnabled.Store(true)
}

// SetRedactEnabled 设置脱敏开关。
func SetRedactEnabled(enabled bool) {
	redactEnabled.Store(enabled)
}

// IsRedactEnabled 返回脱敏是否启用。
func IsRedactEnabled() bool {
	return redactEnabled.Load()
}

// ───────────────────────────── 模式定义 ─────────────────────────────

// prefixPattern 匹配已知 API 密钥前缀
type prefixPattern struct {
	prefix  string
	minLen  int // 密钥最小长度
}

var prefixPatterns = []prefixPattern{
	{"sk-", 20},
	{"sk-ant-", 20},
	{"ghp_", 30},
	{"github_pat_", 30},
	{"AIza", 30},
	{"AKIA", 20},
	{"xai-", 20},
	{"sk-proj-", 20},
	{"glpat-", 20},
	{"npm_", 20},
	{"pypi-", 20},
	{"hf_", 20},
	{"SG.", 20},
	{"xoxb-", 20},
	{"xoxp-", 20},
	{"xapp-", 20},
	{"Bot ", 20},
	{"Bearer ", 20},
}

// regexPatterns 匹配结构化敏感数据
var regexPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	// ENV 赋值: FOO_KEY=sk-abc123...
	{"env_assign", regexp.MustCompile(`(?i)([_a-z0-9]*(?:key|token|secret|password|api_key|apikey|access_token|auth)[_a-z0-9]*)\s*[:=]\s*['"]?([a-zA-Z0-9_\-\.]{20,})['"]?`)},
	// JSON 字段: "api_key": "sk-..."
	{"json_field", regexp.MustCompile(`(?i)"((?:api|secret|token|password|key|auth|credential|bearer)[_a-z0-9]*?)"\s*:\s*"([a-zA-Z0-9_\-\.]{20,})"`)},
	// Authorization header
	{"auth_header", regexp.MustCompile(`(?i)(Authorization|X-Api-Key)\s*[:=]\s*(Bearer\s+|Basic\s+|Token\s+)?([a-zA-Z0-9_\-\.]{20,})`)},
	// JWT
	{"jwt", regexp.MustCompile(`eyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}`)},
	// DB 连接串
	{"db_connstr", regexp.MustCompile(`(?i)(postgres|mysql|mongodb|redis|sqlite):[^\s]{20,}`)},
	// URL userinfo: http://user:pass@host
	{"url_userinfo", regexp.MustCompile(`([a-z]+://[^:]+:)([^@\s]{8,})(@)`)},
	// 手机号 (国际格式)
	{"phone", regexp.MustCompile(`\+[1-9]\d{6,14}`)},
}

// ───────────────────────────── 核心函数 ─────────────────────────────

const (
	prefixKeep = 6 // 保留前缀字符数
	suffixKeep = 4 // 保留后缀字符数
	minMaskLen = 18 // 触发遮蔽的最小长度
)

// RedactSensitiveText 对文本执行敏感信息遮蔽。
// 按顺序应用所有模式，保留部分前缀和后缀字符以便调试。
func RedactSensitiveText(text string) string {
	if !redactEnabled.Load() || text == "" {
		return text
	}

	result := text

	// 1. 前缀模式匹配
	for _, pp := range prefixPatterns {
		result = maskPrefixPattern(result, pp)
	}

	// 2. 正则模式匹配
	for _, rp := range regexPatterns {
		result = rp.re.ReplaceAllStringFunc(result, func(match string) string {
			return maskToken(match)
		})
	}

	return result
}

// maskPrefixPattern 遮蔽以特定前缀开头的令牌。
func maskPrefixPattern(text string, pp prefixPattern) string {
	var result strings.Builder
	remaining := text

	for {
		idx := strings.Index(remaining, pp.prefix)
		if idx == -1 {
			result.WriteString(remaining)
			break
		}

		// 写入前缀之前的内容
		result.WriteString(remaining[:idx])

		// 提取令牌
		start := idx
		end := start + len(pp.prefix)
		for end < len(remaining) && isTokenChar(remaining[end]) {
			end++
		}

		token := remaining[start:end]
		if len(token) >= minMaskLen {
			result.WriteString(maskToken(token))
		} else {
			result.WriteString(token)
		}

		remaining = remaining[end:]
	}

	return result.String()
}

// maskToken 遮蔽令牌，保留前缀和后缀。
func maskToken(token string) string {
	if len(token) < minMaskLen {
		return token
	}
	if len(token) <= prefixKeep+suffixKeep {
		return strings.Repeat("*", len(token))
	}
	return token[:prefixKeep] + strings.Repeat("*", len(token)-prefixKeep-suffixKeep) + token[len(token)-suffixKeep:]
}

// isTokenChar 判断字符是否属于令牌。
func isTokenChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
}

// ───────────────────────────── slog Handler ─────────────────────────────

// RedactingHandler 是一个 slog.Handler 包装器，自动对日志消息和属性执行脱敏。
type RedactingHandler struct {
	inner slog.Handler
}

// NewRedactingHandler 创建一个脱敏 slog handler。
func NewRedactingHandler(inner slog.Handler) *RedactingHandler {
	return &RedactingHandler{inner: inner}
}

// Enabled 委托给内部 handler。
func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle 对日志记录执行脱敏后委托给内部 handler。
func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	// 脱敏消息
	r.Message = RedactSensitiveText(r.Message)

	// 脱敏属性
	r.Attrs(func(a slog.Attr) bool {
		if a.Value.Kind() == slog.KindString {
			a.Value = slog.StringValue(RedactSensitiveText(a.Value.String()))
		}
		return true
	})

	return h.inner.Handle(ctx, r)
}

// WithAttrs 委托给内部 handler。
func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &RedactingHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup 委托给内部 handler。
func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{inner: h.inner.WithGroup(name)}
}
