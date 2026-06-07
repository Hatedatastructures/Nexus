// 参考文档: https://api.slack.com/apis/socket-mode
// Slack 适配器使用 Socket Mode (WebSocket) 接收事件,
// 通过 Web API (HTTP) 发送和编辑消息。
package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// ───────────────────────────── Slack 适配器 ─────────────────────────────

// slackEnvelope 是 Socket Mode 信封格式。
type slackEnvelope struct {
	Type            string          `json:"type"`
	EnvelopeID      string          `json:"envelope_id"`
	Payload         json.RawMessage `json:"payload"`
	AcceptsResponse bool            `json:"accepts_response"`
}

// SlackAdapter 实现 Slack Socket Mode 适配器。
// 使用 Socket Mode 接收事件，通过 chat.postMessage/chat.update 发送消息。
type SlackAdapter struct {
	botToken string             // Bot User OAuth Token (xoxb-...)
	appToken string             // App-Level Token (xapp-...)
	client   *http.Client       // HTTP 客户端
	baseURL  string             // Slack API 基础 URL
	msgCh    chan *MessageEvent // 入站消息通道

	// Socket Mode WebSocket 状态
	ws        *websocket.Conn
	wsMu      sync.Mutex
	closeOnce sync.Once   //
	stopped   atomic.Bool // 确保 msgCh 只关闭一次
}

// NewSlackAdapter 创建 Slack 适配器。
// botToken 为 Bot User OAuth Token, appToken 为 App-Level Token (Socket Mode)。
func NewSlackAdapter(botToken string, appToken string) *SlackAdapter {
	return &SlackAdapter{
		botToken: botToken,
		appToken: appToken,
		client:   &http.Client{Timeout: 30 * time.Second},
		baseURL:  "https://slack.com/api",
		msgCh:    make(chan *MessageEvent, 128),
	}
}

// Name 返回平台名称。
func (s *SlackAdapter) Name() string { return "Slack" }

// PlatformType 返回平台类型枚举。
func (s *SlackAdapter) PlatformType() Platform { return PlatformSlack }

// MaxMessageLength 返回 Slack 最大消息长度。
func (s *SlackAdapter) MaxMessageLength() int { return 4000 }

// SupportsStreaming 返回是否支持流式编辑。
func (s *SlackAdapter) SupportsStreaming() bool { return true }

// Connect 建立 Socket Mode 连接并开始监听事件。
func (s *SlackAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	go s.socketLoop(ctx)
	slog.Info("slack adapter connected")
	return s.msgCh, nil
}

// Disconnect 关闭消息通道。
func (s *SlackAdapter) Disconnect(ctx context.Context) error {
	s.stopped.Store(true)
	s.closeOnce.Do(func() {
		close(s.msgCh)
	})
	slog.Info("slack adapter disconnected")
	return nil
}

// Send 发送文本消息。
func (s *SlackAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"channel": chatID,
		"text":    content,
	}
	if opts != nil {
		if opts.ReplyToMsgID != "" {
			body["thread_ts"] = opts.ReplyToMsgID
		}
		if opts.Silent {
			body["unfurl_links"] = false
		}
	}
	return s.doAPI(ctx, "POST", "/chat.postMessage", body)
}

// EditMessage 编辑已发送的消息。
func (s *SlackAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	body := map[string]any{
		"channel": chatID,
		"ts":      messageID, // Slack 使用 ts 而非 message_id
		"text":    content,
	}
	return s.doAPI(ctx, "POST", "/chat.update", body)
}

// DeleteMessage 删除消息。
func (s *SlackAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	_, err := s.doAPI(ctx, "POST", "/chat.delete", map[string]any{
		"channel": chatID,
		"ts":      messageID,
	})
	return err
}

// SendTyping Slack 没有标准的 typing 指示器 API，静默忽略。
func (s *SlackAdapter) SendTyping(ctx context.Context, chatID string) error {
	// Slack 不暴露 typing 指示器 API
	return nil
}

// SendImage 发送图片。
func (s *SlackAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"channel": chatID,
		"text":    caption,
		"blocks": []map[string]any{
			{
				"type":      "image",
				"image_url": imageURL,
				"alt_text":  caption,
			},
		},
	}
	if opts != nil && opts.ReplyToMsgID != "" {
		body["thread_ts"] = opts.ReplyToMsgID
	}
	return s.doAPI(ctx, "POST", "/chat.postMessage", body)
}

// SendVoice Slack 没有内置音频消息支持，返回不支持。
func (s *SlackAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "voice messages not supported on Slack"}, nil
}

// SendVideo 发送视频。
func (s *SlackAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"channel": chatID,
		"text":    caption,
	}
	return s.doAPI(ctx, "POST", "/chat.postMessage", body)
}

// SendDocument 发送文件。
func (s *SlackAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "file upload via raw HTTP not supported on Slack"}, nil
}

// Configure 注入 Slack 平台配置。
// settings 必须包含 "bot_token" 和 "app_token" 键。
func (s *SlackAdapter) Configure(settings map[string]any) error {
	botToken, _ := settings["bot_token"].(string)
	appToken, _ := settings["app_token"].(string)
	if botToken == "" || appToken == "" {
		return fmt.Errorf("slack 平台缺少 bot_token 或 app_token 配置")
	}
	s.botToken = botToken
	s.appToken = appToken
	s.client = &http.Client{Timeout: 30 * time.Second}
	s.baseURL = "https://slack.com/api"
	s.msgCh = make(chan *MessageEvent, 128)
	return nil
}

// ───────────────────────────── 内部方法 ─────────────────────────────
// (见 slack_handlers.go)
