// Package platforms 提供 OpenAI 兼容 API 服务器适配器。
// 通过 HTTP API 暴露 OpenAI Chat Completions 兼容端点。
package platforms

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
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
	port            int
	messageHandler  func(*MessageEvent)
	httpServer      *http.Server
	msgCh           chan *MessageEvent
	mu              sync.Mutex
	running         bool

	// 响应等待映射 (request_id -> response channel)
	pendingResponses map[string]chan string
	responseMu       sync.Mutex
}

// NewAPIServerAdapter 创建 API 服务器适配器。
func NewAPIServerAdapter(messageHandler func(*MessageEvent)) *APIServerAdapter {
	port := apiServerDefaultPort
	if p := os.Getenv("API_SERVER_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}

	return &APIServerAdapter{
		port:             port,
		messageHandler:   messageHandler,
		pendingResponses: make(map[string]chan string),
	}
}

// ───────────────────────────── PlatformAdapter 接口实现 ─────────────────────────────

func (a *APIServerAdapter) Name() string          { return "API Server" }
func (a *APIServerAdapter) PlatformType() Platform { return PlatformAPIServer }
func (a *APIServerAdapter) MaxMessageLength() int  { return 128000 }
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

// ───────────────────────────── HTTP 处理器 ─────────────────────────────

func (a *APIServerAdapter) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		http.Error(w, "读取请求失败", http.StatusBadRequest)
		return
	}

	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}

	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "解析请求失败", http.StatusBadRequest)
		return
	}

	// 提取最后一条用户消息
	var userMessage string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			userMessage = req.Messages[i].Content
			break
		}
	}

	if userMessage == "" {
		http.Error(w, "未找到用户消息", http.StatusBadRequest)
		return
	}

	// 生成请求 ID (crypto/rand)
	var randBytes [8]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		http.Error(w, "内部错误: 无法生成请求 ID", http.StatusInternalServerError)
		return
	}
	requestID := fmt.Sprintf("req_%x", randBytes[:])

	// 创建响应通道
	responseCh := make(chan string, 1)
	a.responseMu.Lock()
	a.pendingResponses[requestID] = responseCh
	a.responseMu.Unlock()

	// 发送消息到 Agent
	msgEvent := &MessageEvent{
		Text:        userMessage,
		MessageType: MsgText,
		MessageID:   requestID,
		Source: &SessionSource{
			Platform: PlatformAPIServer,
			ChatID:   requestID,
			UserID:   "api_user",
			ChatType: "dm",
		},
	}

	select {
	case a.msgCh <- msgEvent:
	default:
		slog.Warn("[API] message channel full, dropping message")
	}

	// 等待响应
	timer := time.NewTimer(apiServerRequestTimeout)
	defer timer.Stop()

	select {
	case response := <-responseCh:
		if req.Stream {
			a.handleStreamResponse(w, response)
		} else {
			a.handleNormalResponse(w, req.Model, response)
		}
	case <-timer.C:
		a.responseMu.Lock()
		delete(a.pendingResponses, requestID)
		a.responseMu.Unlock()
		http.Error(w, "请求超时", http.StatusGatewayTimeout)
	}
}

func (a *APIServerAdapter) handleNormalResponse(w http.ResponseWriter, model string, content string) {
	resp := map[string]any{
		"id": func() string {
			var b [8]byte
			if _, err := rand.Read(b[:]); err != nil {
				return fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
			}
			return fmt.Sprintf("chatcmpl-%x", b[:])
		}(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (a *APIServerAdapter) handleStreamResponse(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// 分块发送
	chunks := splitContent(content, 20)
	for _, chunk := range chunks {
		resp := map[string]any{
			"id":      generateChunkID(),
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": map[string]string{
						"content": chunk,
					},
					"finish_reason": nil,
				},
			},
		}

		data, err := json.Marshal(resp)
		if err != nil {
			slog.Error("failed to marshal streaming chunk", "error", err)
			continue
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// 发送结束标记
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (a *APIServerAdapter) handleListModels(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"object": "list",
		"data": []map[string]string{
			{"id": "nexus-agent", "object": "model", "owned_by": "nexus"},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (a *APIServerAdapter) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// generateChunkID 使用 crypto/rand 生成不可预测的流式响应 chunk ID。
func generateChunkID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("chatcmpl-fallback-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("chatcmpl-%x", b[:])
}

func splitContent(content string, chunkSize int) []string {
	var chunks []string
	runes := []rune(content)
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformAPIServer,
		Name:     "API Server",
		Factory:  func() PlatformAdapter { return NewAPIServerAdapter(nil) },
	})
}
