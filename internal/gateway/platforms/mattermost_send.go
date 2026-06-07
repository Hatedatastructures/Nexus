package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送消息。
func (a *MattermostAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "channel_id 是必填项"}, nil
	}

	// 分块发送（超过最大长度，按 rune 计数）
	if len([]rune(content)) > mattermostMaxMessageLength {
		return a.sendChunked(ctx, chatID, content, opts)
	}

	// 构建请求
	post := map[string]any{
		"channel_id": chatID,
		"message":    content,
	}

	// 如果有 thread_id，使用线程回复
	if opts != nil && opts.Metadata != nil {
		if threadID, ok := opts.Metadata["thread_id"].(string); ok && threadID != "" {
			post["root_id"] = threadID
		}
	}

	resp, err := a.callAPI(ctx, "POST", "/posts", post)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	postID := getString(resp, "id", "")
	return &SendResult{Success: true, MessageID: postID}, nil
}

// sendChunked 分块发送长消息。
func (a *MattermostAdapter) sendChunked(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	chunks := splitMessage(content, mattermostMaxMessageLength)

	var lastPostID string
	for i, chunk := range chunks {
		result, err := a.Send(ctx, chatID, chunk, opts)
		if err != nil {
			return result, err
		}
		if i == len(chunks)-1 {
			lastPostID = result.MessageID
		}
	}

	return &SendResult{Success: true, MessageID: lastPostID}, nil
}

// SendImage 发送图片。
func (a *MattermostAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	// Mattermost 需要先上传文件，简化为发送 URL 文本
	text := caption
	if text == "" {
		text = imageURL
	} else {
		text = text + "\n" + imageURL
	}
	return a.Send(ctx, chatID, text, opts)
}

// SendTyping 发送正在输入指示。
func (a *MattermostAdapter) SendTyping(ctx context.Context, chatID string) error {
	_, err := a.callAPI(ctx, "POST", "/users/me/typing/"+chatID, nil)
	return err
}

// EditMessage 编辑消息。
func (a *MattermostAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	if err := validateMessageID(messageID); err != nil {
		return nil, err
	}

	post := map[string]any{
		"id":      messageID,
		"message": content,
	}

	resp, err := a.callAPI(ctx, "PUT", "/posts/"+messageID, post)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	return &SendResult{Success: true, MessageID: getString(resp, "id", "")}, nil
}

// DeleteMessage 删除消息。
func (a *MattermostAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	if err := validateMessageID(messageID); err != nil {
		return err
	}
	_, err := a.callAPI(ctx, "DELETE", "/posts/"+messageID, nil)
	return err
}

// ───────────────────────────── API 调用 ─────────────────────────────

// callAPI 调用 REST API。
func (a *MattermostAdapter) callAPI(ctx context.Context, method, endpoint string, body map[string]any) (map[string]any, error) {
	url := a.serverURL + endpoint

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求体失败: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API 错误 (HTTP %d)", resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return result, nil
}

// ───────────────────────────── 接口实现 ─────────────────────────────

// Name 返回适配器名称。
func (a *MattermostAdapter) Name() string { return "Mattermost" }

// PlatformType 返回平台类型。
func (a *MattermostAdapter) PlatformType() Platform { return PlatformMattermost }

// SendVoice 发送语音（需要上传）。
func (a *MattermostAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Mattermost 语音发送需要文件上传"}, nil
}

// SendVideo 发送视频（需要上传）。
func (a *MattermostAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Mattermost 视频发送需要文件上传"}, nil
}

// SendDocument 发送文件（需要上传）。
func (a *MattermostAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Mattermost 文件发送需要文件上传"}, nil
}

// MaxMessageLength 返回最大消息长度。
func (a *MattermostAdapter) MaxMessageLength() int { return mattermostMaxMessageLength }

// SupportsStreaming 返回是否支持流式输出。
func (a *MattermostAdapter) SupportsStreaming() bool { return false }

// ───────────────────────────── 辅助函数 ─────────────────────────────

func splitMessage(content string, maxLength int) []string {
	runes := []rune(content)
	var chunks []string
	for len(runes) > maxLength {
		chunks = append(chunks, string(runes[:maxLength]))
		runes = runes[maxLength:]
	}
	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}
	return chunks
}
