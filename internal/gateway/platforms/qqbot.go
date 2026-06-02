// Package platforms 提供 QQ Bot 平台适配器。
// 通过 WebSocket 和 REST API 连接 QQ Bot Gateway。
package platforms

import (
	"bytes"
	"context"
	"crypto/rand"
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
	qqbotDefaultAPIURL      = "https://api.sgroup.qq.com"
	qqbotTokenURL           = "https://bots.qq.com/app/getAppAccessToken"
	qqbotGatewayURL         = "https://bots.qq.com/gateway"
	qqbotHeartbeatInterval  = 30 * time.Second
	qqbotConnectTimeout     = 20 * time.Second
	qqbotRequestTimeout     = 15 * time.Second
	qqbotDedupMaxSize       = 1000
)

// WebSocket OpCode
const (
	qqbotOpDispatch        = 0
	qqbotOpHeartbeat       = 1
	qqbotOpIdentify        = 2
	qqbotOpResume          = 6
	qqbotOpReconnect       = 7
	qqbotOpInvalidSession  = 9
	qqbotOpHello           = 10
	qqbotOpHeartbeatAck    = 11
)

// QQ Bot intents 精确位掩码
const (
	qqbotIntentC2CGroupAtMessages  = 1 << 25
	qqbotIntentPublicGuildMessages = 1 << 30
	qqbotIntentDirectMessage       = 1 << 12
	qqbotIntentInteraction         = 1 << 26
)

// 致命 close code: 不可恢复，停止重连
var qqbotFatalCloseCodes = map[int]bool{
	4001: true, 4002: true, 4010: true, 4011: true,
	4012: true, 4013: true, 4014: true,
}

// 会话清除 close code: 清空 session 后重连
var qqbotSessionClearCodes = map[int]bool{
	4006: true, 4007: true,
}

// 消息类型
const (
	qqbotMsgTypeText       = 0
	qqbotMsgTypeMarkdown   = 2
	qqbotMsgTypeMedia      = 7
	qqbotMsgTypeInputNotify = 6
)

// 媒体类型
const (
	qqbotMediaTypeImage = 1
	qqbotMediaTypeVideo = 2
	qqbotMediaTypeVoice = 3
	qqbotMediaTypeFile  = 4
)

// ───────────────────────────── QQBotAdapter ─────────────────────────────

// QQBotAdapter QQ Bot 平台适配器。
type QQBotAdapter struct {
	appID       string
	appSecret   string
	apiURL      string
	dmPolicy    string
	groupPolicy string
	allowFrom   []string
	groupAllowFrom []string
	messageHandler func(*MessageEvent)

	// 认证信息
	accessToken string
	botOpenID   string

	// WebSocket 连接
	conn        *websocket.Conn
	sessionID   string
	running     bool
	connected   bool

	// 并发控制
	mu              sync.Mutex
	writeMu         sync.Mutex
	closeOnce       sync.Once
	dedup           *qqbotDeduplicator
	seqNo           int64
	lastHeartbeat   time.Time
	heartbeatCancel context.CancelFunc

	// HTTP 客户端
	httpClient *http.Client
}

// qqbotDeduplicator 消息去重器。
type qqbotDeduplicator struct {
	mu      sync.Mutex
	msgIDs  map[string]time.Time
	maxSize int
}

func newQQBotDeduplicator(maxSize int) *qqbotDeduplicator {
	return &qqbotDeduplicator{
		msgIDs:  make(map[string]time.Time),
		maxSize: maxSize,
	}
}

func (d *qqbotDeduplicator) isDuplicate(msgID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.msgIDs[msgID]; exists {
		return true
	}

	d.msgIDs[msgID] = time.Now()

	if len(d.msgIDs) > d.maxSize {
		for id, t := range d.msgIDs {
			if time.Since(t) > 5*time.Minute {
				delete(d.msgIDs, id)
			}
		}
	}

	return false
}

// NewQQBotAdapter 创建 QQ Bot 适配器。
func NewQQBotAdapter(messageHandler func(*MessageEvent)) *QQBotAdapter {
	appID := os.Getenv("QQBOT_APP_ID")
	appSecret := os.Getenv("QQBOT_APP_SECRET")

	apiURL := os.Getenv("QQBOT_API_URL")
	if apiURL == "" {
		apiURL = qqbotDefaultAPIURL
	}

	dmPolicy := os.Getenv("QQBOT_DM_POLICY")
	if dmPolicy == "" {
		dmPolicy = "open"
	}

	groupPolicy := os.Getenv("QQBOT_GROUP_POLICY")
	if groupPolicy == "" {
		groupPolicy = "open"
	}

	return &QQBotAdapter{
		appID:          appID,
		appSecret:      appSecret,
		apiURL:         apiURL,
		dmPolicy:       dmPolicy,
		groupPolicy:    groupPolicy,
		allowFrom:      getEnvList("QQBOT_ALLOW_FROM"),
		groupAllowFrom: getEnvList("QQBOT_GROUP_ALLOW_FROM"),
		messageHandler: messageHandler,
		dedup:          newQQBotDeduplicator(qqbotDedupMaxSize),
		httpClient:     &http.Client{Timeout: qqbotRequestTimeout},
	}
}

// ───────────────────────────── 连接生命周期 ─────────────────────────────

// Connect 连接到 QQ Bot Gateway。
func (a *QQBotAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if a.appID == "" || a.appSecret == "" {
		return nil, fmt.Errorf("QQBOT_APP_ID 和 QQBOT_APP_SECRET 是必填项")
	}

	// 获取 access token
	if err := a.getAccessToken(ctx); err != nil {
		return nil, fmt.Errorf("获取 access token 失败: %w", err)
	}

	// 获取 gateway URL
	gatewayURL, err := a.getGatewayURL(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取 gateway URL 失败: %w", err)
	}

	a.mu.Lock()
	a.running = true
	a.mu.Unlock()

	// 创建消息通道
	msgCh := make(chan *MessageEvent, 100)

	// 连接 WebSocket
	go a.connectWebSocket(ctx, gatewayURL, msgCh)

	slog.Info("[QQBot] connected", "app_id", a.appID)
	return msgCh, nil
}

// Disconnect 断开连接。
func (a *QQBotAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.connected = false
	conn := a.conn
	a.conn = nil
	a.mu.Unlock()

	if conn != nil {
		conn.Close()
	}

	slog.Info("[QQBot] disconnected")
	return nil
}

// getAccessToken 获取 access token。
// 仅在写入 accessToken 时持锁，HTTP 调用在锁外执行。
func (a *QQBotAdapter) getAccessToken(ctx context.Context) error {
	body := map[string]any{
		"app_id":     a.appID,
		"app_secret": a.appSecret,
	}

	resp, err := a.callExternalAPI(ctx, qqbotTokenURL, body)
	if err != nil {
		return err
	}

	token := getString(resp, "access_token", "")
	if token == "" {
		return fmt.Errorf("access_token 未返回")
	}

	a.mu.Lock()
	a.accessToken = token
	a.mu.Unlock()

	return nil
}

// getGatewayURL 获取 WebSocket gateway URL。
func (a *QQBotAdapter) getGatewayURL(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", qqbotGatewayURL, nil)
	if err != nil {
		return "", err
	}

	a.mu.Lock()
	token := a.accessToken
	a.mu.Unlock()

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-App-Id", a.appID)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	url := getString(result, "url", "")
	if url == "" {
		return "", fmt.Errorf("gateway URL 未返回")
	}

	return url, nil
}

// ───────────────────────────── WebSocket 连接 ─────────────────────────────

// connectWebSocket 连接 WebSocket。
func (a *QQBotAdapter) connectWebSocket(ctx context.Context, gatewayURL string, msgCh chan *MessageEvent) {
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

		dialer := websocket.DefaultDialer
		conn, _, err := dialer.DialContext(ctx, gatewayURL, nil)
		if err != nil {
			delay := backoff[min(backoffIdx, len(backoff)-1)]
			backoffIdx++
			slog.Warn("[QQBot] WebSocket connection failed", "err", err, "retry_after", delay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}

			// 刷新 token
			if err := a.getAccessToken(ctx); err != nil {
				slog.Warn("[QQBot] failed to refresh token", "err", err)
				continue
			}

			// 获取新的 gateway URL
			newURL, err := a.getGatewayURL(ctx)
			if err != nil {
				slog.Warn("[QQBot] failed to get gateway URL", "err", err)
				continue
			}
			gatewayURL = newURL
			continue
		}

		a.mu.Lock()
		a.conn = conn
		a.connected = true
		a.mu.Unlock()

		backoffIdx = 0

		// 等待 Hello
		_, msg, err := conn.ReadMessage()
		if err != nil {
			conn.Close()
			continue
		}

		var helloPayload map[string]any
		if err := json.Unmarshal(msg, &helloPayload); err != nil {
			conn.Close()
			continue
		}

		// 发送 Identify 或 Resume
		if a.sessionID == "" {
			a.sendIdentify(conn)
		} else {
			a.sendResume(conn)
		}

		// 启动心跳（先取消旧的）
		if a.heartbeatCancel != nil {
			a.heartbeatCancel()
		}
		hbCtx, hbCancel := context.WithCancel(ctx)
		a.heartbeatCancel = hbCancel
		go a.heartbeatLoop(hbCtx)

		// 监听消息，返回导致退出的错误用于提取 close code
		listenErr := a.listenLoop(msgCh)

		// 从 listenLoop 返回的错误中提取 close code
		a.mu.Lock()
		a.connected = false
		closeCode := 0
		if ce, ok := listenErr.(*websocket.CloseError); ok {
			closeCode = ce.Code
		}
		if a.conn != nil {
			a.conn.Close()
			a.conn = nil
		}

		if qqbotFatalCloseCodes[closeCode] {
			slog.Error("[QQBot] fatal close code, stopping reconnect", "code", closeCode)
			a.mu.Unlock()
			return
		}
		if qqbotSessionClearCodes[closeCode] {
			slog.Warn("[QQBot] session-clear close code, resetting session", "code", closeCode)
			a.sessionID = ""
			a.seqNo = 0
		}
		a.mu.Unlock()
	}
}

// sendIdentify 发送 Identify 请求。
func (a *QQBotAdapter) sendIdentify(conn *websocket.Conn) {
	a.mu.Lock()
	token := a.accessToken
	a.mu.Unlock()

	identifyReq := map[string]any{
		"op": qqbotOpIdentify,
		"d": map[string]any{
			"token":   token,
			"intents": qqbotIntentC2CGroupAtMessages | qqbotIntentPublicGuildMessages | qqbotIntentDirectMessage | qqbotIntentInteraction,
			"shard":   []int{0, 1},
			"properties": map[string]any{
				"$os":      "linux",
				"$browser": "nexus-agent",
				"$device":  "nexus-agent",
			},
		},
	}

	a.writeMu.Lock()
	if err := conn.WriteJSON(identifyReq); err != nil {
		slog.Warn("[QQBot] failed to send Identify", "err", err)
	}
	a.writeMu.Unlock()
}

// sendResume 发送 Resume 请求。
func (a *QQBotAdapter) sendResume(conn *websocket.Conn) {
	a.mu.Lock()
	token := a.accessToken
	sessID := a.sessionID
	seq := a.seqNo
	a.mu.Unlock()

	resumeReq := map[string]any{
		"op": qqbotOpResume,
		"d": map[string]any{
			"token":      token,
			"session_id": sessID,
			"seq":        seq,
		},
	}

	a.writeMu.Lock()
	if err := conn.WriteJSON(resumeReq); err != nil {
		slog.Warn("[QQBot] failed to send Resume", "err", err)
	}
	a.writeMu.Unlock()
}

// listenLoop WebSocket 消息监听循环。
// 返回导致退出的错误（可用于提取 WebSocket close code）。
func (a *QQBotAdapter) listenLoop(msgCh chan *MessageEvent) error {
	for {
		a.mu.Lock()
		running := a.running
		conn := a.conn
		a.mu.Unlock()

		if !running || conn == nil {
			return nil
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		var payload map[string]any
		if err := json.Unmarshal(msg, &payload); err != nil {
			continue
		}

		a.dispatchPayload(payload, msgCh)
	}
}

// dispatchPayload 处理 WebSocket 消息。
// 每个临界区使用 Lock + defer Unlock 防止 panic 导致死锁。
func (a *QQBotAdapter) dispatchPayload(payload map[string]any, msgCh chan *MessageEvent) {
	op := getInt(payload, "op", 0)

	switch op {
	case qqbotOpDispatch:
		t := getString(payload, "t", "")
		s := getInt(payload, "s", 0)
		d := getMap(payload, "d")

		a.mu.Lock()
		a.seqNo = int64(s)
		a.mu.Unlock()

		switch t {
		case "READY":
			a.mu.Lock()
			a.sessionID = getString(d, "session_id", "")
			userMap := getMap(d, "user")
			if userMap != nil {
				a.botOpenID = getString(userMap, "id", "")
			}
			a.mu.Unlock()
			slog.Info("[QQBot] authenticated", "session_id", a.sessionID)

		case "C2C_MESSAGE_CREATE":
			a.onC2CMessage(d, msgCh)

		case "GROUP_AT_MESSAGE_CREATE":
			a.onGroupAtMessage(d, msgCh)

		case "DIRECT_MESSAGE_CREATE":
			a.onDirectMessage(d, msgCh)
		}

	case qqbotOpHeartbeatAck:
		a.mu.Lock()
		a.lastHeartbeat = time.Now()
		a.mu.Unlock()

	case qqbotOpReconnect:
		slog.Warn("[QQBot] received Reconnect command")
		return

	case qqbotOpInvalidSession:
		d := payload["d"]
		a.mu.Lock()
		if boolVal, ok := d.(bool); ok && boolVal {
			slog.Warn("[QQBot] invalid session (resumable), will resume")
		} else {
			slog.Warn("[QQBot] invalid session (non-resumable), clearing session")
			a.sessionID = ""
			a.seqNo = 0
		}
		a.mu.Unlock()
		return
	}
}

// ───────────────────────────── 消息处理 ─────────────────────────────

// onC2CMessage 处理私聊消息。
func (a *QQBotAdapter) onC2CMessage(d map[string]any, msgCh chan *MessageEvent) {
	author := getMap(d, "author")
	userOpenID := getString(author, "id", "")

	// 检查权限
	if !a.isDMAllowed(userOpenID) {
		return
	}

	msgID := getString(d, "id", "")
	if a.dedup.isDuplicate(msgID) {
		return
	}

	content := getString(d, "content", "")
	if content == "" {
		return
	}

	event := &MessageEvent{
		Text:        content,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformQQBot,
			ChatID:   userOpenID,
			UserID:   userOpenID,
			ChatType: "dm",
		},
		RawMessage: d,
	}

	select {
	case msgCh <- event:
	default:
		slog.Warn("[QQBot] message channel full, dropping message")
	}
}

// onGroupAtMessage 处理群 @消息。
func (a *QQBotAdapter) onGroupAtMessage(d map[string]any, msgCh chan *MessageEvent) {
	groupID := getString(d, "group_openid", "")
	author := getMap(d, "author")
	userOpenID := getString(author, "id", "")

	// 检查权限
	if !a.isGroupAllowed(groupID, userOpenID) {
		return
	}

	msgID := getString(d, "id", "")
	if a.dedup.isDuplicate(msgID) {
		return
	}

	content := getString(d, "content", "")
	if content == "" {
		return
	}

	// 去除 @mention
	a.mu.Lock()
	botID := a.botOpenID
	a.mu.Unlock()
	content = strings.Replace(content, "@"+botID, "", -1)
	content = strings.TrimSpace(content)

	if content == "" {
		return
	}

	event := &MessageEvent{
		Text:        content,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformQQBot,
			ChatID:   groupID,
			UserID:   userOpenID,
			ChatType: "group",
		},
		RawMessage: d,
	}

	select {
	case msgCh <- event:
	default:
		slog.Warn("[QQBot] message channel full, dropping message")
	}
}

// onDirectMessage 处理频道私信。
func (a *QQBotAdapter) onDirectMessage(d map[string]any, msgCh chan *MessageEvent) {
	author := getMap(d, "author")
	userID := getString(author, "id", "")
	guildID := getString(d, "guild_id", "")

	msgID := getString(d, "id", "")
	if a.dedup.isDuplicate(msgID) {
		return
	}

	content := getString(d, "content", "")
	if content == "" {
		return
	}

	chatID := "guild:" + guildID + ":" + userID

	event := &MessageEvent{
		Text:        content,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformQQBot,
			ChatID:   chatID,
			UserID:   userID,
			ChatType: "channel",
		},
		RawMessage: d,
	}

	select {
	case msgCh <- event:
	default:
		slog.Warn("[QQBot] message channel full, dropping message")
	}
}

// heartbeatLoop 发送心跳。
func (a *QQBotAdapter) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(qqbotHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			running := a.running
			conn := a.conn
			seq := a.seqNo
			a.mu.Unlock()

			if !running || conn == nil {
				return
			}

			heartbeatReq := map[string]any{
				"op": qqbotOpHeartbeat,
				"d":  seq,
			}

			a.writeMu.Lock()
			if err := conn.WriteJSON(heartbeatReq); err != nil {
				slog.Debug("[QQBot] heartbeat send failed", "err", err)
			}
			a.writeMu.Unlock()
		}
	}
}

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送文本消息。
func (a *QQBotAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chat_id 是必填项"}, nil
	}

	// 验证 chatID/groupOpenID 只含安全字符
	if err := validateChatID(chatID); err != nil {
		return nil, err
	}

	// 确定是私聊还是群聊
	var endpoint string
	var body map[string]any

	if strings.HasPrefix(chatID, "group:") {
		groupOpenID := strings.TrimPrefix(chatID, "group:")
		endpoint = fmt.Sprintf("/v2/groups/%s/messages", groupOpenID)
		body = map[string]any{
			"content":  content,
			"msg_type": qqbotMsgTypeText,
			"msg_id":   generateMsgID(),
		}
	} else if strings.HasPrefix(chatID, "guild:") {
		// 频道私信
		parts := strings.Split(chatID, ":")
		if len(parts) < 3 {
			return &SendResult{Success: false, Error: "无效的频道私信 ID"}, nil
		}
		guildID := parts[1]
		userID := parts[2]
		endpoint = fmt.Sprintf("/v2/dms/%s/messages", guildID+"_"+userID)
		body = map[string]any{
			"content":      content,
			"msg_type":     qqbotMsgTypeText,
			"msg_id":       generateMsgID(),
			"recipient_id": userID,
		}
	} else {
		// C2C 私聊
		endpoint = fmt.Sprintf("/v2/users/%s/messages", chatID)
		body = map[string]any{
			"content":  content,
			"msg_type": qqbotMsgTypeText,
			"msg_id":   generateMsgID(),
		}
	}

	resp, err := a.callAPI(ctx, endpoint, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	msgID := getString(resp, "id", "")
	return &SendResult{Success: true, MessageID: msgID}, nil
}

// SendImage 发送图片。
func (a *QQBotAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	if err := validateChatID(chatID); err != nil {
		return nil, err
	}
	// QQ Bot 需要先上传媒体
	fileInfo, err := a.uploadMedia(ctx, chatID, imageURL, qqbotMediaTypeImage)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("上传图片失败: %v", err)}, nil
	}

	var endpoint string
	var body map[string]any

	if strings.HasPrefix(chatID, "group:") {
		groupOpenID := strings.TrimPrefix(chatID, "group:")
		endpoint = fmt.Sprintf("/v2/groups/%s/messages", groupOpenID)
		body = map[string]any{
			"msg_type": qqbotMsgTypeMedia,
			"msg_id":   generateMsgID(),
			"media": map[string]any{
				"file_info": fileInfo,
			},
		}
	} else {
		endpoint = fmt.Sprintf("/v2/users/%s/messages", chatID)
		body = map[string]any{
			"msg_type": qqbotMsgTypeMedia,
			"msg_id":   generateMsgID(),
			"media": map[string]any{
				"file_info": fileInfo,
			},
		}
	}

	if caption != "" {
		body["content"] = caption
	}

	resp, err := a.callAPI(ctx, endpoint, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	msgID := getString(resp, "id", "")
	return &SendResult{Success: true, MessageID: msgID}, nil
}

// uploadMedia 上传媒体文件。
func (a *QQBotAdapter) uploadMedia(ctx context.Context, chatID, fileURL string, mediaType int) (string, error) {
	if !isSafeURL(fileURL) {
		return "", fmt.Errorf("URL 不安全: %s", fileURL)
	}
	// QQ Bot 媒体上传: 先下载文件，再通过 /v2/users/{openid}/files 上传
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return "", fmt.Errorf("创建下载请求失败: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载媒体文件失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载媒体文件返回 HTTP %d", resp.StatusCode)
	}

	// 限制下载大小 (10MB)
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", fmt.Errorf("读取媒体文件失败: %w", err)
	}

	// 构建上传请求
	var endpoint string
	if strings.HasPrefix(chatID, "group:") {
		groupID := strings.TrimPrefix(chatID, "group:")
		endpoint = fmt.Sprintf("/v2/groups/%s/files", groupID)
	} else {
		endpoint = fmt.Sprintf("/v2/users/%s/files", chatID)
	}

	// 使用 callAPI 上传
	uploadBody := map[string]any{
		"file_type": mediaType,
		"file_data": data,
	}
	result, err := a.callAPI(ctx, endpoint, uploadBody)
	if err != nil {
		// 上传失败时回退到 URL 格式
		slog.Warn("QQ Bot media upload failed, falling back to URL format", "err", err)
		return fmt.Sprintf("url:%s", fileURL), nil
	}

	if fileUUID, ok := result["file_uuid"].(string); ok {
		return fileUUID, nil
	}
	return fmt.Sprintf("url:%s", fileURL), nil
}

// sendMediaMessage 发送已上传的媒体消息（供 SendVoice/SendVideo/SendDocument 复用）。
func (a *QQBotAdapter) sendMediaMessage(ctx context.Context, chatID, fileInfo, caption string) (*SendResult, error) {
	if err := validateChatID(chatID); err != nil {
		return nil, err
	}

	var endpoint string
	var body map[string]any

	if strings.HasPrefix(chatID, "group:") {
		groupOpenID := strings.TrimPrefix(chatID, "group:")
		endpoint = fmt.Sprintf("/v2/groups/%s/messages", groupOpenID)
		body = map[string]any{
			"msg_type": qqbotMsgTypeMedia,
			"msg_id":   generateMsgID(),
			"media": map[string]any{
				"file_info": fileInfo,
			},
		}
	} else {
		endpoint = fmt.Sprintf("/v2/users/%s/messages", chatID)
		body = map[string]any{
			"msg_type": qqbotMsgTypeMedia,
			"msg_id":   generateMsgID(),
			"media": map[string]any{
				"file_info": fileInfo,
			},
		}
	}

	if caption != "" {
		body["content"] = caption
	}

	resp, err := a.callAPI(ctx, endpoint, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	return &SendResult{Success: true, MessageID: getString(resp, "id", "")}, nil
}

// SendTyping 发送正在输入指示。
func (a *QQBotAdapter) SendTyping(ctx context.Context, chatID string) error {
	if err := validateChatID(chatID); err != nil {
		return err
	}
	if strings.HasPrefix(chatID, "group:") {
		return nil // 群聊不支持输入指示
	}

	endpoint := fmt.Sprintf("/v2/users/%s/messages", chatID)
	body := map[string]any{
		"msg_type": qqbotMsgTypeInputNotify,
		"msg_id":   generateMsgID(),
	}

	_, err := a.callAPI(ctx, endpoint, body)
	return err
}

// ───────────────────────────── 权限检查 ─────────────────────────────

// isDMAllowed 检查私聊权限。
func (a *QQBotAdapter) isDMAllowed(userID string) bool {
	if a.dmPolicy == "disabled" {
		return false
	}
	if a.dmPolicy == "allowlist" {
		return entryMatches(a.allowFrom, userID)
	}
	return true
}

// isGroupAllowed 检查群聊权限。
func (a *QQBotAdapter) isGroupAllowed(groupID, userID string) bool {
	if a.groupPolicy == "disabled" {
		return false
	}
	if a.groupPolicy == "allowlist" {
		return entryMatches(a.groupAllowFrom, groupID)
	}
	return true
}

// ───────────────────────────── API 调用 ─────────────────────────────

// callAPI 调用 QQ Bot REST API。
func (a *QQBotAdapter) callAPI(ctx context.Context, endpoint string, body map[string]any) (map[string]any, error) {
	url := a.apiURL + endpoint

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	token := a.accessToken
	a.mu.Unlock()

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-App-Id", a.appID)

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
		return nil, fmt.Errorf("API 错误 (HTTP %d)", resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return result, nil
}

// callExternalAPI 调用外部 API。
func (a *QQBotAdapter) callExternalAPI(ctx context.Context, url string, body map[string]any) (map[string]any, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("外部 API 错误 (HTTP %d)", resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return result, nil
}

// ───────────────────────────── 接口实现 ─────────────────────────────

// Name 返回适配器名称。
func (a *QQBotAdapter) Name() string { return "QQBot" }

// PlatformType 返回平台类型。
func (a *QQBotAdapter) PlatformType() Platform { return PlatformQQBot }

// EditMessage 编辑消息（QQ Bot 不支持）。
func (a *QQBotAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "QQ Bot 不支持编辑消息"}, nil
}

// DeleteMessage 删除消息（QQ Bot 不支持）。
func (a *QQBotAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("QQ Bot 不支持删除消息")
}

// SendVoice 发送语音。
func (a *QQBotAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	if err := validateChatID(chatID); err != nil {
		return nil, err
	}
	fileInfo, err := a.uploadMedia(ctx, chatID, audioPath, qqbotMediaTypeVoice)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("上传语音失败: %v", err)}, nil
	}

	return a.sendMediaMessage(ctx, chatID, fileInfo, "")
}

// SendVideo 发送视频。
// 直接构造媒体消息，不委托给 SendImage（避免重复上传）。
func (a *QQBotAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	if err := validateChatID(chatID); err != nil {
		return nil, err
	}
	fileInfo, err := a.uploadMedia(ctx, chatID, videoPath, qqbotMediaTypeVideo)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("上传视频失败: %v", err)}, nil
	}

	return a.sendMediaMessage(ctx, chatID, fileInfo, caption)
}

// SendDocument 发送文件。
// 直接构造媒体消息，不委托给 SendImage（避免重复上传）。
func (a *QQBotAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	if err := validateChatID(chatID); err != nil {
		return nil, err
	}
	fileInfo, err := a.uploadMedia(ctx, chatID, filePath, qqbotMediaTypeFile)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("上传文件失败: %v", err)}, nil
	}

	return a.sendMediaMessage(ctx, chatID, fileInfo, caption)
}

// MaxMessageLength 返回最大消息长度。
func (a *QQBotAdapter) MaxMessageLength() int { return 5000 }

// SupportsStreaming 返回是否支持流式输出。
func (a *QQBotAdapter) SupportsStreaming() bool { return false }

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformQQBot,
		Name:     "QQBot",
		Factory:  func() PlatformAdapter { return NewQQBotAdapter(nil) },
	})
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func generateMsgID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// validateChatID 验证 chatID/groupOpenID 只含安全字符，防止路径注入。
func validateChatID(id string) error {
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == ':') {
			return fmt.Errorf("无效的 chat ID")
		}
	}
	return nil
}
