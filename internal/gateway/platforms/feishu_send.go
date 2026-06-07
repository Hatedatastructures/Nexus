package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Send 发送文本消息。
func (f *FeishuAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	contentJSON, err := json.Marshal(map[string]string{"text": content})
	if err != nil {
		return nil, fmt.Errorf("序列化消息内容失败: %w", err)
	}
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "text",
		"content":    string(contentJSON),
	}
	return f.doAPI(ctx, "POST", "/open-apis/im/v1/messages?receive_id_type=chat_id", body)
}

// validateMessageID 验证消息 ID 只含安全字符，防止路径注入。
func validateMessageID(id string) error {
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return fmt.Errorf("无效的消息 ID")
		}
	}
	return nil
}

// EditMessage 编辑已发送的消息 (飞书卡片更新)。
func (f *FeishuAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	if err := validateMessageID(messageID); err != nil {
		return nil, err
	}
	// 飞书通过更新消息卡片来编辑
	body := map[string]any{
		"content": fmt.Sprintf(`{"text":"%s"}`, escapeJSON(content)),
	}
	return f.doAPI(ctx, "PATCH", "/open-apis/im/v1/messages/"+messageID, body)
}

// DeleteMessage 删除消息。
func (f *FeishuAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	if err := validateMessageID(messageID); err != nil {
		return err
	}
	_, err := f.doAPI(ctx, "DELETE", "/open-apis/im/v1/messages/"+messageID, nil)
	return err
}

// SendTyping 飞书不支持 typing 指示器。
func (f *FeishuAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}

// SendImage 发送图片消息。
func (f *FeishuAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "image",
		"content":    fmt.Sprintf(`{"image_key":"%s"}`, escapeJSON(imageURL)),
	}
	return f.doAPI(ctx, "POST", "/open-apis/im/v1/messages?receive_id_type=chat_id", body)
}

// SendVoice 飞书支持语音消息。
func (f *FeishuAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "audio",
		"content":    fmt.Sprintf(`{"file_key":"%s"}`, escapeJSON(audioPath)),
	}
	return f.doAPI(ctx, "POST", "/open-apis/im/v1/messages?receive_id_type=chat_id", body)
}

// SendVideo 发送视频消息。
func (f *FeishuAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "media",
		"content":    fmt.Sprintf(`{"file_key":"%s","image_key":"%s"}`, escapeJSON(videoPath), escapeJSON(videoPath)),
	}
	return f.doAPI(ctx, "POST", "/open-apis/im/v1/messages?receive_id_type=chat_id", body)
}

// SendDocument 发送文件消息。
func (f *FeishuAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "file",
		"content":    fmt.Sprintf(`{"file_key":"%s"}`, escapeJSON(filePath)),
	}
	return f.doAPI(ctx, "POST", "/open-apis/im/v1/messages?receive_id_type=chat_id", body)
}

// ReplyMessage 回复指定消息。
// 飞书支持线程回复，通过 root_id 指定被回复的消息。
func (f *FeishuAdapter) ReplyMessage(ctx context.Context, messageID string, content string) (*SendResult, error) {
	if err := validateMessageID(messageID); err != nil {
		return nil, err
	}
	body := map[string]any{
		"msg_type": "text",
		"content":  fmt.Sprintf(`{"text":"%s"}`, escapeJSON(content)),
	}
	return f.doAPI(ctx, "POST", "/open-apis/im/v1/messages/"+messageID+"/reply", body)
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// doAPI 发送飞书 API 请求。
func (f *FeishuAdapter) doAPI(ctx context.Context, method string, path string, body any) (*SendResult, error) {
	token, err := f.getTenantToken(ctx)
	if err != nil {
		return &SendResult{Success: false, Error: "failed to get token: " + err.Error()}, err
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return &SendResult{Success: false, Error: err.Error()}, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, "https://open.feishu.cn"+path, bodyReader)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := f.client.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}

	if result.Code != 0 {
		return &SendResult{
			Success:   false,
			Error:     fmt.Sprintf("feishu error %d: %s", result.Code, result.Msg),
			Retryable: result.Code == 99991663 || result.Code == 99991661, // 频率限制
		}, nil
	}

	return &SendResult{Success: true, MessageID: result.Data.MessageID}, nil
}

// getTenantToken 获取 tenant_access_token (带缓存)。
func (f *FeishuAdapter) getTenantToken(ctx context.Context) (string, error) {
	f.tokenMu.Lock()
	if f.tenantToken != "" && time.Now().Before(f.tokenExpiry) {
		token := f.tenantToken
		f.tokenMu.Unlock()
		return token, nil
	}
	f.tokenMu.Unlock()

	// HTTP 调用在锁外执行，避免阻塞其他 API 调用
	body := map[string]string{
		"app_id":     f.appID,
		"app_secret": f.appSecret,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal",
		bytes.NewReader(data),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return "", err
	}

	var result struct {
		Code              int    `json:"code"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"` // 秒
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.Code != 0 {
		slog.Warn("[Feishu] get tenant_access_token failed", "code", result.Code)
		return "", fmt.Errorf("get tenant_access_token failed (code=%d)", result.Code)
	}

	f.tokenMu.Lock()
	f.tenantToken = result.TenantAccessToken
	buffer := 60
	if result.Expire < buffer {
		buffer = result.Expire / 2
	}
	f.tokenExpiry = time.Now().Add(time.Duration(result.Expire-buffer) * time.Second)
	f.tokenMu.Unlock()

	return f.tenantToken, nil
}

// escapeJSON 转义 JSON 字符串中的特殊字符。
func escapeJSON(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, c := range s {
		switch c {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&sb, `\u%04x`, c)
			} else {
				sb.WriteRune(c)
			}
		}
	}
	return sb.String()
}
