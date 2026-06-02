// Package platforms 提供企业微信平台适配器。
// 通过 WebSocket 连接企业微信 AI Bot Gateway 进行消息收发。
package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/gorilla/websocket"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	wecomDefaultWSURL      = "wss://openws.work.weixin.qq.com"
	wecomMaxMessageLength  = 4000
	wecomConnectTimeout    = 20 * time.Second
	wecomRequestTimeout    = 15 * time.Second
	wecomHeartbeatInterval = 30 * time.Second
	wecomDedupMaxSize      = 1000
	wecomMapMaxSize        = 1000
)

// WebSocket 命令类型
const (
	wecomCmdSubscribe     = "aibot_subscribe"
	wecomCmdCallback      = "aibot_msg_callback"
	wecomCmdSend          = "aibot_send_msg"
	wecomCmdRespond       = "aibot_respond_msg"
	wecomCmdPing          = "ping"
	wecomCmdUploadInit    = "aibot_upload_media_init"
	wecomCmdUploadChunk   = "aibot_upload_media_chunk"
	wecomCmdUploadFinish  = "aibot_upload_media_finish"
)

// 重连退避时间
var wecomReconnectBackoff = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}

// ───────────────────────────── WeComAdapter ─────────────────────────────

// WeComAdapter 企业微信适配器。
type WeComAdapter struct {
	botID       string
	secret      string
	wsURL       string
	dmPolicy    string
	allowFrom   []string
	groupPolicy string
	groupAllowFrom []string

	// 连接状态
	conn        *websocket.Conn
	running     bool
	connected   bool
	msgCh       chan *MessageEvent
	closeOnce   sync.Once

	// 并发控制
	mu            sync.Mutex
	writeMu       sync.Mutex // WebSocket 写锁，保护并发 WriteJSON
	dedup         *wecomDeduplicator
	replyReqIDs   map[string]string
	lastChatReqIDs map[string]string

	// 设备 ID
	deviceID string
}

// wecomDeduplicator 消息去重器。
type wecomDeduplicator struct {
	mu     sync.Mutex
	msgIDs map[string]time.Time
	maxSize int
}

func newWecomDeduplicator(maxSize int) *wecomDeduplicator {
	return &wecomDeduplicator{
		msgIDs:   make(map[string]time.Time),
		maxSize: maxSize,
	}
}

func (d *wecomDeduplicator) isDuplicate(msgID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.msgIDs[msgID]; exists {
		return true
	}

	d.msgIDs[msgID] = time.Now()

	// 清理过期条目
	if len(d.msgIDs) > d.maxSize {
		for id, t := range d.msgIDs {
			if time.Since(t) > 5 * time.Minute {
				delete(d.msgIDs, id)
			}
		}
	}

	return false
}

// writeWS 安全地向 WebSocket 写入 JSON 消息（加写锁）。
func (a *WeComAdapter) writeWS(conn *websocket.Conn, v any) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return conn.WriteJSON(v)
}

// NewWeComAdapter 创建企业微信适配器。
func NewWeComAdapter(botID, secret string) *WeComAdapter {
	wsURL := os.Getenv("WECOM_WEBSOCKET_URL")
	if wsURL == "" {
		wsURL = wecomDefaultWSURL
	}

	dmPolicy := os.Getenv("WECOM_DM_POLICY")
	if dmPolicy == "" {
		dmPolicy = "open"
	}

	groupPolicy := os.Getenv("WECOM_GROUP_POLICY")
	if groupPolicy == "" {
		groupPolicy = "open"
	}

	return &WeComAdapter{
		botID:          botID,
		secret:         secret,
		wsURL:          wsURL,
		dmPolicy:       dmPolicy,
		groupPolicy:    groupPolicy,
		allowFrom:      getEnvList("WECOM_ALLOW_FROM"),
		groupAllowFrom: getEnvList("WECOM_GROUP_ALLOW_FROM"),
		msgCh:          make(chan *MessageEvent, 64),
		dedup:          newWecomDeduplicator(wecomDedupMaxSize),
		replyReqIDs:    make(map[string]string),
		lastChatReqIDs: make(map[string]string),
		deviceID:       uuid.New().String(),
	}
}

// ───────────────────────────── 连接生命周期 ─────────────────────────────

// Connect 连接到企业微信 Gateway。
func (a *WeComAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if a.botID == "" || a.secret == "" {
		return nil, fmt.Errorf("WECOM_BOT_ID 和 WECOM_SECRET 是必填项")
	}

	a.mu.Lock()
	a.running = true
	a.mu.Unlock()

	// 连接 WebSocket
	conn, err := a.openConnection(ctx)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return nil, fmt.Errorf("连接失败: %w", err)
	}

	a.mu.Lock()
	a.conn = conn
	a.connected = true
	a.mu.Unlock()

	// 启动监听循环
	go a.listenLoop(ctx)

	// 启动心跳循环
	go a.heartbeatLoop(ctx)

	slog.Info("[WeCom] connected", "url", a.wsURL)
	return a.msgCh, nil
}

// Disconnect 断开连接。
func (a *WeComAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.connected = false
	conn := a.conn
	a.conn = nil
	a.mu.Unlock()

	if conn != nil {
		conn.Close()
	}
	// listenLoop 退出时负责关闭 msgCh，这里不再重复关闭

	slog.Info("[WeCom] disconnected")
	return nil
}

// openConnection 打开并认证 WebSocket 连接。
func (a *WeComAdapter) openConnection(ctx context.Context) (*websocket.Conn, error) {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, a.wsURL, nil)
	if err != nil {
		return nil, err
	}

	// 发送订阅请求
	reqID := a.newReqID("subscribe")
	subscribeReq := map[string]any{
		"cmd": wecomCmdSubscribe,
		"headers": map[string]any{"req_id": reqID},
		"body": map[string]any{
			"bot_id":    a.botID,
			"secret":    a.secret,
			"device_id": a.deviceID,
		},
	}

	if err := a.writeWS(conn, subscribeReq); err != nil {
		conn.Close()
		return nil, fmt.Errorf("发送订阅请求失败: %w", err)
	}

	// 等待认证响应
	_, msg, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取认证响应失败: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(msg, &resp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("解析认证响应失败: %w", err)
	}

	errcode := getInt(resp, "errcode", 0)
	if errcode != 0 {
		errmsg := getString(resp, "errmsg", "认证失败")
		conn.Close()
		return nil, fmt.Errorf("认证失败: %s (errcode=%d)", errmsg, errcode)
	}

	return conn, nil
}

// ───────────────────────────── 监听循环 ─────────────────────────────

// listenLoop WebSocket 消息监听循环。
func (a *WeComAdapter) listenLoop(ctx context.Context) {
	defer a.closeOnce.Do(func() { close(a.msgCh) })

	backoffIdx := 0

	for {
		a.mu.Lock()
		running := a.running
		conn := a.conn
		a.mu.Unlock()

		if !running {
			return
		}

		if conn == nil {
			// 尝试重连
			delay := wecomReconnectBackoff[min(backoffIdx, len(wecomReconnectBackoff)-1)]
			backoffIdx++
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}

			newCtx, cancel := context.WithTimeout(ctx, wecomConnectTimeout)
			newConn, err := a.openConnection(newCtx)
			cancel()

			if err != nil {
				slog.Warn("[WeCom] reconnect failed", "err", err)
				continue
			}

			a.mu.Lock()
			a.conn = newConn
			a.connected = true
			a.mu.Unlock()

			backoffIdx = 0
			slog.Info("[WeCom] reconnected")
			continue
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			slog.Warn("[WeCom] failed to read message", "err", err)
			a.mu.Lock()
			a.connected = false
			if a.conn != nil {
				a.conn.Close()
				a.conn = nil
			}
			a.mu.Unlock()
			continue
		}

		var payload map[string]any
		if err := json.Unmarshal(msg, &payload); err != nil {
			continue
		}

		a.dispatchPayload(payload)
	}
}

// dispatchPayload 处理接收到的 WebSocket 消息。
func (a *WeComAdapter) dispatchPayload(payload map[string]any) {
	cmd := getString(payload, "cmd", "")

	// 处理消息回调
	if cmd == wecomCmdCallback {
		a.onMessage(payload)
		return
	}

	// 忽略 ping 和其他命令
	if cmd == wecomCmdPing {
		return
	}

	slog.Debug("[WeCom] ignoring unknown message", "cmd", cmd)
}

// ───────────────────────────── 心跳循环 ─────────────────────────────

// heartbeatLoop 发送心跳。
func (a *WeComAdapter) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(wecomHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingReq := map[string]any{
				"cmd": wecomCmdPing,
				"headers": map[string]any{"req_id": a.newReqID("ping")},
				"body": map[string]any{},
			}

			a.mu.Lock()
			conn := a.conn
			a.mu.Unlock()

			if conn == nil {
				continue
			}
			if err := a.writeWS(conn, pingReq); err != nil {
				slog.Debug("[WeCom] heartbeat send failed", "err", err)
			}
		}
	}
}

// ───────────────────────────── 消息处理 ─────────────────────────────

// onMessage 处理接收到的消息回调。
func (a *WeComAdapter) onMessage(payload map[string]any) {
	body := getMap(payload, "body")
	if body == nil {
		return
	}

	msgID := getString(body, "msgid", a.payloadReqID(payload))
	if msgID == "" {
		msgID = uuid.New().String()
	}

	// 去重检查
	if a.dedup.isDuplicate(msgID) {
		slog.Debug("[WeCom] duplicate message ignored", "msg_id", msgID)
		return
	}

	// 记录回复 req_id
	reqID := a.payloadReqID(payload)
	a.rememberReplyReqID(msgID, reqID)

	// 提取发送者信息
	sender := getMap(body, "from")
	senderID := getString(sender, "userid", "")
	chatID := getString(body, "chatid", senderID)

	if chatID == "" {
		return
	}

	// 检查群组/私聊权限
	isGroup := getString(body, "chattype", "") == "group"
	if isGroup {
		if !a.isGroupAllowed(chatID, senderID) {
			return
		}
	} else {
		if !a.isDMAllowed(senderID) {
			return
		}
	}

	// 记录聊天的 req_id（用于群聊回复）
	a.rememberChatReqID(chatID, reqID)

	// 提取文本
	text := a.extractText(body)

	// 去除群聊中的 @mention
	if isGroup && text != "" {
		text = strings.TrimSpace(strings.ReplaceAll(text, "@"+a.botID, ""))
	}

	if text == "" {
		return
	}

	// 构建消息事件
	chatType := "dm"
	if isGroup {
		chatType = "group"
	}

	event := &MessageEvent{
		Text:        text,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformWeCom,
			ChatID:   chatID,
			UserID:   senderID,
			ChatType: chatType,
		},
		RawMessage: payload,
	}

	select {
	case a.msgCh <- event:
	default:
		slog.Warn("[WeCom] message channel full, dropping message", "msg_id", msgID)
	}
}

// extractText 从消息体提取文本。
func (a *WeComAdapter) extractText(body map[string]any) string {
	msgType := getString(body, "msgtype", "")

	if msgType == "mixed" {
		mixed := getMap(body, "mixed")
		items := getListAny(mixed, "msg_item")
		var parts []string
		for _, item := range items {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if getString(itemMap, "msgtype", "") == "text" {
				textBlock := getMap(itemMap, "text")
				content := getString(textBlock, "content", "")
				if content != "" {
					parts = append(parts, content)
				}
			}
		}
		return strings.Join(parts, "\n")
	}

	textBlock := getMap(body, "text")
	return getString(textBlock, "content", "")
}

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送 Markdown 消息。
func (a *WeComAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return nil, fmt.Errorf("chat_id 是必填项")
	}

	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return nil, fmt.Errorf("WebSocket 未连接")
	}

	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, fmt.Errorf("消息内容为空")
	}

	// 检查是否有缓存的回复 req_id
	replyReqID := a.getReplyReqID(chatID)

	reqID := a.newReqID("send")
	req := map[string]any{
		"cmd": wecomCmdSend,
		"headers": map[string]any{"req_id": reqID},
		"body": map[string]any{
			"chatid": chatID,
			"msgtype": "markdown",
			"markdown": map[string]any{
				"content": trimmed[:min(len(trimmed), wecomMaxMessageLength)],
			},
		},
	}

	// 如果有回复 req_id，使用 respond 命令
	if replyReqID != "" {
		req["cmd"] = wecomCmdRespond
		req["headers"] = map[string]any{"req_id": replyReqID}
	}

	if err := a.writeWS(conn, req); err != nil {
		return nil, fmt.Errorf("发送失败: %w", err)
	}

	return &SendResult{Success: true}, nil
}

// SendImage 发送图片。
func (a *WeComAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	// 企业微信需要先上传媒体，简化为发送 URL 文本
	result, err := a.Send(ctx, chatID, imageURL, opts)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (a *WeComAdapter) Name() string            { return "WeCom" }
func (a *WeComAdapter) PlatformType() Platform   { return PlatformWeCom }
func (a *WeComAdapter) MaxMessageLength() int     { return wecomMaxMessageLength }
func (a *WeComAdapter) SupportsStreaming() bool   { return false }
func (a *WeComAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return a.Send(ctx, chatID, content, nil)
}
func (a *WeComAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return nil
}
func (a *WeComAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}
func (a *WeComAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return nil, fmt.Errorf("企业微信暂不支持语音消息")
}
func (a *WeComAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return nil, fmt.Errorf("企业微信暂不支持视频消息")
}
func (a *WeComAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return nil, fmt.Errorf("企业微信暂不支持文件消息")
}

// ───────────────────────────── 权限检查 ─────────────────────────────

// isDMAllowed 检查私聊权限。
func (a *WeComAdapter) isDMAllowed(senderID string) bool {
	if a.dmPolicy == "disabled" {
		return false
	}
	if a.dmPolicy == "allowlist" {
		return entryMatches(a.allowFrom, senderID)
	}
	return true
}

// isGroupAllowed 检查群聊权限。
func (a *WeComAdapter) isGroupAllowed(chatID, _ string) bool {
	if a.groupPolicy == "disabled" {
		return false
	}
	if a.groupPolicy == "allowlist" {
		return entryMatches(a.groupAllowFrom, chatID)
	}
	return true
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func (a *WeComAdapter) newReqID(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, uuid.New().String()[:8])
}

func (a *WeComAdapter) payloadReqID(payload map[string]any) string {
	headers := getMap(payload, "headers")
	return getString(headers, "req_id", "")
}

func (a *WeComAdapter) rememberReplyReqID(msgID, reqID string) {
	if msgID == "" || reqID == "" {
		return
	}
	a.mu.Lock()
	if len(a.replyReqIDs) > wecomMapMaxSize {
		for k := range a.replyReqIDs {
			delete(a.replyReqIDs, k)
			if len(a.replyReqIDs) <= wecomMapMaxSize/2 {
				break
			}
		}
	}
	a.replyReqIDs[msgID] = reqID
	a.mu.Unlock()
}

func (a *WeComAdapter) rememberChatReqID(chatID, reqID string) {
	if chatID == "" || reqID == "" {
		return
	}
	a.mu.Lock()
	if len(a.lastChatReqIDs) > wecomMapMaxSize {
		for k := range a.lastChatReqIDs {
			delete(a.lastChatReqIDs, k)
			if len(a.lastChatReqIDs) <= wecomMapMaxSize/2 {
				break
			}
		}
	}
	a.lastChatReqIDs[chatID] = reqID
	a.mu.Unlock()
}

func (a *WeComAdapter) getReplyReqID(chatID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastChatReqIDs[chatID]
}

// entryMatches 检查条目是否匹配（支持 * 通配符）。
func entryMatches(entries []string, target string) bool {
	targetLower := strings.ToLower(strings.TrimSpace(target))
	for _, entry := range entries {
		normalized := strings.ToLower(strings.TrimSpace(entry))
		if normalized == "*" || normalized == targetLower {
			return true
		}
	}
	return false
}
