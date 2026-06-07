package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── DefaultClientConfig ─────────────────────────────────────────────────────

func TestDefaultClientConfig(t *testing.T) {
	cfg := DefaultClientConfig()
	if cfg.ConnectTimeout != 30*time.Second {
		t.Errorf("ConnectTimeout = %v, want 30s", cfg.ConnectTimeout)
	}
	if cfg.ReadTimeout != 600*time.Second {
		t.Errorf("ReadTimeout = %v, want 600s", cfg.ReadTimeout)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.RetryBackoff != 1*time.Second {
		t.Errorf("RetryBackoff = %v, want 1s", cfg.RetryBackoff)
	}
	if cfg.MaxRetryBackoff != 60*time.Second {
		t.Errorf("MaxRetryBackoff = %v, want 60s", cfg.MaxRetryBackoff)
	}
	if cfg.MaxIdleConns != 100 {
		t.Errorf("MaxIdleConns = %d, want 100", cfg.MaxIdleConns)
	}
	if cfg.MaxIdleConnsPerHost != 10 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 10", cfg.MaxIdleConnsPerHost)
	}
	if cfg.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 90s", cfg.IdleConnTimeout)
	}
}

// ── NewHTTPClient ───────────────────────────────────────────────────────────

func TestNewHTTPClient_NilConfig(t *testing.T) {
	client := NewHTTPClient(nil)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.Timeout != 0 {
		t.Errorf("Timeout = %v, want 0 (no global timeout)", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if transport.MaxIdleConns != 100 {
		t.Errorf("MaxIdleConns = %d, want 100", transport.MaxIdleConns)
	}
}

func TestNewHTTPClient_CustomConfig(t *testing.T) {
	cfg := &ClientConfig{
		Timeout:        10 * time.Second,
		ConnectTimeout: 5 * time.Second,
	}
	client := NewHTTPClient(cfg)
	if client.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", client.Timeout)
	}
}

func TestNewHTTPClient_InsecureSkipVerify(t *testing.T) {
	cfg := &ClientConfig{
		InsecureSkipVerify: true,
	}
	client := NewHTTPClient(cfg)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	tlsCfg := transport.TLSClientConfig
	if tlsCfg == nil || !tlsCfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify to be true")
	}
}

// ── getProxyURL ─────────────────────────────────────────────────────────────

func TestGetProxyURL_Empty(t *testing.T) {
	result := getProxyURL("")
	if result != nil {
		t.Errorf("expected nil for empty proxy, got %v", result)
	}
}

func TestGetProxyURL_HTTPS(t *testing.T) {
	result := getProxyURL("https://proxy.example.com:8080")
	if result == nil {
		t.Fatal("expected non-nil URL")
	}
	if result.Scheme != "https" {
		t.Errorf("Scheme = %q, want https", result.Scheme)
	}
}

func TestGetProxyURL_SOCKS5(t *testing.T) {
	result := getProxyURL("socks5://127.0.0.1:1080")
	if result == nil {
		t.Fatal("expected non-nil URL")
	}
	if result.Scheme != "socks5" {
		t.Errorf("Scheme = %q, want socks5", result.Scheme)
	}
}

func TestGetProxyURL_Invalid(t *testing.T) {
	result := getProxyURL("://invalid")
	if result != nil {
		t.Errorf("expected nil for invalid URL, got %v", result)
	}
}

// ── resolveProxyFunc ────────────────────────────────────────────────────────

func TestResolveProxyFunc_WithProxy(t *testing.T) {
	fn := resolveProxyFunc("http://127.0.0.1:7890")
	if fn == nil {
		t.Fatal("expected non-nil proxy function")
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	proxyURL, err := fn(req)
	if err != nil {
		t.Fatalf("proxy function error: %v", err)
	}
	if proxyURL == nil {
		t.Fatal("expected proxy URL")
	}
	if proxyURL.Host != "127.0.0.1:7890" {
		t.Errorf("proxy host = %q, want 127.0.0.1:7890", proxyURL.Host)
	}
}

func TestResolveProxyFunc_Empty(t *testing.T) {
	fn := resolveProxyFunc("")
	if fn == nil {
		t.Fatal("expected non-nil (environment fallback) function")
	}
}

// ── retryableStatusCode ────────────────────────────────────────────────────

func TestRetryableStatusCode(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{200, false},
		{400, false},
		{401, false},
		{403, false},
		{404, false},
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{504, true},
	}
	for _, tt := range tests {
		got := retryableStatusCode(tt.code)
		if got != tt.want {
			t.Errorf("retryableStatusCode(%d) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

// ── IsRetryableHTTPError ───────────────────────────────────────────────────

func TestIsRetryableHTTPError(t *testing.T) {
	if !IsRetryableHTTPError(429) {
		t.Error("429 should be retryable")
	}
	if !IsRetryableHTTPError(500) {
		t.Error("500 should be retryable")
	}
	if IsRetryableHTTPError(200) {
		t.Error("200 should not be retryable")
	}
	if IsRetryableHTTPError(404) {
		t.Error("404 should not be retryable")
	}
}

// ── httpError ───────────────────────────────────────────────────────────────

func TestHTTPError_Error(t *testing.T) {
	err := &httpError{StatusCode: 429, Message: "429 Too Many Requests"}
	if err.Error() != "429 Too Many Requests" {
		t.Errorf("Error() = %q, want %q", err.Error(), "429 Too Many Requests")
	}
}

// ── retryWithBackoff ───────────────────────────────────────────────────────

func TestRetryWithBackoff_Success(t *testing.T) {
	result, err := retryWithBackoff(context.Background(), 3, 1*time.Millisecond, 10*time.Millisecond, func() (string, int, error) {
		return "ok", 200, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q, want ok", result)
	}
}

func TestRetryWithBackoff_RetryThenSucceed(t *testing.T) {
	calls := 0
	result, err := retryWithBackoff(context.Background(), 3, 1*time.Millisecond, 10*time.Millisecond, func() (string, int, error) {
		calls++
		if calls < 3 {
			return "", 500, &httpError{StatusCode: 500, Message: "internal error"}
		}
		return "ok", 200, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q, want ok", result)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestRetryWithBackoff_NonRetryableStops(t *testing.T) {
	calls := 0
	_, err := retryWithBackoff(context.Background(), 3, 1*time.Millisecond, 10*time.Millisecond, func() (string, int, error) {
		calls++
		return "", 400, &httpError{StatusCode: 400, Message: "bad request"}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (non-retryable should stop immediately)", calls)
	}
}

func TestRetryWithBackoff_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := retryWithBackoff(ctx, 3, 1*time.Millisecond, 10*time.Millisecond, func() (string, int, error) {
		return "", 500, &httpError{StatusCode: 500, Message: "error"}
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestRetryWithBackoff_MaxRetriesExhausted(t *testing.T) {
	calls := 0
	_, err := retryWithBackoff(context.Background(), 2, 1*time.Millisecond, 10*time.Millisecond, func() (string, int, error) {
		calls++
		return "", 500, &httpError{StatusCode: 500, Message: "error"}
	})
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

// ── DoWithRetry ─────────────────────────────────────────────────────────────

func TestDoWithRetry_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client := server.Client()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)

	resp, err := DoWithRetry(context.Background(), client, req, 2, 1*time.Millisecond, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
}

func TestDoWithRetry_RetryOnServerError(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer server.Close()

	client := server.Client()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)

	resp, err := DoWithRetry(context.Background(), client, req, 3, 1*time.Millisecond, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDoWithRetry_WithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	body := strings.NewReader(`{"key":"value"}`)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, body)
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"key":"value"}`)), nil
	}

	client := server.Client()
	resp, err := DoWithRetry(context.Background(), client, req, 2, 1*time.Millisecond, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
}

func TestDoWithRetry_NonRetryableFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	client := server.Client()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)

	_, err := DoWithRetry(context.Background(), client, req, 3, 1*time.Millisecond, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for 403")
	}
}
