// Package platforms 提供 Mattermost 平台适配器。
// 通过 REST API 和 WebSocket 连接 Mattermost 服务器。
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
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	mattermostDefaultAPIURL    = "https://mattermost.example.com/api/v4"
	mattermostMaxMessageLength = 4000
	mattermostHeartbeatInterval = 30 * time.Second
	mattermostRequestTimeout   = 15 * time.Second
	mattermostConnectTimeout   = 20 * time.Second
	mattermostDedupMaxSize     = 1000
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
	serverURL string
	token     string
	botUserID string
	messageHandler func(*MessageEvent)

	// HTTP 客户端
	httpClient *http.Client

	// WebSocket 连接
	conn      *websocket.Conn
	running   bool
	connected bool

	// 并发控制
	mu          sync.Mutex
	dedup       *mattermostDeduplicator
	pendingResps map[string]chan map[string]any
	seqNo       int64

	// @mention 配置
	requireMention bool
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
		pendingResps:   make(map[string]chan map[string]any),
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
		a.conn.Close()
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
			close(msgCh)
			return
		}

		// 构建 WebSocket URL
		wsURL := strings.Replace(a.serverURL, "https://", "wss://", 1)
		wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
		wsURL = wsURL + "/websocket"

		dialer := websocket.DefaultDialer
		conn, _, err := dialer.DialContext(ctx, wsURL, nil)
		if err != nil {
			delay := backoff[min(backoffIdx, len(backoff)-1)]
			backoffIdx++
			slog.Warn("[Mattermost] WebSocket connection failed", "err", err, "retry_after", delay)
			time.Sleep(delay)
			continue
		}

		a.mu.Lock()
		a.conn = conn
		a.connected = true
		a.mu.Unlock()

		backoffIdx = 0

		// 发送认证
		authReq := map[string]any{
			"seq":     1,
			"action":  "authentication_challenge",
			"data":    map[string]any{"token": a.token},
		}
		if err := conn.WriteJSON(authReq); err != nil {
			conn.Close()
			continue
		}

		// 启动心跳
		go a.heartbeatLoop()

		// 监听消息
		a.listenLoop(msgCh)

		// 连接断开，尝试重连
		a.mu.Lock()
		a.connected = false
		if a.conn != nil {
			a.conn.Close()
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

	if event == "posted" {
		data := getMap(payload, "data")
		if data == nil {
			return
		}

		// 解析双层 JSON 编码的 post
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
		event := a.parsePost(post, data)
		if event != nil {
			msgCh <- event
		}
	}
}

// parsePost 解析 post 消息。
func (a *MattermostAdapter) parsePost(post, data map[string]any) *MessageEvent {
	// 提取文本
	text := getString(post, "message", "")
	if text == "" {
		return nil
	}

	// 提取发送者信息
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
		// 去除 @mention
		text = strings.Replace(text, "@"+a.botUserID, "", -1)
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
func (a *MattermostAdapter) heartbeatLoop() {
	ticker := time.NewTicker(mattermostHeartbeatInterval)
	defer ticker.Stop()

	for range ticker.C {
		a.mu.Lock()
		running := a.running
		conn := a.conn
		seq := a.seqNo + 1
		a.seqNo = seq
		a.mu.Unlock()

		if !running || conn == nil {
			return
		}

		pingReq := map[string]any{
			"seq":    seq,
			"action": "ping",
		}

		if err := conn.WriteJSON(pingReq); err != nil {
			slog.Debug("[Mattermost] heartbeat send failed", "err", err)
		}
	}
}

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送消息。
func (a *MattermostAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "channel_id 是必填项"}, nil
	}

	// 分块发送（超过最大长度）
	if len(content) > mattermostMaxMessageLength {
		return a.sendChunked(ctx, chatID, content, opts)
	}

	// 构建请求
	post := map[string]any{
		"channel_id": chatID,
		"message":    content,
	}

	// 如果有 thread_id，使用线程回复
	if opts != nil && opts.Metadata != nil {
		if threadID, ok := opts.Metadata["thread_id"].(string); ok && threadID != "" {
			post["root_id"] = threadID
		}
	}

	resp, err := a.callAPI(ctx, "POST", "/posts", post)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	postID := getString(resp, "id", "")
	return &SendResult{Success: true, MessageID: postID}, nil
}

// sendChunked 分块发送长消息。
func (a *MattermostAdapter) sendChunked(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	// 分割消息
	chunks := splitMessage(content, mattermostMaxMessageLength)

	var lastPostID string
	for i, chunk := range chunks {
		result, err := a.Send(ctx, chatID, chunk, opts)
		if err != nil {
			return result, err
		}
		if i == len(chunks)-1 {
			lastPostID = result.MessageID
		}
	}

	return &SendResult{Success: true, MessageID: lastPostID}, nil
}

// SendImage 发送图片。
func (a *MattermostAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	// Mattermost 需要先上传文件，简化为发送 URL 文本
	text := caption
	if text == "" {
		text = imageURL
	} else {
		text = text + "\n" + imageURL
	}
	return a.Send(ctx, chatID, text, opts)
}

// SendTyping 发送正在输入指示。
func (a *MattermostAdapter) SendTyping(ctx context.Context, chatID string) error {
	_, err := a.callAPI(ctx, "POST", "/users/me/typing/"+chatID, nil)
	return err
}

// EditMessage 编辑消息。
func (a *MattermostAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	post := map[string]any{
		"id":      messageID,
		"message": content,
	}

	resp, err := a.callAPI(ctx, "PUT", "/posts/"+messageID, post)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	return &SendResult{Success: true, MessageID: getString(resp, "id", "")}, nil
}

// DeleteMessage 删除消息。
func (a *MattermostAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	_, err := a.callAPI(ctx, "DELETE", "/posts/"+messageID, nil)
	return err
}

// ───────────────────────────── API 调用 ─────────────────────────────

// callAPI 调用 REST API。
func (a *MattermostAdapter) callAPI(ctx context.Context, method, endpoint string, body map[string]any) (map[string]any, error) {
	url := a.serverURL + endpoint

	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = json.Marshal(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return result, nil
}

// ───────────────────────────── 接口实现 ─────────────────────────────

// Name 返回适配器名称。
func (a *MattermostAdapter) Name() string { return "Mattermost" }

// PlatformType 返回平台类型。
func (a *MattermostAdapter) PlatformType() Platform { return PlatformMattermost }

// SendVoice 发送语音（需要上传）。
func (a *MattermostAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Mattermost 语音发送需要文件上传"}, nil
}

// SendVideo 发送视频（需要上传）。
func (a *MattermostAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Mattermost 视频发送需要文件上传"}, nil
}

// SendDocument 发送文件（需要上传）。
func (a *MattermostAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Mattermost 文件发送需要上传"}, nil
}

// MaxMessageLength 返回最大消息长度。
func (a *MattermostAdapter) MaxMessageLength() int { return mattermostMaxMessageLength }

// SupportsStreaming 返回是否支持流式输出。
func (a *MattermostAdapter) SupportsStreaming() bool { return false }

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformMattermost,
		Name:     "Mattermost",
		Factory:  func() PlatformAdapter { return NewMattermostAdapter(nil) },
	})
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func splitMessage(content string, maxLength int) []string {
	var chunks []string
	for len(content) > maxLength {
		chunks = append(chunks, content[:maxLength])
		content = content[maxLength:]
	}
	if content != "" {
		chunks = append(chunks, content)
	}
	return chunks
}