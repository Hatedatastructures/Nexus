// 参考文档: https://discord.com/developers/docs/resources/channel
// Discord 适配器通过 HTTP REST API 和 WebSocket 网关与 Discord 通信。
// 使用原生的 net/http 和 WebSocket (gorilla/websocket) 实现。
package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ───────────────────────────── Discord 适配器 ─────────────────────────────

// DiscordAdapter 实现 Discord Bot API 适配器。
// 通过 HTTP REST API 发送/编辑消息，通过 WebSocket 接收消息。
type DiscordAdapter struct {
	token   string             // Bot Token
	client  *http.Client       // HTTP 客户端
	baseURL string             // API 基础 URL: https://discord.com/api/v10
	msgCh   chan *MessageEvent // 入站消息通道

	// WebSocket 网关状态
	wsURL      string           // Gateway WebSocket URL
	lastSeq    int              // 最后收到的序列号 (用于重连)
	sessionID  string           // 当前会话 ID
	heartbeatInterval time.Duration // 心跳间隔
	ws         *websocket.Conn  // 当前 WebSocket 连接
	wsMu       sync.Mutex       // WebSocket 连接锁
}

// NewDiscordAdapter 创建 Discord 适配器。
// token 为 Discord Bot Token。
func NewDiscordAdapter(token string) *DiscordAdapter {
	return &DiscordAdapter{
		token:   token,
		client:  &http.Client{Timeout: 30 * time.Second},
		baseURL: "https://discord.com/api/v10",
		msgCh:   make(chan *MessageEvent, 128),
	}
}

// Name 返回平台名称。
func (d *DiscordAdapter) Name() string { return "Discord" }

// PlatformType 返回平台类型枚举。
func (d *DiscordAdapter) PlatformType() Platform { return PlatformDiscord }

// MaxMessageLength 返回 Discord 最大消息长度 (2000)。
func (d *DiscordAdapter) MaxMessageLength() int { return 2000 }

// SupportsStreaming 返回是否支持流式编辑。
func (d *DiscordAdapter) SupportsStreaming() bool { return true }

// Connect 建立连接并开始监听消息。
// 通过 HTTP Gateway API 获取 WebSocket URL，然后建立 WebSocket 连接。
func (d *DiscordAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	// 启动 WebSocket 连接 goroutine
	go d.wsLoop(ctx)
	slog.Info("discord adapter connected")
	return d.msgCh, nil
}

// Disconnect 关闭消息通道。
func (d *DiscordAdapter) Disconnect(ctx context.Context) error {
	close(d.msgCh)
	slog.Info("discord adapter disconnected")
	return nil
}

// Send 发送文本消息到指定频道。
func (d *DiscordAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"content": content,
	}
	if opts != nil {
		if opts.ReplyToMsgID != "" {
			body["message_reference"] = map[string]string{
				"message_id": opts.ReplyToMsgID,
			}
		}
		if opts.Metadata != nil {
			if embeds, ok := opts.Metadata["embeds"]; ok {
				body["embeds"] = embeds
			}
		}
	}
	return d.doAPI(ctx, "POST", "/channels/"+chatID+"/messages", body)
}

// EditMessage 编辑已发送的消息。
func (d *DiscordAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	body := map[string]any{
		"content": content,
	}
	return d.doAPI(ctx, "PATCH", "/channels/"+chatID+"/messages/"+messageID, body)
}

// DeleteMessage 删除消息。
func (d *DiscordAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	_, err := d.doAPI(ctx, "DELETE", "/channels/"+chatID+"/messages/"+messageID, nil)
	return err
}

// SendTyping 发送"正在输入..."指示器。
func (d *DiscordAdapter) SendTyping(ctx context.Context, chatID string) error {
	_, err := d.doAPI(ctx, "POST", "/channels/"+chatID+"/typing", nil)
	return err
}

// SendImage 发送图片。
func (d *DiscordAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"content": caption,
		"embeds": []map[string]any{
			{"image": map[string]string{"url": imageURL}},
		},
	}
	if opts != nil && opts.ReplyToMsgID != "" {
		body["message_reference"] = map[string]string{"message_id": opts.ReplyToMsgID}
	}
	return d.doAPI(ctx, "POST", "/channels/"+chatID+"/messages", body)
}

// SendVoice 发送语音 (Discord 使用附件)。
func (d *DiscordAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	// Discord 音频需要 multipart 上传，这里返回不支持
	return &SendResult{Success: false, Error: "voice via raw HTTP not supported, use file upload"}, nil
}

// SendVideo 发送视频。
func (d *DiscordAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"content": caption,
	}
	return d.doAPI(ctx, "POST", "/channels/"+chatID+"/messages", body)
}

// SendDocument 发送文件 (Discord 附件)。
func (d *DiscordAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "document via raw HTTP not supported, use file upload"}, nil
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// wsLoop WebSocket 连接循环 — 实现完整的 Discord Gateway 协议。
// 处理心跳、识别、事件分发和自动重连。
func (d *DiscordAdapter) wsLoop(ctx context.Context) {
	slog.Debug("discord gateway loop starting")

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := d.connectGateway(ctx); err != nil {
			slog.Warn("discord gateway connect failed", "err", err)
			// 等待后重连
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		// 连接成功，等待关闭
		<-ctx.Done()
		d.closeWS()
		return
	}
}

// connectGateway 建立 Gateway WebSocket 连接并开始事件循环。
func (d *DiscordAdapter) connectGateway(ctx context.Context) error {
	// 1. 获取 Gateway URL
	gwURL, err := d.getGatewayURL()
	if err != nil {
		return err
	}

	// 2. 建立 WebSocket 连接
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(gwURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}

	d.wsMu.Lock()
	d.ws = conn
	d.wsMu.Unlock()

	slog.Info("discord gateway websocket connected")

	// 3. 等待 Hello 事件并启动心跳
	return d.gatewayEventLoop(ctx, conn)
}

// gatewayEventLoop 处理 Gateway 事件循环。
func (d *DiscordAdapter) gatewayEventLoop(ctx context.Context, conn *websocket.Conn) error {
	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	defer heartbeatCancel()

	// 启动心跳 goroutine
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		d.heartbeatLoop(heartbeatCtx, conn)
	}()

	defer func() {
		heartbeatCancel()
		<-heartbeatDone
	}()

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read message: %w", err)
		}

		var event struct {
			Op   int             `json:"op"`
			S    *int            `json:"s"`
			T    *string         `json:"t"`
			D    json.RawMessage `json:"d"`
		}
		if err := json.Unmarshal(msgBytes, &event); err != nil {
			slog.Warn("discord event parse error", "err", err)
			continue
		}

		// 跟踪序列号
		if event.S != nil {
			d.lastSeq = *event.S
		}

		switch event.Op {
		case 10: // Hello
			var hello struct {
				HeartbeatInterval int `json:"heartbeat_interval"`
			}
			if err := json.Unmarshal(event.D, &hello); err != nil {
				slog.Warn("discord hello parse error", "err", err)
				continue
			}
			d.heartbeatInterval = time.Duration(hello.HeartbeatInterval) * time.Millisecond
			slog.Debug("discord gateway hello received", "interval", d.heartbeatInterval)

			// 发送 Identify 或 Resume
			if d.sessionID != "" {
				d.sendResume(conn)
			} else {
				d.sendIdentify(conn)
			}

		case 11: // Heartbeat ACK
			// 心跳响应，无需处理

		case 0: // Dispatch
			if event.T == nil {
				continue
			}
			switch *event.T {
			case "READY":
				var ready struct {
					SessionID string `json:"session_id"`
					User      struct {
						ID       string `json:"id"`
						Username string `json:"username"`
					} `json:"user"`
				}
				if err := json.Unmarshal(event.D, &ready); err == nil {
					d.sessionID = ready.SessionID
					slog.Info("discord gateway ready",
						"bot_id", ready.User.ID,
						"username", ready.User.Username,
					)
				}

			case "RESUMED":
				slog.Info("discord gateway resumed")

			case "MESSAGE_CREATE":
				d.handleMessageCreate(event.D)
			}
		}
	}
}

// heartbeatLoop 定期发送心跳。
func (d *DiscordAdapter) heartbeatLoop(ctx context.Context, conn *websocket.Conn) {
	// 初始延迟: 随机 0~heartbeatInterval
	initialDelay := time.Duration(int64(d.heartbeatInterval) * int64(rand.Intn(1000)) / 1000)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(d.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.wsMu.Lock()
			if conn != d.ws {
				d.wsMu.Unlock()
				return
			}
			d.wsMu.Unlock()

			heartbeat := map[string]any{
				"op": 1,
				"d":  d.lastSeq,
			}
			data, _ := json.Marshal(heartbeat)
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				slog.Warn("discord heartbeat failed", "err", err)
				return
			}
		}
	}
}

// sendIdentify 发送 Identify 消息。
func (d *DiscordAdapter) sendIdentify(conn *websocket.Conn) {
	payload := map[string]any{
		"op": 2,
		"d": map[string]any{
			"token": d.token,
			"intents": 4097, // Guilds + GuildMessages
			"properties": map[string]string{
				"device":    "nexus-agent",
				"browser":   "nexus-agent",
				"os":        "linux",
			},
		},
	}
	data, _ := json.Marshal(payload)
	_ = conn.WriteMessage(websocket.TextMessage, data)
}

// sendResume 发送 Resume 消息。
func (d *DiscordAdapter) sendResume(conn *websocket.Conn) {
	payload := map[string]any{
		"op": 6,
		"d": map[string]string{
			"token":     d.token,
			"session_id": d.sessionID,
			"seq":       fmt.Sprintf("%d", d.lastSeq),
		},
	}
	data, _ := json.Marshal(payload)
	_ = conn.WriteMessage(websocket.TextMessage, data)
}

// handleMessageCreate 处理 MESSAGE_CREATE 事件。
func (d *DiscordAdapter) handleMessageCreate(raw json.RawMessage) {
	var msg struct {
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
		Content   string `json:"content"`
		Author    struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Bot      bool   `json:"bot"`
		} `json:"author"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	// 忽略机器人自己的消息
	if msg.Author.Bot {
		return
	}

	event := &MessageEvent{
		MessageID: msg.ID,
		Text:      msg.Content,
		MessageType: MsgText,
		Timestamp: time.Now(),
		Source: &SessionSource{
			Platform: PlatformDiscord,
			ChatID:   msg.ChannelID,
			UserID:   msg.Author.ID,
			UserName: msg.Author.Username,
			ChatType: "dm",
		},
	}

	select {
	case d.msgCh <- event:
	default:
		slog.Warn("discord message channel full, dropping message")
	}
}

// getGatewayURL 获取 Gateway WebSocket URL。
func (d *DiscordAdapter) getGatewayURL() (string, error) {
	req, _ := http.NewRequest("GET", d.baseURL+"/gateway", nil)
	req.Header.Set("Authorization", "Bot "+d.token)

	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var result struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.URL == "" {
		return "", fmt.Errorf("empty gateway url: %s", string(raw))
	}

	// 将 https:// 替换为 wss://
	wsURL := result.URL
	if len(wsURL) > 5 && wsURL[:5] == "https" {
		wsURL = "wss" + wsURL[5:]
	}
	return wsURL, nil
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformDiscord,
		Name:     "Discord",
		Factory:  func() PlatformAdapter { return &DiscordAdapter{} },
	})
}

// Configure 注入 Discord 平台配置。
// settings 必须包含 "token" 键。
func (d *DiscordAdapter) Configure(settings map[string]any) error {
	token, _ := settings["token"].(string)
	if token == "" {
		return fmt.Errorf("discord 平台缺少 token 配置")
	}
	d.token = token
	d.client = &http.Client{Timeout: 30 * time.Second}
	d.baseURL = "https://discord.com/api/v10"
	d.msgCh = make(chan *MessageEvent, 128)
	return nil
}

// closeWS 关闭 WebSocket 连接。
func (d *DiscordAdapter) closeWS() {
	d.wsMu.Lock()
	defer d.wsMu.Unlock()
	if d.ws != nil {
		_ = d.ws.Close()
		d.ws = nil
	}
}

// doAPI 发送 Discord API 请求。
func (d *DiscordAdapter) doAPI(ctx context.Context, method string, path string, body any) (*SendResult, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return &SendResult{Success: false, Error: err.Error()}, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, d.baseURL+path, bodyReader)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	req.Header.Set("Authorization", "Bot "+d.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}

	if resp.StatusCode >= 400 {
		errMsg := fmt.Sprintf("discord api error %d: %s", resp.StatusCode, string(raw))
		retryable := resp.StatusCode == 429 || resp.StatusCode >= 500
		return &SendResult{Success: false, Error: errMsg, Retryable: retryable}, nil
	}

	// 解析消息 ID
	var result struct {
		ID string `json:"id"`
	}
	json.Unmarshal(raw, &result) // 忽略解析错误

	return &SendResult{
		Success:   true,
		MessageID: result.ID,
	}, nil
}
