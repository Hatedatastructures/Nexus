package platforms

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

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
		_ = r.Body.Close()

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
		_ = json.NewEncoder(w).Encode(map[string]any{
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
		msgID = generateCryptoID()
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

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("序列化请求体失败: %v", err)}, nil
	}

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &SendResult{Success: false, Error: fmt.Sprintf("HTTP 错误 (status=%d)", resp.StatusCode)}, nil
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

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("序列化请求体失败: %v", err)}, nil
	}

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &SendResult{Success: false, Error: fmt.Sprintf("HTTP 错误 (status=%d)", resp.StatusCode)}, nil
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
	return fmt.Errorf("webhook 不支持删除消息")
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
