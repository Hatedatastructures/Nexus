// Package platforms 提供 OpenAI 兼容 API 服务器适配器。
// 通过 HTTP API 暴露 OpenAI Chat Completions 兼容端点。
package platforms

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	apiServerDefaultPort    = 8081
	apiServerRequestTimeout = 120 * time.Second
)

// ───────────────────────────── APIServerAdapter ─────────────────────────────

// APIServerAdapter OpenAI 兼容 API 服务器适配器。
type APIServerAdapter struct {
	port           int
	messageHandler func(*MessageEvent)
	httpServer     *http.Server
	msgCh          chan *MessageEvent
	mu             sync.Mutex
	running        bool

	// 响应等待映射 (request_id -> response channel)
	pendingResponses map[string]chan string
	responseMu       sync.Mutex
}

// NewAPIServerAdapter 创建 API 服务器适配器。
func NewAPIServerAdapter(messageHandler func(*MessageEvent)) *APIServerAdapter {
	port := apiServerDefaultPort
	if p := os.Getenv("API_SERVER_PORT"); p != "" {
		_, _ = fmt.Sscanf(p, "%d", &port)
	}

	return &APIServerAdapter{
		port:             port,
		messageHandler:   messageHandler,
		pendingResponses: make(map[string]chan string),
	}
}

// ───────────────────────────── PlatformAdapter 接口实现 ─────────────────────────────

func (a *APIServerAdapter) Name() string            { return "API Server" }
func (a *APIServerAdapter) PlatformType() Platform  { return PlatformAPIServer }
func (a *APIServerAdapter) MaxMessageLength() int   { return 128000 }
func (a *APIServerAdapter) SupportsStreaming() bool { return true }

// bearerAuthMiddleware 检查 NEXUS_API_KEY 环境变量，
// 如果已设置则要求 Authorization: Bearer <key> 头部，
// 如果未设置则拒绝所有请求 (fail-closed)。
// 同时验证 Host 和 Origin 头部，防止 DNS 重绑定攻击。
func bearerAuthMiddleware(next http.Handler) http.Handler {
	apiKey := os.Getenv("NEXUS_API_KEY")
	if apiKey == "" {
		slog.Error("[API Server] NEXUS_API_KEY not set, refusing all requests (fail-closed)")
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "Service Unavailable: NEXUS_API_KEY not configured", http.StatusServiceUnavailable)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validateHostOrigin(w, r) {
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Unauthorized: missing Authorization header", http.StatusUnauthorized)
			return
		}

		const prefix = "Bearer "
		if !strings.HasPrefix(authHeader, prefix) || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(authHeader, prefix)), []byte(apiKey)) != 1 {
			http.Error(w, "Unauthorized: invalid API key", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// validateHostOrigin 验证请求的 Host 和 Origin 头部。
// 防止 DNS 重绑定攻击: 仅允许 loopback 地址访问 API Server。
func validateHostOrigin(w http.ResponseWriter, r *http.Request) bool {
	// 验证 Host 头
	host := r.Host
	if host != "" {
		h, _, err := net.SplitHostPort(host)
		if err != nil {
			h = host
		}
		if !isLoopbackHost(h) {
			slog.Warn("[API Server] rejected non-loopback Host", "host", host, "remote", r.RemoteAddr)
			http.Error(w, "Forbidden: non-loopback access denied", http.StatusForbidden)
			return false
		}
	}

	// 验证 Origin 头（浏览器发起的请求）
	origin := r.Header.Get("Origin")
	if origin != "" {
		origin = strings.TrimPrefix(origin, "http://")
		origin = strings.TrimPrefix(origin, "https://")
		h, _, err := net.SplitHostPort(origin)
		if err != nil {
			h = origin
		}
		h = strings.TrimSuffix(h, "/")
		if !isLoopbackHost(h) {
			slog.Warn("[API Server] rejected non-loopback Origin", "origin", r.Header.Get("Origin"), "remote", r.RemoteAddr)
			http.Error(w, "Forbidden: non-loopback origin denied", http.StatusForbidden)
			return false
		}
	}

	return true
}

// isLoopbackHost 检查主机名是否为 loopback 地址。
func isLoopbackHost(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	default:
		return net.ParseIP(host).IsLoopback()
	}
}

func (a *APIServerAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	a.mu.Lock()
	a.running = true
	a.msgCh = make(chan *MessageEvent, 100)
	a.mu.Unlock()

	bindAddr := "127.0.0.1"
	if b := os.Getenv("API_SERVER_BIND"); b != "" {
		bindAddr = b
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", a.handleChatCompletions)
	mux.HandleFunc("/v1/models", a.handleListModels)
	mux.HandleFunc("/health", a.handleHealth)

	a.httpServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", bindAddr, a.port),
		Handler:      bearerAuthMiddleware(mux),
		ReadTimeout:  apiServerRequestTimeout,
		WriteTimeout: apiServerRequestTimeout,
	}

	go func() {
		slog.Info("[API Server] started", "port", a.port)
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("[API Server] start failed", "err", err)
		}
	}()

	return a.msgCh, nil
}

func (a *APIServerAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.mu.Unlock()

	if a.httpServer != nil {
		return a.httpServer.Shutdown(ctx)
	}
	return nil
}

func (a *APIServerAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	a.responseMu.Lock()
	if ch, ok := a.pendingResponses[chatID]; ok {
		ch <- content
		delete(a.pendingResponses, chatID)
	}
	a.responseMu.Unlock()

	return &SendResult{Success: true, MessageID: chatID}, nil
}

func (a *APIServerAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return a.Send(ctx, chatID, content, nil)
}

func (a *APIServerAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return nil
}

func (a *APIServerAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}

func (a *APIServerAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	return a.Send(ctx, chatID, caption, opts)
}

func (a *APIServerAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "不支持语音"}, nil
}

func (a *APIServerAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "不支持视频"}, nil
}

func (a *APIServerAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "不支持文件"}, nil
}

