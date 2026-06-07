package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/gorilla/websocket"
)

// ───────────────────────────── WebSocket 网关事件处理 ─────────────────────────────

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
			d.wsRetryDelay = d.wsRetryDelay * 2
			if d.wsRetryDelay < 5*time.Second {
				d.wsRetryDelay = 5 * time.Second
			}
			if d.wsRetryDelay > 60*time.Second {
				d.wsRetryDelay = 60 * time.Second
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(d.wsRetryDelay):
			}
			continue
		}

		// 连接成功，重置退避并等待关闭
		d.wsRetryDelay = 0
		<-ctx.Done()
		d.closeWS()
		return
	}
}

// connectGateway 建立 Gateway WebSocket 连接并开始事件循环。
func (d *DiscordAdapter) connectGateway(ctx context.Context) error {
	// 1. 获取 Gateway URL
	gwURL, err := d.getGatewayURL(ctx)
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
			Op int             `json:"op"`
			S  *int            `json:"s"`
			T  *string         `json:"t"`
			D  json.RawMessage `json:"d"`
		}
		if err := json.Unmarshal(msgBytes, &event); err != nil {
			slog.Warn("discord event parse error", "err", err)
			continue
		}

		// 跟踪序列号
		if event.S != nil {
			d.stateMu.Lock()
			d.lastSeq = *event.S
			d.stateMu.Unlock()
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
			d.stateMu.Lock()
			d.heartbeatInterval = time.Duration(hello.HeartbeatInterval) * time.Millisecond
			if d.heartbeatInterval < time.Second {
				d.heartbeatInterval = 5 * time.Second
			}
			d.stateMu.Unlock()
			slog.Debug("discord gateway hello received", "interval", d.heartbeatInterval)

			// 发送 Identify 或 Resume
			d.stateMu.RLock()
			sid := d.sessionID
			d.stateMu.RUnlock()
			if sid != "" {
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
					d.stateMu.Lock()
					d.sessionID = ready.SessionID
					d.stateMu.Unlock()
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
	d.stateMu.RLock()
	initialDelay := time.Duration(int64(d.heartbeatInterval) * int64(rand.Intn(1000)) / 1000)
	d.stateMu.RUnlock()
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	d.stateMu.RLock()
	ticker := time.NewTicker(d.heartbeatInterval)
	d.stateMu.RUnlock()
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

			d.stateMu.RLock()
			seq := d.lastSeq
			d.stateMu.RUnlock()
			heartbeat := map[string]any{
				"op": 1,
				"d":  seq,
			}
			data, err := json.Marshal(heartbeat)
			if err != nil {
				slog.Warn("discord: failed to marshal heartbeat", "error", err)
				return
			}
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
			"token":   d.token,
			"intents": 4097, // Guilds + GuildMessages
			"properties": map[string]string{
				"device":  "nexus-agent",
				"browser": "nexus-agent",
				"os":      "linux",
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("discord: failed to marshal identify payload", "error", err)
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		slog.Warn("discord: failed to send identify", "error", err)
	}
}

// sendResume 发送 Resume 消息。
func (d *DiscordAdapter) sendResume(conn *websocket.Conn) {
	d.stateMu.RLock()
	sid := d.sessionID
	seq := d.lastSeq
	d.stateMu.RUnlock()
	payload := map[string]any{
		"op": 6,
		"d": map[string]any{
			"token":      d.token,
			"session_id": sid,
			"seq":        seq,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("discord: failed to marshal resume payload", "error", err)
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		slog.Warn("discord: failed to send resume", "error", err)
	}
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
		MessageID:   msg.ID,
		Text:        msg.Content,
		MessageType: MsgText,
		Timestamp:   time.Now(),
		Source: &SessionSource{
			Platform: PlatformDiscord,
			ChatID:   msg.ChannelID,
			UserID:   msg.Author.ID,
			UserName: msg.Author.Username,
			ChatType: d.chatTypeFromChannelID(msg.ChannelID),
		},
	}

	if d.stopped.Load() {
		return
	}
	select {
	case d.msgCh <- event:
	default:
		slog.Warn("discord message channel full, dropping message")
	}
}
