package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// ───────────────────────────── 发送与编辑方法 ─────────────────────────────

// Send 发送文本消息。
func (t *TelegramAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"chat_id": chatID,
		"text":    content,
	}
	if opts != nil {
		if opts.ParseMode != "" {
			body["parse_mode"] = opts.ParseMode
		}
		if opts.ReplyToMsgID != "" {
			body["reply_to_message_id"] = opts.ReplyToMsgID
		}
	}
	return t.doPost(ctx, "/sendMessage", body)
}

// EditMessage 编辑已发送的消息。
func (t *TelegramAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	body := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       content,
	}
	return t.doPost(ctx, "/editMessageText", body)
}

// DeleteMessage 删除消息。
func (t *TelegramAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	_, err := t.doPost(ctx, "/deleteMessage", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
	})
	return err
}

// SendTyping 发送"正在输入..."指示器。
func (t *TelegramAdapter) SendTyping(ctx context.Context, chatID string) error {
	_, err := t.doPost(ctx, "/sendChatAction", map[string]any{
		"chat_id": chatID,
		"action":  "typing",
	})
	return err
}

// SendOrUpdateStatus 发送或更新状态消息。
// 如果同一 chatID+statusKey 已有缓存消息，则编辑而非追加新消息。
func (t *TelegramAdapter) SendOrUpdateStatus(ctx context.Context, chatID, statusKey, content string) error {
	key := chatID + ":" + statusKey

	t.statusMu.Lock()
	existingID, hasExisting := t.statusMsgIDs[key]
	t.statusMu.Unlock()

	if hasExisting {
		_, err := t.EditMessage(ctx, chatID, existingID, content)
		if err == nil {
			return nil
		}
		// Edit 失败（消息被删除等），回退到发送新消息
	}

	result, err := t.Send(ctx, chatID, content, nil)
	if err != nil {
		return err
	}

	if result != nil && result.MessageID != "" {
		t.statusMu.Lock()
		t.statusMsgIDs[key] = result.MessageID
		t.statusMu.Unlock()
		if len(t.statusMsgIDs) > t.statusMsgMax {
			for k := range t.statusMsgIDs {
				delete(t.statusMsgIDs, k)
				break
			}
		}
	}
	return nil
}

// SendImage 发送图片。
func (t *TelegramAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"chat_id": chatID,
		"photo":   imageURL,
		"caption": caption,
	}
	if opts != nil && opts.ReplyToMsgID != "" {
		body["reply_to_message_id"] = opts.ReplyToMsgID
	}
	return t.doPost(ctx, "/sendPhoto", body)
}

// SendVoice 发送语音。
func (t *TelegramAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"chat_id": chatID,
		"voice":   audioPath,
	}
	if opts != nil && opts.ReplyToMsgID != "" {
		body["reply_to_message_id"] = opts.ReplyToMsgID
	}
	return t.doPost(ctx, "/sendVoice", body)
}

// SendVideo 发送视频。
func (t *TelegramAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"chat_id": chatID,
		"video":   videoPath,
		"caption": caption,
	}
	if opts != nil && opts.ReplyToMsgID != "" {
		body["reply_to_message_id"] = opts.ReplyToMsgID
	}
	return t.doPost(ctx, "/sendVideo", body)
}

// SendDocument 发送文件。
func (t *TelegramAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"chat_id":  chatID,
		"document": filePath,
		"caption":  caption,
	}
	if opts != nil && opts.ReplyToMsgID != "" {
		body["reply_to_message_id"] = opts.ReplyToMsgID
	}
	return t.doPost(ctx, "/sendDocument", body)
}

// ───────────────────────────── 内部 HTTP 辅助方法 ─────────────────────────────

// doPost 发送 POST 请求并解析响应。
func (t *TelegramAdapter) doPost(ctx context.Context, method string, body map[string]any) (*SendResult, error) {
	resp, err := t.doRequest(ctx, "POST", method, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, err
	}
	return t.parseSendResponse(resp)
}

// doRequest 发送 HTTP 请求。
func (t *TelegramAdapter) doRequest(ctx context.Context, method string, path string, body any) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	return io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
}

// parseSendResponse 解析发送/编辑响应。
func (t *TelegramAdapter) parseSendResponse(raw []byte) (*SendResult, error) {
	var resp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	if !resp.OK {
		return &SendResult{Success: false, Error: resp.Description, Retryable: true}, nil
	}
	return &SendResult{
		Success:   true,
		MessageID: formatInt(resp.Result.MessageID),
	}, nil
}
