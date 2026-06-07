// Package platforms 提供 Webhook 平台适配器。
// 通过 HTTP POST 接收消息，支持自定义 webhook URL。
package platforms

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	webhookDefaultPort    = 8080
	webhookDefaultPath    = "/webhook"
	webhookMaxBodySize    = 1024 * 1024 // 1MB
	webhookRequestTimeout = 30 * time.Second
)

// ───────────────────────────── WebhookAdapter ─────────────────────────────

// WebhookAdapter Webhook 平台适配器。
type WebhookAdapter struct {
	port           int
	path           string
	secret         string
	messageHandler func(*MessageEvent)

	// HTTP 服务器
	server    *http.Server
	running   bool
	connected bool
	mu        sync.Mutex
	closeOnce sync.Once
	msgCh     chan *MessageEvent

	// 发送配置
	sendURL    string
	sendSecret string
	httpClient *http.Client
}

// NewWebhookAdapter 创建 Webhook 适配器。
func NewWebhookAdapter(messageHandler func(*MessageEvent)) *WebhookAdapter {
	port := webhookDefaultPort
	if p := os.Getenv("WEBHOOK_PORT"); p != "" {
		if parsed, err := parseInt(p); err == nil && parsed > 0 {
			port = parsed
		}
	}

	path := webhookDefaultPath
	if p := os.Getenv("WEBHOOK_PATH"); p != "" {
		path = p
	}

	secret := os.Getenv("WEBHOOK_SECRET")
	sendURL := os.Getenv("WEBHOOK_SEND_URL")
	sendSecret := os.Getenv("WEBHOOK_SEND_SECRET")

	return &WebhookAdapter{
		port:           port,
		path:           path,
		secret:         secret,
		messageHandler: messageHandler,
		sendURL:        sendURL,
		sendSecret:     sendSecret,
		httpClient:     &http.Client{Timeout: webhookRequestTimeout},
	}
}

// ───────────────────────────── 连接生命周期 ─────────────────────────────

// Connect 启动 HTTP 服务器接收 webhook。
func (a *WebhookAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	a.mu.Lock()
	a.running = true
	a.mu.Unlock()

	// 验证 sendURL scheme
	if a.sendURL != "" {
		if !isSafeURL(a.sendURL) {
			return nil, fmt.Errorf("webhook: WEBHOOK_SEND_URL 不安全 (必须为 http/https 且不指向私有 IP)")
		}
	}

	// 空 secret 安全检查: 非 loopback 绑定时必须设置 secret
	if a.secret == "" {
		addr := fmt.Sprintf(":%d", a.port)
		if !isLoopback(addr) {
			slog.Error("[Webhook] refusing to start without WEBHOOK_SECRET on non-loopback address", "port", a.port)
			return nil, fmt.Errorf("webhook: WEBHOOK_SECRET required on non-loopback address")
		}
		slog.Warn("[Webhook] WEBHOOK_SECRET not set, running without authentication (loopback only)")
	}

	// 创建消息通道
	msgCh := make(chan *MessageEvent, 100)
	a.msgCh = msgCh

	// 设置路由
	mux := http.NewServeMux()
	mux.HandleFunc(a.path, a.handleWebhook(msgCh))

	// 绑定地址: 默认 127.0.0.1, 可通过 WEBHOOK_BIND 环境变量覆盖
	bindAddr := "127.0.0.1"
	if b := os.Getenv("WEBHOOK_BIND"); b != "" {
		bindAddr = b
	}

	// 创建服务器
	a.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", bindAddr, a.port),
		Handler: mux,
	}

	// 启动服务器
	go func() {
		if err := a.server.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("[Webhook] server error", "err", err)
		}
	}()

	a.connected = true
	slog.Info("[Webhook] server started", "port", a.port, "path", a.path)

	return msgCh, nil
}

// Disconnect 关闭服务器。
func (a *WebhookAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.mu.Unlock()

	if a.server != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			slog.Warn("[Webhook] server shutdown failed", "err", err)
		}
	}

	a.closeOnce.Do(func() {
		if a.msgCh != nil {
			close(a.msgCh)
		}
	})

	a.connected = false
	slog.Info("[Webhook] server stopped")
	return nil
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func parseInt(s string) (int, error) {
	var result int
	for _, c := range strings.TrimSpace(s) {
		if c >= '0' && c <= '9' {
			result = result*10 + int(c-'0')
		} else {
			return 0, fmt.Errorf("invalid integer: %s", s)
		}
	}
	return result, nil
}

// isLoopback 检查地址是否为 loopback 绑定。
func isLoopback(addr string) bool {
	host := strings.TrimPrefix(addr, ":")
	if host == "" || host == "0.0.0.0" {
		return false
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// isSafeURL 检查 URL 是否安全 (拒绝私有 IP 和非 HTTP(S) scheme)。
func isSafeURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return false
		}
	}
	return true
}
