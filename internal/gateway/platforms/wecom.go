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

	// 并发控制
	mu            sync.Mutex
	pendingResps  map[string]chan map[string]any
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
		pendingResps:   make(map[string]chan map[string]any),
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
	go a.listenLoop()

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

	if err := conn.WriteJSON(subscribeReq); err != nil {
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
func (a *WeComAdapter) listenLoop() {
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
			time.Sleep(delay)

			ctx, cancel := context.WithTimeout(context.Background(), wecomConnectTimeout)
			newConn, err := a.openConnection(ctx)
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
	reqID := a.payloadReqID(payload)
	cmd := getString(payload, "cmd", "")

	// 检查是否是等待的响应
	if reqID != "" {
		a.mu.Lock()
		ch, exists := a.pendingResps[reqID]
		if exists {
			delete(a.pendingResps, reqID)
			a.mu.Unlock()
			ch <- payload
			return
		}
		a.mu.Unlock()
	}

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
			a.mu.Lock()
			running := a.running
			conn := a.conn
			a.mu.Unlock()

			if !running || conn == nil {
				return
			}

			pingReq := map[string]any{
				"cmd": wecomCmdPing,
				"headers": map[string]any{"req_id": a.newReqID("ping")},
				"body": map[string]any{},
			}

			a.mu.Lock()
			if err := conn.WriteJSON(pingReq); err != nil {
				a.mu.Unlock()
				slog.Debug("[WeCom] heartbeat send failed", "err", err)
			} else {
				a.mu.Unlock()
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
		text = strings.TrimSpace(strings.Replace(text, "@"+a.botID, "", 1))
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

	// 回调处理
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

	// 检查是否有缓存的回复 req_id
	replyReqID := a.getReplyReqID(chatID)

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

	// 注册响应通道 + 发送请求（原子操作，防止 flush 竞态）
	respCh := make(chan map[string]any, 1)
	a.mu.Lock()
	a.pendingResps[reqID] = respCh
	if err := conn.WriteJSON(req); err != nil {
		delete(a.pendingResps, reqID)
		a.mu.Unlock()
		return nil, fmt.Errorf("发送失败: %w", err)
	}
	a.mu.Unlock()

	// 等待响应
	select {
	case resp := <-respCh:
		errcode := getInt(resp, "errcode", 0)
		if errcode != 0 {
			errmsg := getString(resp, "errmsg", "发送失败")
			return nil, fmt.Errorf("发送失败: %s (errcode=%d)", errmsg, errcode)
		}
		return &SendResult{Success: true}, nil

	case <-time.After(wecomRequestTimeout):
		a.mu.Lock()
		delete(a.pendingResps, reqID)
		a.mu.Unlock()
		return nil, fmt.Errorf("发送超时")
	}
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
func (a *WeComAdapter) PlatformType() Platform   { return PlatformWeChat }
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
func (a *WeComAdapter) isGroupAllowed(chatID, senderID string) bool {
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
	a.replyReqIDs[msgID] = reqID
	// 清理超过上限的条目
	if len(a.replyReqIDs) > wecomDedupMaxSize {
		for key := range a.replyReqIDs {
			delete(a.replyReqIDs, key)
			break
		}
	}
	a.mu.Unlock()
}

func (a *WeComAdapter) rememberChatReqID(chatID, reqID string) {
	if chatID == "" || reqID == "" {
		return
	}
	a.mu.Lock()
	a.lastChatReqIDs[chatID] = reqID
	if len(a.lastChatReqIDs) > wecomDedupMaxSize {
		for key := range a.lastChatReqIDs {
			delete(a.lastChatReqIDs, key)
			break
		}
	}
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

// ───────────────────────────── 配置辅助函数 ─────────────────────────────

func getString(m map[string]any, key string, defaultVal string) string {
	if m == nil {
		return defaultVal
	}
	val, ok := m[key]
	if !ok {
		return defaultVal
	}
	str, ok := val.(string)
	if !ok {
		return defaultVal
	}
	return strings.TrimSpace(str)
}

func getInt(m map[string]any, key string, defaultVal int) int {
	if m == nil {
		return defaultVal
	}
	val, ok := m[key]
	if !ok {
		return defaultVal
	}
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return defaultVal
	}
}

func getMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	val, ok := m[key]
	if !ok {
		return nil
	}
	mapVal, ok := val.(map[string]any)
	if !ok {
		return nil
	}
	return mapVal
}

func getList(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	val, ok := m[key]
	if !ok {
		return nil
	}

	switch v := val.(type) {
	case []string:
		return v
	case []any:
		result := make([]string, len(v))
		for i, item := range v {
			result[i] = strings.TrimSpace(fmt.Sprintf("%v", item))
		}
		return result
	case string:
		// 逗号分隔的字符串
		parts := strings.Split(v, ",")
		result := make([]string, len(parts))
		for i, p := range parts {
			result[i] = strings.TrimSpace(p)
		}
		return result
	default:
		return nil
	}
}

func getListAny(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	val, ok := m[key]
	if !ok {
		return nil
	}
	list, ok := val.([]any)
	if !ok {
		return nil
	}
	return list
}

func getEnvList(envKey string) []string {
	val := os.Getenv(envKey)
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	result := make([]string, len(parts))
	for i, p := range parts {
		result[i] = strings.TrimSpace(p)
	}
	return result
}