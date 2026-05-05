// 参考文档: https://api.slack.com/apis/socket-mode
// Slack 适配器使用 Socket Mode (WebSocket) 接收事件,
// 通过 Web API (HTTP) 发送和编辑消息。
package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ───────────────────────────── Slack 适配器 ─────────────────────────────

// SlackAdapter 实现 Slack Socket Mode 适配器。
// 使用 Socket Mode 接收事件，通过 chat.postMessage/chat.update 发送消息。
type SlackAdapter struct {
	botToken string             // Bot User OAuth Token (xoxb-...)
	appToken string             // App-Level Token (xapp-...)
	client   *http.Client       // HTTP 客户端
	baseURL  string             // Slack API 基础 URL
	msgCh    chan *MessageEvent // 入站消息通道

	// Socket Mode WebSocket 状态
	ws   *websocket.Conn
	wsMu sync.Mutex
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
	close(s.msgCh)
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
				"type": "image",
				"image_url": imageURL,
				"alt_text": caption,
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

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformSlack,
		Name:     "Slack",
		Factory:  func() PlatformAdapter { return &SlackAdapter{} },
	})
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

// socketLoop Socket Mode 连接循环。
// 通过 apps.connections.open API 获取 WebSocket URL，建立连接并处理事件。
func (s *SlackAdapter) socketLoop(ctx context.Context) {
	slog.Debug("slack socket mode loop starting")

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := s.connectSocket(ctx); err != nil {
			slog.Warn("slack socket connect failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		<-ctx.Done()
		s.closeSocket()
		return
	}
}

// connectSocket 建立 Socket Mode WebSocket 连接。
func (s *SlackAdapter) connectSocket(ctx context.Context) error {
	wsURL, err := s.getSocketURL(ctx)
	if err != nil {
		return err
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}

	s.wsMu.Lock()
	s.ws = conn
	s.wsMu.Unlock()

	slog.Info("slack socket mode websocket connected")

	// 事件循环
	return s.socketEventLoop(ctx, conn)
}

// socketEventLoop 处理 Socket Mode 事件。
func (s *SlackAdapter) socketEventLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read message: %w", err)
		}

		var envelope struct {
			Type     string          `json:"type"`
			EnvelopeID string        `json:"envelope_id"`
			Payload  json.RawMessage `json:"payload"`
			AcceptsResponse bool     `json:"accepts_response"`
		}
		if err := json.Unmarshal(msgBytes, &envelope); err != nil {
			slog.Warn("slack socket parse error", "err", err)
			continue
		}

		switch envelope.Type {
		case "hello":
			slog.Debug("slack socket mode hello received")

		case "events_api":
			s.handleEventsAPI(&envelope, conn)

		case "disconnect":
			slog.Info("slack socket mode disconnect requested")
			return nil
		}
	}
}

// handleEventsAPI 处理 Events API 事件。
func (s *SlackAdapter) handleEventsAPI(envelope *struct {
	Type     string          `json:"type"`
	EnvelopeID string        `json:"envelope_id"`
	Payload  json.RawMessage `json:"payload"`
	AcceptsResponse bool     `json:"accepts_response"`
}, conn *websocket.Conn) {
	// 先发送确认
	if envelope.EnvelopeID != "" {
		ack := map[string]any{
			"envelope_id": envelope.EnvelopeID,
		}
		data, _ := json.Marshal(ack)
		_ = conn.WriteMessage(websocket.TextMessage, data)
	}

	// 解析事件
	var event struct {
		Type    string `json:"type"`
		Channel string `json:"channel"`
		User    string `json:"user"`
		Text    string `json:"text"`
		ClientMsgID string `json:"client_msg_id"`
	}
	if err := json.Unmarshal(envelope.Payload, &event); err != nil {
		return
	}

	// 只处理消息事件
	if event.Type != "message" || event.Text == "" {
		return
	}

	msgEvent := &MessageEvent{
		MessageID:   event.ClientMsgID,
		Text:        event.Text,
		MessageType: MsgText,
		Timestamp:   time.Now(),
		Source: &SessionSource{
			Platform: PlatformSlack,
			ChatID:   event.Channel,
			UserID:   event.User,
			ChatType: "dm",
		},
	}

	select {
	case s.msgCh <- msgEvent:
	default:
		slog.Warn("slack message channel full, dropping message")
	}
}

// closeSocket 关闭 Socket 连接。
func (s *SlackAdapter) closeSocket() {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	if s.ws != nil {
		_ = s.ws.Close()
		s.ws = nil
	}
}

// getSocketURL 获取 Socket Mode WebSocket URL。
func (s *SlackAdapter) getSocketURL(ctx context.Context) (string, error) {
	resp, err := s.doSlackCall(ctx, "POST", "/apps.connections.open", nil)
	if err != nil {
		return "", err
	}
	var result struct {
		OK  bool   `json:"ok"`
		URL string `json:"url"`
	}
	json.Unmarshal(resp, &result)
	if !result.OK {
		return "", fmt.Errorf("slack apps.connections.open failed")
	}
	return result.URL, nil
}

// doAPI 发送 Slack API 请求。
func (s *SlackAdapter) doAPI(ctx context.Context, method string, path string, body any) (*SendResult, error) {
	raw, err := s.doSlackCall(ctx, method, path, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, err
	}

	var result struct {
		OK      bool   `json:"ok"`
		TS      string `json:"ts"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	if !result.OK {
		return &SendResult{Success: false, Error: result.Error, Retryable: result.Error == "ratelimited"}, nil
	}

	return &SendResult{
		Success:   true,
		MessageID: result.TS,
	}, nil
}

// doSlackCall 发送 Slack HTTP 请求。
func (s *SlackAdapter) doSlackCall(ctx context.Context, method string, path string, body any) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.botToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}
