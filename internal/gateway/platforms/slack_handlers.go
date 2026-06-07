package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// ───────────────────────────── Socket Mode 连接处理 ─────────────────────────────

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
		s.closeSocket()

		if s.stopped.Load() {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
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

		var envelope slackEnvelope
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
func (s *SlackAdapter) handleEventsAPI(envelope *slackEnvelope, conn *websocket.Conn) {
	// 先发送确认
	if envelope.EnvelopeID != "" {
		ack := map[string]any{
			"envelope_id": envelope.EnvelopeID,
		}
		data, err := json.Marshal(ack)
		if err != nil {
			slog.Warn("slack: failed to marshal ack", "error", err)
		} else if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			slog.Warn("slack: failed to send ack", "envelope_id", envelope.EnvelopeID, "error", err)
		}
	}

	// 解析事件
	var event struct {
		Type        string `json:"type"`
		Channel     string `json:"channel"`
		User        string `json:"user"`
		Text        string `json:"text"`
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
			ChatType: s.chatTypeFromChannel(event.Channel),
		},
	}

	if s.stopped.Load() {
		return
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

// chatTypeFromChannel 根据频道 ID 判断聊天类型。
// Slack DM 频道 ID 以 'D' 开头，群组频道以 'C' 开头。
func (s *SlackAdapter) chatTypeFromChannel(channelID string) string {
	if len(channelID) == 0 {
		return "dm"
	}
	switch channelID[0] {
	case 'D':
		return "dm"
	default:
		return "group"
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
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("slack: failed to parse connections.open response: %w", err)
	}
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
		OK    bool   `json:"ok"`
		TS    string `json:"ts"`
		Error string `json:"error"`
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
	defer func() { _ = resp.Body.Close() }()

	return io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
}
