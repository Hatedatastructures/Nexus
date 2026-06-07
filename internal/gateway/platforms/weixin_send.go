package platforms

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送文本消息。
func (a *WeixinAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chat_id 是必填项"}, nil
	}

	contextToken := a.getContextToken(chatID)

	body := map[string]any{
		"base_info": map[string]any{
			"channel_version": "2.2.0",
		},
		"to_user": map[string]any{
			"user_id": chatID,
		},
		"context_token": contextToken,
		"msg_type":      2,
		"msg_state":     2,
		"items": []map[string]any{
			{
				"type": weixinItemText,
				"text": map[string]any{
					"content": content,
				},
			},
		},
	}

	resp, err := a.callAPI(ctx, weixinEpSendMessage, body, weixinAPITimeoutMs)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	errcode := getInt(resp, "errcode", 0)
	if errcode == weixinSessionExpiredErrcode {
		return &SendResult{Success: false, Error: "会话已过期"}, nil
	}
	if errcode != 0 {
		errmsg := getString(resp, "errmsg", "发送失败")
		return &SendResult{Success: false, Error: fmt.Sprintf("%s (errcode=%d)", errmsg, errcode)}, nil
	}

	newToken := getString(resp, "context_token", "")
	if newToken != "" {
		a.setContextToken(chatID, newToken)
	}

	msgID := getString(resp, "msg_id", "")
	return &SendResult{Success: true, MessageID: msgID}, nil
}

// SendImage 发送图片。
func (a *WeixinAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	text := caption
	if text == "" {
		text = imageURL
	} else {
		text = text + "\n" + imageURL
	}
	return a.Send(ctx, chatID, text, opts)
}

// SendTyping 发送正在输入指示。
func (a *WeixinAdapter) SendTyping(ctx context.Context, chatID string) error {
	body := map[string]any{
		"base_info": map[string]any{
			"channel_version": "2.2.0",
		},
		"to_user": map[string]any{
			"user_id": chatID,
		},
		"typing": 1,
	}

	_, err := a.callAPI(ctx, weixinEpSendTyping, body, weixinAPITimeoutMs)
	return err
}

// SendVoice 发送语音（不支持）。
func (a *WeixinAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "微信语音发送需要媒体上传"}, nil
}

// SendVideo 发送视频（不支持）。
func (a *WeixinAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "微信视频发送需要媒体上传"}, nil
}

// SendDocument 发送文件（不支持）。
func (a *WeixinAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "微信文件发送需要媒体上传"}, nil
}

// ───────────────────────────── Context Token 管理 ─────────────────────────────

func (a *WeixinAdapter) getContextToken(userID string) string {
	a.contextTokenMu.Lock()
	defer a.contextTokenMu.Unlock()
	return a.contextTokens[userID]
}

func (a *WeixinAdapter) setContextToken(userID, token string) {
	if userID == "" || token == "" {
		return
	}
	a.contextTokenMu.Lock()
	if len(a.contextTokens) > weixinContextTokenMaxSize {
		for k := range a.contextTokens {
			delete(a.contextTokens, k)
			if len(a.contextTokens) <= weixinContextTokenMaxSize/2 {
				break
			}
		}
	}
	a.contextTokens[userID] = token
	a.contextTokenMu.Unlock()
}

// ───────────────────────────── API 调用 ─────────────────────────────

// callAPI 调用 iLink API。
func (a *WeixinAdapter) callAPI(ctx context.Context, endpoint string, body map[string]any, timeoutMs int) (map[string]any, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	reqURL := a.baseURL + "/" + endpoint
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("iLink-App-Id", "bot")
	req.Header.Set("iLink-App-ClientVersion", "131072")
	req.Header.Set("X-WECHAT-UIN", generateWechatUIN())

	ctx2, cancel2 := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel2()
	req = req.WithContext(ctx2)

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

// generateWechatUIN 生成随机微信 UIN。
func generateWechatUIN() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		slog.Warn("[Weixin] crypto/rand failed, using weak fallback for UIN")
		b = fmt.Appendf(nil, "%d", time.Now().UnixNano())
	}
	return base64.StdEncoding.EncodeToString(b)
}
