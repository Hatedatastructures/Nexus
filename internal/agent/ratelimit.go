// Package agent 提供速率限制追踪功能。
// 解析 LLM API 响应中的 x-ratelimit-* 头，并以 ASCII 进度条格式显示。
package agent

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ───────────────────────────── 数据结构 ─────────────────────────────

// RateLimitBucket 表示单个速率限制桶的状态。
type RateLimitBucket struct {
	Limit      int       // 总限额
	Remaining  int       // 剩余额度
	ResetSecs  float64   // 重置等待秒数
	CapturedAt time.Time // 采集时间
}

// RateLimitState 表示完整的速率限制状态（4 个维度）。
type RateLimitState struct {
	RequestsPerMin  RateLimitBucket
	RequestsPerHour RateLimitBucket
	TokensPerMin    RateLimitBucket
	TokensPerHour   RateLimitBucket
}

// ───────────────────────────── 解析函数 ─────────────────────────────

// ParseRateLimitHeaders 从 HTTP 响应头中解析速率限制信息。
// 支持大小写不敏感的 x-ratelimit-* 头。
func ParseRateLimitHeaders(h http.Header) RateLimitState {
	state := RateLimitState{}

	state.RequestsPerMin = parseBucket(h,
		"x-ratelimit-limit-requests",
		"x-ratelimit-remaining-requests",
		"x-ratelimit-reset-requests",
	)

	state.RequestsPerHour = parseBucket(h,
		"x-ratelimit-limit-requests-hour",
		"x-ratelimit-remaining-requests-hour",
		"x-ratelimit-reset-requests-hour",
	)

	state.TokensPerMin = parseBucket(h,
		"x-ratelimit-limit-tokens",
		"x-ratelimit-remaining-tokens",
		"x-ratelimit-reset-tokens",
	)

	state.TokensPerHour = parseBucket(h,
		"x-ratelimit-limit-tokens-hour",
		"x-ratelimit-remaining-tokens-hour",
		"x-ratelimit-reset-tokens-hour",
	)

	return state
}

// ParseRateLimitFromMap 从 map 中解析速率限制信息（适配不同 API 格式）。
func ParseRateLimitFromMap(headers map[string]string) RateLimitState {
	h := make(http.Header)
	for k, v := range headers {
		h.Set(k, v)
	}
	return ParseRateLimitHeaders(h)
}

func parseBucket(h http.Header, limitKey, remainingKey, resetKey string) RateLimitBucket {
	bucket := RateLimitBucket{CapturedAt: time.Now()}

	if v := getHeader(h, limitKey); v != "" {
		fmt.Sscanf(v, "%d", &bucket.Limit)
	}
	if v := getHeader(h, remainingKey); v != "" {
		fmt.Sscanf(v, "%d", &bucket.Remaining)
	}
	if v := getHeader(h, resetKey); v != "" {
		fmt.Sscanf(v, "%f", &bucket.ResetSecs)
		// 某些 API 返回的是 Unix 时间戳而非秒数差
		if bucket.ResetSecs > 1e10 {
			bucket.ResetSecs = bucket.ResetSecs - float64(time.Now().Unix())
		}
	}

	return bucket
}

// getHeader 大小写不敏感地获取 HTTP 头。
func getHeader(h http.Header, key string) string {
	// 先尝试标准形式
	if v := h.Get(key); v != "" {
		return v
	}
	// 再尝试小写
	return h.Get(strings.ToLower(key))
}

// ───────────────────────────── 格式化显示 ─────────────────────────────

// FormatRateLimitDisplay 将速率限制状态格式化为 ASCII 进度条。
func FormatRateLimitDisplay(state RateLimitState) string {
	var b strings.Builder

	b.WriteString("速率限制状态:\n")
	formatBucket(&b, "请求/分钟", state.RequestsPerMin)
	formatBucket(&b, "请求/小时", state.RequestsPerHour)
	formatBucket(&b, "Token/分钟", state.TokensPerMin)
	formatBucket(&b, "Token/小时", state.TokensPerHour)

	return b.String()
}

func formatBucket(b *strings.Builder, label string, bucket RateLimitBucket) {
	if bucket.Limit == 0 {
		return
	}

	used := bucket.Limit - bucket.Remaining
	pct := float64(used) / float64(bucket.Limit) * 100

	// 进度条
	barLen := 20
	filled := int(pct / 100 * float64(barLen))
	if filled > barLen {
		filled = barLen
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", barLen-filled)

	// 警告标记
	warn := ""
	if pct >= 90 {
		warn = " ⚠️ 危险"
	} else if pct >= 80 {
		warn = " ⚡ 警告"
	}

	b.WriteString(fmt.Sprintf("  %-12s [%s] %d/%d (%.0f%%) 重置: %.0fs%s\n",
		label, bar, used, bucket.Limit, pct, bucket.ResetSecs, warn))
}

// FormatRateLimitCompact 返回单行紧凑格式（适合日志）。
func FormatRateLimitCompact(state RateLimitState) string {
	parts := []string{}

	if state.RequestsPerMin.Limit > 0 {
		pct := float64(state.RequestsPerMin.Limit-state.RequestsPerMin.Remaining) / float64(state.RequestsPerMin.Limit) * 100
		parts = append(parts, fmt.Sprintf("req:%d/%d(%.0f%%)", state.RequestsPerMin.Remaining, state.RequestsPerMin.Limit, pct))
	}
	if state.TokensPerMin.Limit > 0 {
		pct := float64(state.TokensPerMin.Limit-state.TokensPerMin.Remaining) / float64(state.TokensPerMin.Limit) * 100
		parts = append(parts, fmt.Sprintf("tok:%d/%d(%.0f%%)", state.TokensPerMin.Remaining, state.TokensPerMin.Limit, pct))
	}

	if len(parts) == 0 {
		return "rate_limit: unknown"
	}
	return strings.Join(parts, " ")
}
