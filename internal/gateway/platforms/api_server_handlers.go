package platforms

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

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
	_ = json.NewEncoder(w).Encode(resp)
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
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// 发送结束标记
	_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
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
	_ = json.NewEncoder(w).Encode(resp)
}

func (a *APIServerAdapter) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
