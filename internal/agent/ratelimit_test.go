package agent

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseRateLimitHeaders_Full(t *testing.T) {
	h := http.Header{}
	h.Set("x-ratelimit-limit-requests", "60")
	h.Set("x-ratelimit-remaining-requests", "45")
	h.Set("x-ratelimit-reset-requests", "30")
	h.Set("x-ratelimit-limit-requests-hour", "1000")
	h.Set("x-ratelimit-remaining-requests-hour", "800")
	h.Set("x-ratelimit-reset-requests-hour", "1800")
	h.Set("x-ratelimit-limit-tokens", "100000")
	h.Set("x-ratelimit-remaining-tokens", "75000")
	h.Set("x-ratelimit-reset-tokens", "45")
	h.Set("x-ratelimit-limit-tokens-hour", "500000")
	h.Set("x-ratelimit-remaining-tokens-hour", "400000")
	h.Set("x-ratelimit-reset-tokens-hour", "2700")

	state := ParseRateLimitHeaders(h)

	if state.RequestsPerMin.Limit != 60 {
		t.Errorf("RequestsPerMin.Limit = %d, want 60", state.RequestsPerMin.Limit)
	}
	if state.RequestsPerMin.Remaining != 45 {
		t.Errorf("RequestsPerMin.Remaining = %d, want 45", state.RequestsPerMin.Remaining)
	}
	if state.RequestsPerMin.ResetSecs != 30 {
		t.Errorf("RequestsPerMin.ResetSecs = %f, want 30", state.RequestsPerMin.ResetSecs)
	}
	if state.RequestsPerHour.Limit != 1000 {
		t.Errorf("RequestsPerHour.Limit = %d, want 1000", state.RequestsPerHour.Limit)
	}
	if state.TokensPerMin.Limit != 100000 {
		t.Errorf("TokensPerMin.Limit = %d, want 100000", state.TokensPerMin.Limit)
	}
	if state.TokensPerHour.Limit != 500000 {
		t.Errorf("TokensPerHour.Limit = %d, want 500000", state.TokensPerHour.Limit)
	}
}

func TestParseRateLimitHeaders_Empty(t *testing.T) {
	h := http.Header{}
	state := ParseRateLimitHeaders(h)

	if state.RequestsPerMin.Limit != 0 {
		t.Errorf("empty headers: Limit = %d, want 0", state.RequestsPerMin.Limit)
	}
	if state.RequestsPerMin.Remaining != 0 {
		t.Errorf("empty headers: Remaining = %d, want 0", state.RequestsPerMin.Remaining)
	}
}

func TestParseRateLimitHeaders_UnixTimestamp(t *testing.T) {
	futureTs := float64(time.Now().Unix() + 120)
	h := http.Header{}
	h.Set("x-ratelimit-limit-requests", "60")
	h.Set("x-ratelimit-remaining-requests", "30")
	h.Set("x-ratelimit-reset-requests", fmt.Sprintf("%.0f", futureTs))

	state := ParseRateLimitHeaders(h)
	if state.RequestsPerMin.ResetSecs < 100 || state.RequestsPerMin.ResetSecs > 140 {
		t.Errorf("ResetSecs from Unix ts = %f, want ~120", state.RequestsPerMin.ResetSecs)
	}
}

func TestParseRateLimitHeaders_InvalidValues(t *testing.T) {
	h := http.Header{}
	h.Set("x-ratelimit-limit-requests", "not-a-number")
	h.Set("x-ratelimit-remaining-requests", "also-not")
	h.Set("x-ratelimit-reset-requests", "bad")

	state := ParseRateLimitHeaders(h)
	if state.RequestsPerMin.Limit != 0 {
		t.Errorf("invalid limit should be 0, got %d", state.RequestsPerMin.Limit)
	}
	if state.RequestsPerMin.Remaining != 0 {
		t.Errorf("invalid remaining should be 0, got %d", state.RequestsPerMin.Remaining)
	}
}

func TestParseRateLimitHeaders_NegativeReset(t *testing.T) {
	h := http.Header{}
	h.Set("x-ratelimit-limit-requests", "10")
	h.Set("x-ratelimit-remaining-requests", "5")
	h.Set("x-ratelimit-reset-requests", "-5")

	state := ParseRateLimitHeaders(h)
	if state.RequestsPerMin.ResetSecs != 0 {
		t.Errorf("negative reset should be clamped to 0, got %f", state.RequestsPerMin.ResetSecs)
	}
}

func TestParseRateLimitFromMap(t *testing.T) {
	headers := map[string]string{
		"x-ratelimit-limit-requests":     "100",
		"x-ratelimit-remaining-requests": "80",
		"x-ratelimit-reset-requests":     "60",
	}

	state := ParseRateLimitFromMap(headers)
	if state.RequestsPerMin.Limit != 100 {
		t.Errorf("from map: Limit = %d, want 100", state.RequestsPerMin.Limit)
	}
	if state.RequestsPerMin.Remaining != 80 {
		t.Errorf("from map: Remaining = %d, want 80", state.RequestsPerMin.Remaining)
	}
}

func TestGetHeader_CaseInsensitive(t *testing.T) {
	h := http.Header{}
	h.Set("X-RateLimit-Limit-Requests", "50")

	v := getHeader(h, "x-ratelimit-limit-requests")
	if v != "50" {
		t.Errorf("getHeader case insensitive: got %q, want %q", v, "50")
	}
}

func TestFormatRateLimitDisplay(t *testing.T) {
	state := RateLimitState{
		RequestsPerMin: RateLimitBucket{Limit: 60, Remaining: 30, ResetSecs: 45},
		TokensPerMin:   RateLimitBucket{Limit: 1000, Remaining: 200, ResetSecs: 30},
	}

	out := FormatRateLimitDisplay(state)
	if !strings.Contains(out, "请求/分钟") {
		t.Error("display should contain 请求/分钟")
	}
	if !strings.Contains(out, "Token/分钟") {
		t.Error("display should contain Token/分钟")
	}
	if !strings.Contains(out, "50%") {
		t.Error("display should contain 50% for requests")
	}
}

func TestFormatRateLimitDisplay_HighUsage(t *testing.T) {
	state := RateLimitState{
		RequestsPerMin: RateLimitBucket{Limit: 100, Remaining: 5, ResetSecs: 10},
	}

	out := FormatRateLimitDisplay(state)
	if !strings.Contains(out, "危险") {
		t.Error(">= 90%% usage should show 危险")
	}
}

func TestFormatRateLimitDisplay_Warning(t *testing.T) {
	state := RateLimitState{
		RequestsPerMin: RateLimitBucket{Limit: 100, Remaining: 15, ResetSecs: 10},
	}

	out := FormatRateLimitDisplay(state)
	if !strings.Contains(out, "警告") {
		t.Error(">= 80%% usage should show 警告")
	}
}

func TestFormatRateLimitDisplay_Empty(t *testing.T) {
	state := RateLimitState{}
	out := FormatRateLimitDisplay(state)
	if !strings.Contains(out, "速率限制状态") {
		t.Error("should contain header even when empty")
	}
}

func TestFormatRateLimitCompact(t *testing.T) {
	state := RateLimitState{
		RequestsPerMin: RateLimitBucket{Limit: 60, Remaining: 30, ResetSecs: 45},
		TokensPerMin:   RateLimitBucket{Limit: 1000, Remaining: 800, ResetSecs: 30},
	}

	out := FormatRateLimitCompact(state)
	if !strings.Contains(out, "req:") {
		t.Error("compact should contain req:")
	}
	if !strings.Contains(out, "tok:") {
		t.Error("compact should contain tok:")
	}
}

func TestFormatRateLimitCompact_Empty(t *testing.T) {
	state := RateLimitState{}
	out := FormatRateLimitCompact(state)
	if out != "rate_limit: unknown" {
		t.Errorf("empty compact: got %q", out)
	}
}

func TestFormatBucket_ZeroLimit(t *testing.T) {
	var b strings.Builder
	formatBucket(&b, "test", RateLimitBucket{Limit: 0})
	if b.Len() > 0 {
		t.Error("zero-limit bucket should produce no output")
	}
}
