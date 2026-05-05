package llm

import (
	"testing"
)

func TestClassifyError_HTTPStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantReason string
		wantRetry  bool
		wantFall   bool
	}{
		{"401 auth", 401, "Unauthorized", ReasonAuth, false, true},
		{"403 auth", 403, "Forbidden", ReasonAuth, false, true},
		{"402 billing", 402, "Insufficient credits", ReasonBilling, false, true},
		{"402 rate limit transient", 402, "usage limit exceeded, try again in 60s", ReasonRateLimit, true, true},
		{"404 model not found", 404, "model not found", ReasonModelNotFound, false, true},
		{"404 unknown", 404, "Not Found", ReasonUnknown, true, false},
		{"413 payload too large", 413, "Request too large", ReasonPayloadTooLarge, true, false},
		{"429 rate limit", 429, "Too many requests", ReasonRateLimit, true, true},
		{"400 context overflow", 400, "context length exceeded", ReasonContextOverflow, true, false},
		{"400 model not found", 400, "is not a valid model", ReasonModelNotFound, false, true},
		{"400 format error", 400, "Invalid request format", ReasonFormatError, false, true},
		{"500 server error", 500, "Internal Server Error", ReasonServerError, true, false},
		{"502 server error", 502, "Bad Gateway", ReasonServerError, true, false},
		{"503 overloaded", 503, "Service Unavailable", ReasonOverloaded, true, false},
		{"529 overloaded", 529, "Overloaded", ReasonOverloaded, true, false},
		{"400 thinking signature", 400, "signature thinking mismatch", ReasonFormatError, true, false},
		{"429 long context tier", 429, "extra usage long context tier", ReasonRateLimit, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyError(tt.statusCode, tt.body)
			if got.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.wantReason)
			}
			if got.Retryable != tt.wantRetry {
				t.Errorf("Retryable = %v, want %v", got.Retryable, tt.wantRetry)
			}
			if got.ShouldFallback != tt.wantFall {
				t.Errorf("ShouldFallback = %v, want %v", got.ShouldFallback, tt.wantFall)
			}
		})
	}
}

func TestClassifyError_MessagePatterns(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantReason string
	}{
		{"billing insufficient", "insufficient_quota exceeded", ReasonBilling},
		{"rate limit", "rate limit exceeded for requests per minute", ReasonRateLimit},
		{"context overflow", "maximum context length exceeded", ReasonContextOverflow},
		{"auth", "invalid api key provided", ReasonAuth},
		{"model not found", "model does not exist", ReasonModelNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyError(0, tt.body)
			if got.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.wantReason)
			}
		})
	}
}

func TestClassifyFromError(t *testing.T) {
	tests := []struct {
		name       string
		errMsg     string
		wantReason string
	}{
		{"429 in message", "status code 429: rate limit", ReasonRateLimit},
		{"401 in message", "HTTP 401: unauthorized", ReasonAuth},
		{"context overflow", "context length exceeded: too many tokens", ReasonContextOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &testError{msg: tt.errMsg}
			got := ClassifyFromError(err)
			if got.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.wantReason)
			}
		})
	}
}

func TestExtractHTTPStatus(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"HTTP 429 Too Many Requests", 429},
		{"status: 401 unauthorized", 401},
		{"error 500 internal", 500},
		{"no status code here", 0},
		{"200 OK", 0}, // 2xx not extracted
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ExtractHTTPStatus(tt.input)
			if got != tt.want {
				t.Errorf("ExtractHTTPStatus(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestContainsAny(t *testing.T) {
	tests := []struct {
		s        string
		patterns []string
		want     bool
	}{
		{"hello world", []string{"world", "foo"}, true},
		{"hello world", []string{"foo", "bar"}, false},
		{"", []string{"foo"}, false},
		{"hello", []string{}, false},
	}

	for _, tt := range tests {
		got := containsAny(tt.s, tt.patterns)
		if got != tt.want {
			t.Errorf("containsAny(%q, %v) = %v, want %v", tt.s, tt.patterns, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s      string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello"},
		{"", 5, ""},
	}

	for _, tt := range tests {
		got := truncate(tt.s, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
		}
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
