// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"context"
	"crypto/tls"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"time"
)

// ClientConfig 定义 HTTP 客户端的配置参数。
type ClientConfig struct {
	// Timeout 为整体请求超时时间（包含连接、读取、写入），0 表示不限制。
	Timeout time.Duration

	// ConnectTimeout 为建立 TCP/TLS 连接的超时时间，0 表示使用默认值 30s。
	ConnectTimeout time.Duration

	// ReadTimeout 为读取响应体的超时时间，0 表示使用默认值 600s（流式请求需要较长超时）。
	ReadTimeout time.Duration

	// MaxRetries 为最大重试次数（仅对可重试错误生效），0 表示不重试。
	MaxRetries int

	// RetryBackoff 为重试基准退避时间，实际退避 = backoff * 2^attempt，0 表示使用默认值 1s。
	RetryBackoff time.Duration

	// MaxRetryBackoff 为重试退避上限，0 表示使用默认值 60s。
	MaxRetryBackoff time.Duration

	// Proxy 为 HTTP/SOCKS 代理地址（如 "http://127.0.0.1:7890" 或 "socks5://127.0.0.1:1080"）。
	Proxy string

	// InsecureSkipVerify 为 true 时跳过 TLS 证书验证（仅用于自签证书的内部端点）。
	InsecureSkipVerify bool

	// MaxIdleConns 为连接池最大空闲连接数，0 表示使用默认值 100。
	MaxIdleConns int

	// MaxIdleConnsPerHost 为每个主机的最大空闲连接数，0 表示使用默认值 10。
	MaxIdleConnsPerHost int

	// IdleConnTimeout 为空闲连接超时回收时间，0 表示使用默认值 90s。
	IdleConnTimeout time.Duration
}

// DefaultClientConfig 返回带合理默认值的 ClientConfig。
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		Timeout:             0, // 不设置全局超时，由请求级 context 控制
		ConnectTimeout:      30 * time.Second,
		ReadTimeout:         600 * time.Second, // 流式请求可能持续很久
		MaxRetries:          3,
		RetryBackoff:        1 * time.Second,
		MaxRetryBackoff:     60 * time.Second,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
}

// NewHTTPClient 根据给定的配置创建一个 *http.Client。
// 包含连接池管理、TLS 配置、代理支持以及超时控制。
func NewHTTPClient(cfg *ClientConfig) *http.Client {
	if cfg == nil {
		cfg = DefaultClientConfig()
	}

	dialer := &net.Dialer{
		Timeout:   cfg.ConnectTimeout,
		KeepAlive: 30 * time.Second,
	}

	var transport http.RoundTripper

	// 如果配置了代理，则通过 HTTP/HTTPS 代理拨号
	// 注意：SOCKS5 代理需要 golang.org/x/net/proxy 包，不在当前依赖中。
	// 如需 SOCKS5 支持，请引入该包并扩展此处的代理逻辑。
	if cfg.Proxy != "" {
		proxyURL, err := url.Parse(cfg.Proxy)
		if err == nil {
			transport = &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.DialContext(ctx, network, addr)
				},
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: cfg.InsecureSkipVerify,
				},
				MaxIdleConns:        cfg.MaxIdleConns,
				MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
				IdleConnTimeout:     cfg.IdleConnTimeout,
			}
		} else {
			slog.Warn("解析代理 URL 失败，回退到直连", "proxy", cfg.Proxy, "error", err)
		}
	}

	if transport == nil {
		transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			},
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: cfg.InsecureSkipVerify,
			},
			MaxIdleConns:        cfg.MaxIdleConns,
			MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
			IdleConnTimeout:     cfg.IdleConnTimeout,
		}
	}

	return &http.Client{
		Timeout:   cfg.Timeout,
		Transport: transport,
	}
}

// retryableStatusCode 判断 HTTP 状态码是否代表可重试的错误。
// 429（速率限制）、5xx（服务端错误）均为可重试。
func retryableStatusCode(code int) bool {
	return code == 429 || code >= 500
}

// retryWithBackoff 以指数退避方式重试给定的操作。
// 每次重试前等待 backoff * 2^attempt，上限为 maxBackoff。
func retryWithBackoff[T any](
	ctx context.Context,
	maxRetries int,
	backoff time.Duration,
	maxBackoff time.Duration,
	operation func() (T, int, error), // 返回 (结果, 状态码, 错误)
) (T, error) {
	var zero T
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// 检查 context 是否已取消
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		default:
		}

		result, statusCode, err := operation()
		if err == nil {
			return result, nil
		}

		lastErr = err
		slog.Debug("HTTP 请求失败，将重试",
			"attempt", attempt+1,
			"maxRetries", maxRetries,
			"statusCode", statusCode,
			"error", err.Error(),
		)

		// 如果已达最大重试次数，退出
		if attempt >= maxRetries {
			break
		}

		// 检查状态码是否可重试
		if statusCode > 0 && !retryableStatusCode(statusCode) {
			break
		}

		// 计算退避时间：backoff * 2^attempt
		waitTime := time.Duration(float64(backoff) * math.Pow(2, float64(attempt)))
		if waitTime > maxBackoff {
			waitTime = maxBackoff
		}

		slog.Debug("等待后重试", "waitTime", waitTime.String())
		timer := time.NewTimer(waitTime)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}

	return zero, lastErr
}

// DoWithRetry 使用内置的重试逻辑执行 HTTP 请求。
// 对 429 和 5xx 状态码执行指数退避重试。
func DoWithRetry(
	ctx context.Context,
	client *http.Client,
	req *http.Request,
	maxRetries int,
	backoff time.Duration,
	maxBackoff time.Duration,
) (*http.Response, error) {
	return retryWithBackoff(ctx, maxRetries, backoff, maxBackoff, func() (*http.Response, int, error) {
		// 每次重试需要克隆请求体（http.Request 的 Body 是 io.ReadCloser，只能读一次）
		reqClone := req.Clone(ctx)
		if req.Body != nil {
			// GetBody 在原始请求构建时已设置，用于重试时重新获取请求体
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, 0, err
				}
				reqClone.Body = body
			}
		}

		resp, err := client.Do(reqClone)
		if err != nil {
			return nil, 0, err
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, resp.StatusCode, nil
		}
		// 非 2xx 视为错误，关闭响应体并重试
		defer resp.Body.Close()
		return nil, resp.StatusCode, &httpError{StatusCode: resp.StatusCode, Message: resp.Status}
	})
}

// httpError 是 HTTP 非 2xx 状态码的简单错误封装。
type httpError struct {
	StatusCode int
	Message    string
}

func (e *httpError) Error() string {
	return e.Message
}

// IsRetryableHTTPError 判断一个 HTTP 错误是否代表可重试的场景。
func IsRetryableHTTPError(statusCode int) bool {
	return retryableStatusCode(statusCode)
}
