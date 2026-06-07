// Package platforms 提供 Mattermost 平台适配器。
// 通过 REST API 和 WebSocket 连接 Mattermost 服务器。
package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	mattermostDefaultAPIURL     = "https://mattermost.example.com/api/v4"
	mattermostMaxMessageLength  = 4000
	mattermostHeartbeatInterval = 30 * time.Second
	mattermostRequestTimeout    = 15 * time.Second
	mattermostConnectTimeout    = 20 * time.Second
	mattermostDedupMaxSize      = 1000
)

// 频道类型映射
var mattermostChannelTypeMap = map[string]string{
	"D": "dm",      // Direct Message
	"G": "group",   // Group Message
	"P": "group",   // Private Channel
	"O": "channel", // Open Channel
}

// ───────────────────────────── MattermostAdapter ─────────────────────────────

// MattermostAdapter Mattermost 平台适配器。
type MattermostAdapter struct {
	serverURL      string
	token          string
	botUserID      string
	messageHandler func(*MessageEvent)
	// HTTP 客户端
	httpClient *http.Client
	// WebSocket 连接
	conn      *websocket.Conn
	running   bool
	connected bool
	// 并发控制
	mu              sync.Mutex
	writeMu         sync.Mutex
	dedup           *mattermostDeduplicator
	seqNo           int64
	heartbeatCancel context.CancelFunc
	// @mention 配置
	requireMention bool
	// 通道关闭保护
	closeOnce sync.Once
}

// mattermostDeduplicator 消息去重器。
type mattermostDeduplicator struct {
	mu      sync.Mutex
	msgIDs  map[string]time.Time
	maxSize int
}

func newMattermostDeduplicator(maxSize int) *mattermostDeduplicator {
	return &mattermostDeduplicator{
		msgIDs:  make(map[string]time.Time),
		maxSize: maxSize,
	}
}

func (d *mattermostDeduplicator) isDuplicate(msgID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.msgIDs[msgID]; exists {
		return true
	}

	d.msgIDs[msgID] = time.Now()

	// 清理过期条目
	if len(d.msgIDs) > d.maxSize {
		for id, t := range d.msgIDs {
			if time.Since(t) > 5*time.Minute {
				delete(d.msgIDs, id)
			}
		}
	}

	return false
}

// writeWS 安全地向 WebSocket 写入 JSON 消息（加写锁）。
func (a *MattermostAdapter) writeWS(conn *websocket.Conn, v any) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return conn.WriteJSON(v)
}

// NewMattermostAdapter 创建 Mattermost 适配器。
func NewMattermostAdapter(messageHandler func(*MessageEvent)) *MattermostAdapter {
	serverURL := os.Getenv("MATTERMOST_URL")
	if serverURL == "" {
		serverURL = mattermostDefaultAPIURL
	}

	token := os.Getenv("MATTERMOST_TOKEN")
	requireMention := os.Getenv("MATTERMOST_REQUIRE_MENTION") == "true"

	return &MattermostAdapter{
		serverURL:      serverURL,
		token:          token,
		messageHandler: messageHandler,
		httpClient:     &http.Client{Timeout: mattermostRequestTimeout},
		dedup:          newMattermostDeduplicator(mattermostDedupMaxSize),
		requireMention: requireMention,
	}
}

// ───────────────────────────── 连接生命周期 ─────────────────────────────

// Connect 连接到 Mattermost。
func (a *MattermostAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if a.token == "" {
		return nil, fmt.Errorf("MATTERMOST_TOKEN 是必填项")
	}

	a.mu.Lock()
	a.running = true
	a.mu.Unlock()

	// 获取用户信息
	userInfo, err := a.getUserInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取用户信息失败: %w", err)
	}
	a.botUserID = getString(userInfo, "id", "")

	// 创建消息通道
	msgCh := make(chan *MessageEvent, 100)

	// 连接 WebSocket
	go a.connectWebSocket(ctx, msgCh)

	slog.Info("[Mattermost] connected", "user_id", a.botUserID)
	return msgCh, nil
}

// Disconnect 断开连接。
func (a *MattermostAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.connected = false
	a.mu.Unlock()

	if a.conn != nil {
		_ = a.conn.Close()
		a.conn = nil
	}

	slog.Info("[Mattermost] disconnected")
	return nil
}

// getUserInfo 获取当前用户信息。
func (a *MattermostAdapter) getUserInfo(ctx context.Context) (map[string]any, error) {
	return a.callAPI(ctx, "GET", "/users/me", nil)
}

// ───────────────────────────── WebSocket 连接 ─────────────────────────────

// connectWebSocket 连接 WebSocket。
func (a *MattermostAdapter) connectWebSocket(ctx context.Context, msgCh chan *MessageEvent) {
	backoff := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}
	backoffIdx := 0
	for {
		a.mu.Lock()
		running := a.running
		a.mu.Unlock()
		if !running {
			a.closeOnce.Do(func() { close(msgCh) })
			return
		}
		wsURL := strings.Replace(a.serverURL, "https://", "wss://", 1)
		wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
		wsURL = wsURL + "/websocket"
		dialer := websocket.DefaultDialer
		conn, _, err := dialer.DialContext(ctx, wsURL, nil)
		if err != nil {
			delay := backoff[min(backoffIdx, len(backoff)-1)]
			backoffIdx++
			slog.Warn("[Mattermost] WebSocket connection failed", "err", err, "retry_after", delay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			continue
		}
		a.mu.Lock()
		a.conn = conn
		a.connected = true
		a.mu.Unlock()
		backoffIdx = 0
		// 发送认证（加写锁）
		authReq := map[string]any{
			"seq": 1, "action": "authentication_challenge",
			"data": map[string]any{"token": a.token},
		}
		if err := a.writeWS(conn, authReq); err != nil {
			_ = conn.Close()
			continue
		}
		// 启动心跳（先取消旧的）
		if a.heartbeatCancel != nil {
			a.heartbeatCancel()
		}
		hbCtx, hbCancel := context.WithCancel(ctx)
		a.heartbeatCancel = hbCancel
		go a.heartbeatLoop(hbCtx)
		// 监听消息
		a.listenLoop(msgCh)
		// 连接断开，尝试重连
		a.mu.Lock()
		a.connected = false
		if a.conn != nil {
			_ = a.conn.Close()
			a.conn = nil
		}
		a.mu.Unlock()
	}
}

// listenLoop WebSocket 消息监听循环。
func (a *MattermostAdapter) listenLoop(msgCh chan *MessageEvent) {
	for {
		a.mu.Lock()
		running := a.running
		conn := a.conn
		a.mu.Unlock()

		if !running || conn == nil {
			return
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			slog.Warn("[Mattermost] failed to read message", "err", err)
			return
		}

		var payload map[string]any
		if err := json.Unmarshal(msg, &payload); err != nil {
			continue
		}

		a.dispatchPayload(payload, msgCh)
	}
}

// dispatchPayload 处理 WebSocket 消息。
func (a *MattermostAdapter) dispatchPayload(payload map[string]any, msgCh chan *MessageEvent) {
	event := getString(payload, "event", "")
	if event != "posted" {
		return
	}
	data := getMap(payload, "data")
	if data == nil {
		return
	}
	postStr := getString(data, "post", "")
	if postStr == "" {
		return
	}
	var post map[string]any
	if err := json.Unmarshal([]byte(postStr), &post); err != nil {
		return
	}
	// 检查是否是自己发送的消息
	if getString(post, "user_id", "") == a.botUserID {
		return
	}
	// 去重
	postID := getString(post, "id", "")
	if a.dedup.isDuplicate(postID) {
		return
	}
	// 解析消息
	evt := a.parsePost(post, data)
	if evt != nil {
		select {
		case msgCh <- evt:
		default:
			slog.Warn("[Mattermost] message channel full, dropping message")
		}
	}
}

// parsePost 解析 post 消息。
func (a *MattermostAdapter) parsePost(post, data map[string]any) *MessageEvent {
	text := getString(post, "message", "")
	if text == "" {
		return nil
	}

	userID := getString(post, "user_id", "")
	channelID := getString(post, "channel_id", "")

	// 获取频道类型
	channelType := getString(data, "channel_type", "O")
	chatType := mattermostChannelTypeMap[channelType]
	if chatType == "" {
		chatType = "channel"
	}

	// 非 DM 频道需要 @mention
	if chatType != "dm" && a.requireMention {
		if !strings.Contains(text, "@"+a.botUserID) {
			return nil
		}
		text = strings.ReplaceAll(text, "@"+a.botUserID, "")
		text = strings.TrimSpace(text)
	}

	// 提取 thread_id
	rootID := getString(post, "root_id", "")
	if rootID == "" {
		rootID = getString(post, "parent_id", "")
	}

	return &MessageEvent{
		Text:        text,
		MessageType: MsgText,
		MessageID:   getString(post, "id", ""),
		Source: &SessionSource{
			Platform: PlatformMattermost,
			ChatID:   channelID,
			UserID:   userID,
			ChatType: chatType,
			ThreadID: rootID,
		},
		RawMessage: post,
	}
}

// heartbeatLoop 发送心跳。
func (a *MattermostAdapter) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(mattermostHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		a.mu.Lock()
		running := a.running
		conn := a.conn
		seq := a.seqNo + 1
		a.seqNo = seq
		a.mu.Unlock()
		if !running || conn == nil {
			return
		}
		pingReq := map[string]any{"seq": seq, "action": "ping"}
		if err := a.writeWS(conn, pingReq); err != nil {
			slog.Debug("[Mattermost] heartbeat send failed", "err", err)
		}
	}
}

