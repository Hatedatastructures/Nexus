// Package platforms 提供飞书文档评论适配器。
// 监听飞书文档评论事件，当用户在文档中 @机器人 时自动回复。
package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	feishuCommentRequestTimeout = 15 * time.Second
	feishuCommentMaxMessageLen  = 4000
)

// ───────────────────────────── FeishuCommentAdapter ─────────────────────────────

// FeishuCommentAdapter 飞书文档评论适配器。
type FeishuCommentAdapter struct {
	appID          string
	appSecret      string
	messageHandler func(*MessageEvent)
	httpClient     *http.Client
	msgCh          chan *MessageEvent
	running        bool
	accessToken    string
	tokenExpiresAt time.Time
}

// FeishuCommentEvent 飞书评论事件结构。
type FeishuCommentEvent struct {
	Schema string `json:"schema"`
	Header struct {
		EventID   string `json:"event_id"`
		EventType string `json:"event_type"`
		Token     string `json:"token"`
		AppID     string `json:"app_id"`
	} `json:"header"`
	Event struct {
		CommentID   string `json:"comment_id"`
		FileToken   string `json:"file_token"`
		FileType    string `json:"file_type"`
		Content     string `json:"content"`
		UserID      string `json:"user_id"`
		UserName    string `json:"user_name"`
		ReplyMsgID  string `json:"reply_msg_id"`
	} `json:"event"`
}

// NewFeishuCommentAdapter 创建飞书文档评论适配器。
func NewFeishuCommentAdapter(messageHandler func(*MessageEvent)) *FeishuCommentAdapter {
	appID := os.Getenv("FEISHU_APP_ID")
	appSecret := os.Getenv("FEISHU_APP_SECRET")

	return &FeishuCommentAdapter{
		appID:          appID,
		appSecret:      appSecret,
		messageHandler: messageHandler,
		httpClient:     &http.Client{Timeout: feishuCommentRequestTimeout},
	}
}

// ───────────────────────────── PlatformAdapter 接口实现 ─────────────────────────────

func (a *FeishuCommentAdapter) Name() string          { return "Feishu Comment" }
func (a *FeishuCommentAdapter) PlatformType() Platform { return PlatformFeishu }
func (a *FeishuCommentAdapter) MaxMessageLength() int  { return feishuCommentMaxMessageLen }
func (a *FeishuCommentAdapter) SupportsStreaming() bool { return false }

func (a *FeishuCommentAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if a.appID == "" || a.appSecret == "" {
		return nil, fmt.Errorf("FEISHU_APP_ID 和 FEISHU_APP_SECRET 是必填项")
	}

	a.msgCh = make(chan *MessageEvent, 100)
	a.running = true

	// 获取初始 access_token
	if err := a.refreshAccessToken(ctx); err != nil {
		return nil, fmt.Errorf("获取 access_token 失败: %w", err)
	}

	slog.Info("[Feishu Comment] 已连接", "app_id", a.appID)
	return a.msgCh, nil
}

func (a *FeishuCommentAdapter) Disconnect(ctx context.Context) error {
	a.running = false
	slog.Info("[Feishu Comment] 已断开")
	return nil
}

func (a *FeishuCommentAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	// chatID 格式: "file_token:comment_id"
	parts := strings.SplitN(chatID, ":", 2)
	if len(parts) != 2 {
		return &SendResult{Success: false, Error: "无效的 chatID 格式，应为 file_token:comment_id"}, nil
	}

	fileToken := parts[0]
	commentID := parts[1]

	// 回复评论
	if err := a.replyComment(ctx, fileToken, commentID, content); err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	return &SendResult{Success: true}, nil
}

func (a *FeishuCommentAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "飞书评论不支持编辑"}, nil
}

func (a *FeishuCommentAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("飞书评论不支持删除")
}

func (a *FeishuCommentAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}

func (a *FeishuCommentAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	text := caption
	if text == "" {
		text = imageURL
	}
	return a.Send(ctx, chatID, text, opts)
}

func (a *FeishuCommentAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "飞书评论不支持语音"}, nil
}

func (a *FeishuCommentAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "飞书评论不支持视频"}, nil
}

func (a *FeishuCommentAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "飞书评论不支持文件"}, nil
}

// ───────────────────────────── 事件处理 ─────────────────────────────

// HandleEvent 处理飞书事件回调。
func (a *FeishuCommentAdapter) HandleEvent(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "读取请求失败", http.StatusBadRequest)
		return
	}

	var event FeishuCommentEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "解析事件失败", http.StatusBadRequest)
		return
	}

	// 验证事件类型
	if event.Header.EventType != "drive.file.comment" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 提取评论内容
	content := event.Event.Content
	if content == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 发送消息到 Agent
	msgEvent := &MessageEvent{
		Text:        content,
		MessageType: MsgText,
		MessageID:   event.Event.CommentID,
		Source: &SessionSource{
			Platform: PlatformFeishu,
			ChatID:   event.Event.FileToken + ":" + event.Event.CommentID,
			UserID:   event.Event.UserID,
			ChatType: "dm",
		},
	}

	a.msgCh <- msgEvent

	w.WriteHeader(http.StatusOK)
}

// ───────────────────────────── 飞书 API ─────────────────────────────

func (a *FeishuCommentAdapter) refreshAccessToken(ctx context.Context) error {
	apiURL := "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"

	payload := map[string]string{
		"app_id":     a.appID,
		"app_secret": a.appSecret,
	}

	jsonData, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if code, ok := result["code"].(float64); ok && code != 0 {
		msg, _ := result["msg"].(string)
		return fmt.Errorf("飞书 API 错误: %s", msg)
	}

	token, ok := result["tenant_access_token"].(string)
	if !ok {
		return fmt.Errorf("无法获取 access_token")
	}

	a.accessToken = token
	a.tokenExpiresAt = time.Now().Add(2 * time.Hour)

	return nil
}

func (a *FeishuCommentAdapter) replyComment(ctx context.Context, fileToken, commentID, content string) error {
	// 检查 token 是否过期
	if time.Now().After(a.tokenExpiresAt) {
		if err := a.refreshAccessToken(ctx); err != nil {
			return err
		}
	}

	apiURL := fmt.Sprintf("https://open.feishu.cn/open-apis/drive/v1/files/%s/comments/%s/replies", fileToken, commentID)

	payload := map[string]any{
		"content": map[string]any{
			"elements": []map[string]any{
				{
					"type": "text_run",
					"text_run": map[string]string{
						"text": content,
					},
				},
			},
		},
	}

	jsonData, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.accessToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if code, ok := result["code"].(float64); ok && code != 0 {
		msg, _ := result["msg"].(string)
		return fmt.Errorf("飞书 API 错误: %s", msg)
	}

	return nil
}

func init() {
	// Feishu Comment 适配器需要手动注册
}
