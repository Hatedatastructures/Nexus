// Package platforms 提供 OpenAI 兼容 API 服务器适配器。
// 通过 HTTP API 暴露 OpenAI Chat Completions 兼容端点。
package platforms

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
// 如果未设置则跳过认证 (向后兼容)。
func bearerAuthMiddleware(next http.Handler) http.Handler {
	apiKey := os.Getenv("NEXUS_API_KEY")
	if apiKey == "" {
		slog.Warn("[API Server] NEXUS_API_KEY not set, API endpoints are unauthenticated")
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func (a *APIServerAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	a.mu.Lock()
	a.running = true
	a.msgCh = make(chan *MessageEvent, 100)
	a.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", a.handleChatCompletions)
	mux.HandleFunc("/v1/models", a.handleListModels)
	mux.HandleFunc("/health", a.handleHealth)

	a.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", a.port),
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

	// 生成请求 ID
	requestID := fmt.Sprintf("req_%d", time.Now().UnixNano())

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
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
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
			"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
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

		data, _ := json.Marshal(resp)
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
