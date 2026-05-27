// Package platforms 提供 Webhook 平台适配器。
// 通过 HTTP POST 接收消息，支持自定义 webhook URL。
package platforms

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
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
	webhookDefaultPort      = 8080
	webhookDefaultPath      = "/webhook"
	webhookMaxBodySize      = 1024 * 1024 // 1MB
	webhookRequestTimeout   = 30 * time.Second
)

// ───────────────────────────── WebhookAdapter ─────────────────────────────

// WebhookAdapter Webhook 平台适配器。
type WebhookAdapter struct {
	port           int
	path           string
	secret         string
	messageHandler func(*MessageEvent)

	// HTTP 服务器
	server   *http.Server
	running  bool
	connected bool
	mu       sync.Mutex

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

	// 设置路由
	mux := http.NewServeMux()
	mux.HandleFunc(a.path, a.handleWebhook(msgCh))

	// 创建服务器
	a.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", a.port),
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

	a.connected = false
	slog.Info("[Webhook] server stopped")
	return nil
}

// ───────────────────────────── Webhook 处理 ─────────────────────────────

// handleWebhook 处理 webhook 请求。
func (a *WebhookAdapter) handleWebhook(msgCh chan *MessageEvent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 只接受 POST 请求
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 验证 secret
		if a.secret != "" {
			authHeader := r.Header.Get("Authorization")
			if subtle.ConstantTimeCompare([]byte(authHeader), []byte("Bearer "+a.secret)) != 1 &&
				subtle.ConstantTimeCompare([]byte(authHeader), []byte(a.secret)) != 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		// 读取请求体
		body, err := io.ReadAll(io.LimitReader(r.Body, webhookMaxBodySize))
		if err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		r.Body.Close()

		// 解析消息
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// 解析消息事件
		event := a.parseWebhookPayload(payload)
		if event != nil {
			select {
			case msgCh <- event:
			default:
				slog.Warn("[Webhook] message channel full, dropping message")
			}
		}

		// 返回成功响应
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"message": "Webhook received",
		})
	}
}

// parseWebhookPayload 解析 webhook payload。
func (a *WebhookAdapter) parseWebhookPayload(payload map[string]any) *MessageEvent {
	// 尝试标准格式
	text := getString(payload, "text", getString(payload, "content", getString(payload, "message", "")))
	if text == "" {
		return nil
	}

	// 提取发送者信息
	userID := getString(payload, "user_id", getString(payload, "from", getString(payload, "sender", "")))
	chatID := getString(payload, "chat_id", getString(payload, "channel", getString(payload, "room", userID)))

	// 提取消息 ID
	msgID := getString(payload, "message_id", getString(payload, "id", ""))
	if msgID == "" {
		msgID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	// 确定聊天类型
	chatType := getString(payload, "chat_type", getString(payload, "type", "dm"))

	return &MessageEvent{
		Text:        text,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformWebhook,
			ChatID:   chatID,
			UserID:   userID,
			ChatType: chatType,
		},
		RawMessage: payload,
	}
}

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送消息到 webhook URL。
func (a *WebhookAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if a.sendURL == "" {
		return &SendResult{Success: false, Error: "WEBHOOK_SEND_URL 未配置"}, nil
	}

	body := map[string]any{
		"chat_id": chatID,
		"text":    content,
		"content": content,
		"message": content,
	}

	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", a.sendURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	req.Header.Set("Content-Type", "application/json")
	if a.sendSecret != "" {
		req.Header.Set("Authorization", "Bearer "+a.sendSecret)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("HTTP 请求失败: %v", err)}, nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &SendResult{Success: false, Error: fmt.Sprintf("HTTP 错误 (status=%d): %s", resp.StatusCode, string(respBody))}, nil
	}

	return &SendResult{Success: true}, nil
}

// SendImage 发送图片 URL。
func (a *WebhookAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	if a.sendURL == "" {
		return &SendResult{Success: false, Error: "WEBHOOK_SEND_URL 未配置"}, nil
	}

	body := map[string]any{
		"chat_id":   chatID,
		"image_url": imageURL,
		"caption":   caption,
		"type":      "image",
	}

	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", a.sendURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	req.Header.Set("Content-Type", "application/json")
	if a.sendSecret != "" {
		req.Header.Set("Authorization", "Bearer "+a.sendSecret)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("HTTP 请求失败: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &SendResult{Success: false, Error: fmt.Sprintf("HTTP 错误 (status=%d): %s", resp.StatusCode, string(respBody))}, nil
	}

	return &SendResult{Success: true}, nil
}

// SendTyping 发送正在输入指示（webhook 不支持）。
func (a *WebhookAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}

// ───────────────────────────── 接口实现 ─────────────────────────────

// Name 返回适配器名称。
func (a *WebhookAdapter) Name() string { return "Webhook" }

// PlatformType 返回平台类型。
func (a *WebhookAdapter) PlatformType() Platform { return PlatformWebhook }

// EditMessage 编辑消息（webhook 不支持）。
func (a *WebhookAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Webhook 不支持编辑消息"}, nil
}

// DeleteMessage 删除消息（webhook 不支持）。
func (a *WebhookAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("Webhook 不支持删除消息")
}

// SendVoice 发送语音。
func (a *WebhookAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Webhook 语音发送需要媒体上传"}, nil
}

// SendVideo 发送视频。
func (a *WebhookAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Webhook 视频发送需要媒体上传"}, nil
}

// SendDocument 发送文件。
func (a *WebhookAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Webhook 文件发送需要媒体上传"}, nil
}

// MaxMessageLength 返回最大消息长度。
func (a *WebhookAdapter) MaxMessageLength() int { return 10000 }

// SupportsStreaming 返回是否支持流式输出。
func (a *WebhookAdapter) SupportsStreaming() bool { return false }

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformWebhook,
		Name:     "Webhook",
		Factory:  func() PlatformAdapter { return NewWebhookAdapter(nil) },
	})
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func parseInt(s string) (int, error) {
	var result int
	for _, c := range strings.TrimSpace(s) {
		if c >= '0' && c <= '9' {
			result = result * 10 + int(c - '0')
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