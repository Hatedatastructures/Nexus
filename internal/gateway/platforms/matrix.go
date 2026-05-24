// Matrix 平台适配器 — 通过 HTTP REST API 与 Matrix 服务器通信。
package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── Matrix 适配器 ─────────────────────────────

// MatrixAdapter 实现 Matrix 平台适配器。
type MatrixAdapter struct {
	homeServer  string
	accessToken string
	userID      string
	client      *http.Client
	msgCh       chan *MessageEvent
	syncToken   string
	shutdown    chan struct{}
	closeOnce   sync.Once
	stateMu     sync.RWMutex
}

// NewMatrixAdapter 创建 Matrix 适配器实例。
func NewMatrixAdapter(homeServer, accessToken, userID string) *MatrixAdapter {
	return &MatrixAdapter{
		homeServer:  strings.TrimRight(homeServer, "/"),
		accessToken: accessToken,
		userID:      userID,
		client:      &http.Client{Timeout: 30 * time.Second},
		msgCh:       make(chan *MessageEvent, 128),
		shutdown:    make(chan struct{}),
	}
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformMatrix,
		Name:     "Matrix",
		Factory:  func() PlatformAdapter { return &MatrixAdapter{} },
	})
}

// Configure 注入 Matrix 平台配置。
// settings 应包含 "home_server"、"access_token" 和 "user_id" 键。
func (m *MatrixAdapter) Configure(settings map[string]any) error {
	homeServer, _ := settings["home_server"].(string)
	accessToken, _ := settings["access_token"].(string)
	userID, _ := settings["user_id"].(string)
	if homeServer == "" || accessToken == "" {
		return fmt.Errorf("matrix 平台缺少 home_server 或 access_token 配置")
	}
	m.homeServer = strings.TrimRight(homeServer, "/")
	m.accessToken = accessToken
	m.userID = userID
	m.client = &http.Client{Timeout: 30 * time.Second}
	m.msgCh = make(chan *MessageEvent, 128)
	m.shutdown = make(chan struct{})
	return nil
}

func (m *MatrixAdapter) Name() string { return "Matrix" }
func (m *MatrixAdapter) PlatformType() Platform { return PlatformMatrix }
func (m *MatrixAdapter) MaxMessageLength() int { return 4096 }
func (m *MatrixAdapter) SupportsStreaming() bool { return false }

// Connect 连接到 Matrix 服务器并启动同步循环。
func (m *MatrixAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	// 获取初始 sync token
	if _, err := m.doSync(ctx, "", 0); err != nil {
		slog.Warn("matrix initial sync failed, will continue polling", "err", err)
	}

	go m.syncLoop(ctx)
	slog.Info("matrix adapter connected")
	return m.msgCh, nil
}

// Disconnect 停止同步循环。
func (m *MatrixAdapter) Disconnect(ctx context.Context) error {
	close(m.shutdown)
	m.closeOnce.Do(func() { close(m.msgCh) })
	slog.Info("matrix adapter disconnected")
	return nil
}

// Send 发送消息到 Matrix 房间。
func (m *MatrixAdapter) Send(ctx context.Context, chatID, content string, opts *SendOptions) (*SendResult, error) {
	eventType := "m.room.message"
	body := map[string]any{
		"msgtype": "m.text",
		"body":    content,
	}

	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/send/%s", url.PathEscape(chatID), eventType)
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

	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/send/m.room.message/%s", url.PathEscape(chatID), msgID)
	resp, err := m.doAPI(ctx, "PUT", path, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	return &SendResult{Success: true, MessageID: resp.EventID}, nil
}

func (m *MatrixAdapter) DeleteMessage(ctx context.Context, chatID, msgID string) error {
	body := map[string]any{"reason": "deleted"}
	path := fmt.Sprintf("/_matrix/client/v3/rooms/%s/redact/%s/%s", url.PathEscape(chatID), msgID, msgID)
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

// ───────────────────────────── 内部方法 ─────────────────────────────

// matrixAPIResponse 通用 API 响应。
type matrixAPIResponse struct {
	EventID string `json:"event_id"`
	TXNID   string `json:"txn_id"`
}

// syncResponse Matrix /sync 响应。
type syncResponse struct {
	NextBatch string `json:"next_batch"`
	Rooms     struct {
		Join map[string]struct {
			Timeline struct {
				Events []json.RawMessage `json:"events"`
			} `json:"timeline"`
		} `json:"join"`
	} `json:"rooms"`
}

// matrixEvent 解析后的 Matrix 事件。
type matrixEvent struct {
	Type        string          `json:"type"`
	Sender      string          `json:"sender"`
	EventID     string          `json:"event_id"`
	StateKey    *string         `json:"state_key"`
	Content     json.RawMessage `json:"content"`
	OriginServerTS int64        `json:"origin_server_ts"`
}

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
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("matrix sync error %d: %s", resp.StatusCode, string(body))
	}

	var sr syncResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, fmt.Errorf("解析 sync 响应失败: %w", err)
	}

	return &sr, nil
}

// syncLoop 长轮询同步循环。
func (m *MatrixAdapter) syncLoop(ctx context.Context) {
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
			time.Sleep(5 * time.Second)
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
			RelType string `json:"rel_type"`
			EventID string `json:"event_id"`
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
		Text:        text,
		MessageType: msgType,
		MessageID:   event.EventID,
		MediaURLs:   mediaURLs,
		ReplyToMsgID: replyToMsgID,
		ReplyToText: replyToText,
		Timestamp:   time.Now(),
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

// doAPI 发送 Matrix API 请求。
func (m *MatrixAdapter) doAPI(ctx context.Context, method, path string, body any) (*matrixAPIResponse, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	reqURL := m.homeServer + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.accessToken)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("matrix API error %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResp matrixAPIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("解析 API 响应失败: %w", err)
	}

	return &apiResp, nil
}
