package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// ───────────────────────────── 消息发送 ─────────────────────────────

// Send 发送消息到 Matrix 房间。
func (m *MatrixAdapter) Send(ctx context.Context, chatID, content string, opts *SendOptions) (*SendResult, error) {
	eventType := "m.room.message"
	txnID := generateCryptoID()
	body := map[string]any{
		"msgtype": "m.text",
		"body":    content,
	}

	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/send/%s/%s", url.PathEscape(chatID), eventType, txnID)
	resp, err := m.doAPI(ctx, "PUT", path, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	return &SendResult{Success: true, MessageID: resp.EventID}, nil
}

// EditMessage 编辑 Matrix 消息。
func (m *MatrixAdapter) EditMessage(ctx context.Context, chatID, msgID, content string) (*SendResult, error) {
	body := map[string]any{
		"msgtype": "m.text",
		"body":    content,
		"m.new_content": map[string]any{
			"msgtype": "m.text",
			"body":    content,
		},
		"m.relates_to": map[string]any{
			"rel_type": "m.replace",
			"event_id": msgID,
		},
	}

	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/send/m.room.message/%s", url.PathEscape(chatID), url.PathEscape(msgID))
	resp, err := m.doAPI(ctx, "PUT", path, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	return &SendResult{Success: true, MessageID: resp.EventID}, nil
}

func (m *MatrixAdapter) DeleteMessage(ctx context.Context, chatID, msgID string) error {
	body := map[string]any{"reason": "deleted"}
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/redact/%s/%s", url.PathEscape(chatID), url.PathEscape(msgID), generateCryptoID())
	_, err := m.doAPI(ctx, "PUT", path, body)
	return err
}

func (m *MatrixAdapter) SendTyping(ctx context.Context, chatID string) error {
	body := map[string]any{"timeout": 30000}
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/typing/%s", url.PathEscape(chatID), url.PathEscape(m.userID))
	_, err := m.doAPI(ctx, "PUT", path, body)
	return err
}

func (m *MatrixAdapter) SendImage(ctx context.Context, chatID, imageURL, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"msgtype": "m.image",
		"body":    caption,
		"url":     imageURL,
	}
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/send/m.room.message", url.PathEscape(chatID))
	resp, err := m.doAPI(ctx, "PUT", path, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	return &SendResult{Success: true, MessageID: resp.EventID}, nil
}

func (m *MatrixAdapter) SendVoice(ctx context.Context, chatID, audioPath string, opts *SendOptions) (*SendResult, error) {
	return m.SendImage(ctx, chatID, audioPath, "语音消息", opts)
}

func (m *MatrixAdapter) SendVideo(ctx context.Context, chatID, videoPath, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"msgtype": "m.video",
		"body":    caption,
		"url":     videoPath,
	}
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/send/m.room.message", url.PathEscape(chatID))
	resp, err := m.doAPI(ctx, "PUT", path, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	return &SendResult{Success: true, MessageID: resp.EventID}, nil
}

func (m *MatrixAdapter) SendDocument(ctx context.Context, chatID, filePath, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"msgtype": "m.file",
		"body":    caption,
		"url":     filePath,
	}
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/send/m.room.message", url.PathEscape(chatID))
	resp, err := m.doAPI(ctx, "PUT", path, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	return &SendResult{Success: true, MessageID: resp.EventID}, nil
}

// ───────────────────────────── 同步与事件处理 ─────────────────────────────

// doSync 执行一次 /sync 请求。
func (m *MatrixAdapter) doSync(ctx context.Context, since string, timeout uint) (*syncResponse, error) {
	params := url.Values{}
	if since != "" {
		params.Set("since", since)
	}
	if timeout > 0 {
		params.Set("timeout", fmt.Sprintf("%d", timeout))
	}

	reqURL := m.homeServer + "/_matrix/client/v3/sync?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.accessToken)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("matrix sync error %d", resp.StatusCode)
	}

	var sr syncResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, fmt.Errorf("解析 sync 响应失败: %w", err)
	}

	return &sr, nil
}

// syncLoop 长轮询同步循环。
func (m *MatrixAdapter) syncLoop(ctx context.Context) {
	defer m.closeOnce.Do(func() { close(m.msgCh) })

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.shutdown:
			return
		default:
		}

		m.stateMu.RLock()
		token := m.syncToken
		m.stateMu.RUnlock()

		sr, err := m.doSync(ctx, token, 30000)
		if err != nil {
			slog.Warn("matrix sync failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-m.shutdown:
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		m.stateMu.Lock()
		m.syncToken = sr.NextBatch
		m.stateMu.Unlock()

		for roomID, roomData := range sr.Rooms.Join {
			for _, rawEvent := range roomData.Timeline.Events {
				var evt matrixEvent
				if err := json.Unmarshal(rawEvent, &evt); err != nil {
					continue
				}
				m.handleRoomEvent(ctx, roomID, &evt)
			}
		}
	}
}

// handleRoomEvent 处理单个房间事件。
func (m *MatrixAdapter) handleRoomEvent(ctx context.Context, roomID string, event *matrixEvent) {
	if event.Type != "m.room.message" {
		return
	}
	if event.Sender == m.userID {
		return
	}

	var msgContent struct {
		MsgType       string `json:"msgtype"`
		Body          string `json:"body"`
		FormattedBody string `json:"formatted_body"`
		Format        string `json:"format"`
		URL           string `json:"url"`
		RelatesTo     *struct {
			RelType   string `json:"rel_type"`
			EventID   string `json:"event_id"`
			InReplyTo *struct {
				EventID string `json:"event_id"`
			} `json:"m.in_reply_to"`
		} `json:"m.relates_to"`
	}
	if err := json.Unmarshal(event.Content, &msgContent); err != nil {
		return
	}

	// 忽略编辑事件
	if msgContent.RelatesTo != nil && msgContent.RelatesTo.RelType == "m.replace" {
		return
	}

	msgType := MsgText
	switch msgContent.MsgType {
	case "m.image":
		msgType = MsgPhoto
	case "m.audio":
		msgType = MsgVoice
	case "m.video":
		msgType = MsgVideo
	case "m.file":
		msgType = MsgDocument
	}

	var mediaURLs []string
	if msgContent.URL != "" {
		mediaURLs = append(mediaURLs, msgContent.URL)
	}

	var replyToMsgID, replyToText string
	if msgContent.RelatesTo != nil && msgContent.RelatesTo.InReplyTo != nil {
		replyToMsgID = msgContent.RelatesTo.InReplyTo.EventID
	}

	text := msgContent.Body
	if msgContent.FormattedBody != "" && msgContent.Format == "org.matrix.custom.html" {
		text = msgContent.FormattedBody
	}

	msgEvent := &MessageEvent{
		Text:         text,
		MessageType:  msgType,
		MessageID:    event.EventID,
		MediaURLs:    mediaURLs,
		ReplyToMsgID: replyToMsgID,
		ReplyToText:  replyToText,
		Timestamp:    time.UnixMilli(event.OriginServerTS),
		Source: &SessionSource{
			Platform: PlatformMatrix,
			ChatID:   roomID,
			UserID:   event.Sender,
			ChatType: "group",
		},
	}

	select {
	case m.msgCh <- msgEvent:
	default:
		slog.Warn("matrix message channel full, dropping message")
	}
}
