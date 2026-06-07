package platforms

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送文本消息。
func (a *QQBotAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chat_id 是必填项"}, nil
	}

	// 验证 chatID/groupOpenID 只含安全字符
	if err := validateChatID(chatID); err != nil {
		return nil, err
	}

	// 确定是私聊还是群聊
	var endpoint string
	var body map[string]any

	if strings.HasPrefix(chatID, "group:") {
		groupOpenID := strings.TrimPrefix(chatID, "group:")
		endpoint = fmt.Sprintf("/v2/groups/%s/messages", groupOpenID)
		body = map[string]any{
			"content":  content,
			"msg_type": qqbotMsgTypeText,
			"msg_id":   generateMsgID(),
		}
	} else if strings.HasPrefix(chatID, "guild:") {
		// 频道私信
		parts := strings.Split(chatID, ":")
		if len(parts) < 3 {
			return &SendResult{Success: false, Error: "无效的频道私信 ID"}, nil
		}
		guildID := parts[1]
		userID := parts[2]
		endpoint = fmt.Sprintf("/v2/dms/%s/messages", guildID+"_"+userID)
		body = map[string]any{
			"content":      content,
			"msg_type":     qqbotMsgTypeText,
			"msg_id":       generateMsgID(),
			"recipient_id": userID,
		}
	} else {
		// C2C 私聊
		endpoint = fmt.Sprintf("/v2/users/%s/messages", chatID)
		body = map[string]any{
			"content":  content,
			"msg_type": qqbotMsgTypeText,
			"msg_id":   generateMsgID(),
		}
	}

	resp, err := a.callAPI(ctx, endpoint, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	msgID := getString(resp, "id", "")
	return &SendResult{Success: true, MessageID: msgID}, nil
}

// SendImage 发送图片。
func (a *QQBotAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	if err := validateChatID(chatID); err != nil {
		return nil, err
	}
	// QQ Bot 需要先上传媒体
	fileInfo, err := a.uploadMedia(ctx, chatID, imageURL, qqbotMediaTypeImage)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("上传图片失败: %v", err)}, nil
	}

	var endpoint string
	var body map[string]any

	if strings.HasPrefix(chatID, "group:") {
		groupOpenID := strings.TrimPrefix(chatID, "group:")
		endpoint = fmt.Sprintf("/v2/groups/%s/messages", groupOpenID)
		body = map[string]any{
			"msg_type": qqbotMsgTypeMedia,
			"msg_id":   generateMsgID(),
			"media": map[string]any{
				"file_info": fileInfo,
			},
		}
	} else {
		endpoint = fmt.Sprintf("/v2/users/%s/messages", chatID)
		body = map[string]any{
			"msg_type": qqbotMsgTypeMedia,
			"msg_id":   generateMsgID(),
			"media": map[string]any{
				"file_info": fileInfo,
			},
		}
	}

	if caption != "" {
		body["content"] = caption
	}

	resp, err := a.callAPI(ctx, endpoint, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	msgID := getString(resp, "id", "")
	return &SendResult{Success: true, MessageID: msgID}, nil
}

// uploadMedia 上传媒体文件。
func (a *QQBotAdapter) uploadMedia(ctx context.Context, chatID, fileURL string, mediaType int) (string, error) {
	if !isSafeURL(fileURL) {
		return "", fmt.Errorf("URL 不安全: %s", fileURL)
	}
	// QQ Bot 媒体上传: 先下载文件，再通过 /v2/users/{openid}/files 上传
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return "", fmt.Errorf("创建下载请求失败: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载媒体文件失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载媒体文件返回 HTTP %d", resp.StatusCode)
	}

	// 限制下载大小 (10MB)
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", fmt.Errorf("读取媒体文件失败: %w", err)
	}

	// 构建上传请求
	var endpoint string
	if strings.HasPrefix(chatID, "group:") {
		groupID := strings.TrimPrefix(chatID, "group:")
		endpoint = fmt.Sprintf("/v2/groups/%s/files", groupID)
	} else {
		endpoint = fmt.Sprintf("/v2/users/%s/files", chatID)
	}

	// 使用 callAPI 上传
	uploadBody := map[string]any{
		"file_type": mediaType,
		"file_data": data,
	}
	result, err := a.callAPI(ctx, endpoint, uploadBody)
	if err != nil {
		// 上传失败时回退到 URL 格式
		slog.Warn("QQ Bot media upload failed, falling back to URL format", "err", err)
		return fmt.Sprintf("url:%s", fileURL), nil
	}

	if fileUUID, ok := result["file_uuid"].(string); ok {
		return fileUUID, nil
	}
	return fmt.Sprintf("url:%s", fileURL), nil
}

// sendMediaMessage 发送已上传的媒体消息（供 SendVoice/SendVideo/SendDocument 复用）。
func (a *QQBotAdapter) sendMediaMessage(ctx context.Context, chatID, fileInfo, caption string) (*SendResult, error) {
	if err := validateChatID(chatID); err != nil {
		return nil, err
	}

	var endpoint string
	var body map[string]any

	if strings.HasPrefix(chatID, "group:") {
		groupOpenID := strings.TrimPrefix(chatID, "group:")
		endpoint = fmt.Sprintf("/v2/groups/%s/messages", groupOpenID)
		body = map[string]any{
			"msg_type": qqbotMsgTypeMedia,
			"msg_id":   generateMsgID(),
			"media": map[string]any{
				"file_info": fileInfo,
			},
		}
	} else {
		endpoint = fmt.Sprintf("/v2/users/%s/messages", chatID)
		body = map[string]any{
			"msg_type": qqbotMsgTypeMedia,
			"msg_id":   generateMsgID(),
			"media": map[string]any{
				"file_info": fileInfo,
			},
		}
	}

	if caption != "" {
		body["content"] = caption
	}

	resp, err := a.callAPI(ctx, endpoint, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	return &SendResult{Success: true, MessageID: getString(resp, "id", "")}, nil
}

// SendTyping 发送正在输入指示。
func (a *QQBotAdapter) SendTyping(ctx context.Context, chatID string) error {
	if err := validateChatID(chatID); err != nil {
		return err
	}
	if strings.HasPrefix(chatID, "group:") {
		return nil // 群聊不支持输入指示
	}

	endpoint := fmt.Sprintf("/v2/users/%s/messages", chatID)
	body := map[string]any{
		"msg_type": qqbotMsgTypeInputNotify,
		"msg_id":   generateMsgID(),
	}

	_, err := a.callAPI(ctx, endpoint, body)
	return err
}

// ───────────────────────────── 权限检查与 API ─────────────────────────────
// (见 qqbot_api.go)

// ───────────────────────────── 接口实现 ─────────────────────────────

// Name 返回适配器名称。
func (a *QQBotAdapter) Name() string { return "QQBot" }

// PlatformType 返回平台类型。
func (a *QQBotAdapter) PlatformType() Platform { return PlatformQQBot }

// EditMessage 编辑消息（QQ Bot 不支持）。
func (a *QQBotAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "QQ Bot 不支持编辑消息"}, nil
}

// DeleteMessage 删除消息（QQ Bot 不支持）。
func (a *QQBotAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("QQ Bot 不支持删除消息")
}

// SendVoice 发送语音。
func (a *QQBotAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	if err := validateChatID(chatID); err != nil {
		return nil, err
	}
	fileInfo, err := a.uploadMedia(ctx, chatID, audioPath, qqbotMediaTypeVoice)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("上传语音失败: %v", err)}, nil
	}

	return a.sendMediaMessage(ctx, chatID, fileInfo, "")
}

// SendVideo 发送视频。
// 直接构造媒体消息，不委托给 SendImage（避免重复上传）。
func (a *QQBotAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	if err := validateChatID(chatID); err != nil {
		return nil, err
	}
	fileInfo, err := a.uploadMedia(ctx, chatID, videoPath, qqbotMediaTypeVideo)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("上传视频失败: %v", err)}, nil
	}

	return a.sendMediaMessage(ctx, chatID, fileInfo, caption)
}

// SendDocument 发送文件。
// 直接构造媒体消息，不委托给 SendImage（避免重复上传）。
func (a *QQBotAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	if err := validateChatID(chatID); err != nil {
		return nil, err
	}
	fileInfo, err := a.uploadMedia(ctx, chatID, filePath, qqbotMediaTypeFile)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("上传文件失败: %v", err)}, nil
	}

	return a.sendMediaMessage(ctx, chatID, fileInfo, caption)
}

// MaxMessageLength 返回最大消息长度。
func (a *QQBotAdapter) MaxMessageLength() int { return 5000 }

// SupportsStreaming 返回是否支持流式输出。
func (a *QQBotAdapter) SupportsStreaming() bool { return false }
