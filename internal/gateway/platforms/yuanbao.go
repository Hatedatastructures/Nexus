// Package platforms 提供元宝平台适配器。
// 通过 WebSocket 连接元宝 Bot Gateway 进行消息收发。
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

	"github.com/google/uuid"

	"github.com/gorilla/websocket"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	yuanbaoDefaultWSURL      = "wss://bot-wss.yuanbao.tencent.com/wss/connection"
	yuanbaoDefaultAPIDomain  = "https://bot.yuanbao.tencent.com"
	yuanbaoHeartbeatInterval = 30 * time.Second
	yuanbaoConnectTimeout    = 15 * time.Second
	yuanbaoAuthTimeout       = 10 * time.Second
	yuanbaoMaxReconnect      = 100
	yuanbaoSendTimeout       = 30 * time.Second
)

// WebSocket 命令类型 (简化版)
const (
	yuanbaoCmdAuthBind    = "AUTH_BIND"
	yuanbaoCmdPing        = "PING"
	yuanbaoCmdPong        = "PONG"
	yuanbaoCmdT05         = "T05" // 接收消息
	yuanbaoCmdT06         = "T06" // 发送消息
	yuanbaoCmdHeartbeat   = "HEARTBEAT"
)

// ───────────────────────────── YuanbaoAdapter ─────────────────────────────

// YuanbaoAdapter 元宝平台适配器。
type YuanbaoAdapter struct {
	appID      string
	appSecret  string
	botID      string
	wsURL      string
	apiDomain  string
	messageHandler func(*MessageEvent)

	// WebSocket 连接
	conn      *websocket.Conn
	running   bool
	connected bool

	// 并发控制
	mu             sync.Mutex
	pendingResps   map[string]chan map[string]any
	dedup          *yuanbaoDeduplicator
	instanceID     string
	seqNo          int64

	// 群信息缓存
	groupInfoCache map[string]*YuanbaoGroupInfo
}

// YuanbaoGroupInfo 群信息。
type YuanbaoGroupInfo struct {
	GroupCode    string `json:"group_code"`
	GroupName    string `json:"group_name"`
	MemberCount  int    `json:"member_count"`
	OwnerID      string `json:"owner_id"`
}

// YuanbaoMember 群成员。
type YuanbaoMember struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	UserType int    `json:"user_type"`
}

// yuanbaoDeduplicator 消息去重器。
type yuanbaoDeduplicator struct {
	mu      sync.Mutex
	msgIDs  map[string]time.Time
	maxSize int
}

func newYuanbaoDeduplicator(maxSize int) *yuanbaoDeduplicator {
	return &yuanbaoDeduplicator{
		msgIDs:  make(map[string]time.Time),
		maxSize: maxSize,
	}
}

func (d *yuanbaoDeduplicator) isDuplicate(msgID string) bool {
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

// NewYuanbaoAdapter 创建元宝适配器。
func NewYuanbaoAdapter(appID, appSecret string, messageHandler func(*MessageEvent)) *YuanbaoAdapter {
	wsURL := os.Getenv("YUANBAO_WS_URL")
	if wsURL == "" {
		wsURL = yuanbaoDefaultWSURL
	}

	apiDomain := os.Getenv("YUANBAO_API_DOMAIN")
	if apiDomain == "" {
		apiDomain = yuanbaoDefaultAPIDomain
	}

	return &YuanbaoAdapter{
		appID:           appID,
		appSecret:       appSecret,
		wsURL:           wsURL,
		apiDomain:       apiDomain,
		messageHandler:  messageHandler,
		pendingResps:    make(map[string]chan map[string]any),
		dedup:           newYuanbaoDeduplicator(1000),
		instanceID:      uuid.New().String(),
		seqNo:           0,
		groupInfoCache:  make(map[string]*YuanbaoGroupInfo),
	}
}

// ───────────────────────────── 连接生命周期 ─────────────────────────────

// Connect 连接到元宝 Gateway。
func (a *YuanbaoAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if a.appID == "" || a.appSecret == "" {
		return nil, fmt.Errorf("YUANBAO_APP_ID 和 YUANBAO_APP_SECRET 是必填项")
	}

	a.mu.Lock()
	a.running = true
	a.mu.Unlock()

	// 获取 sign-token
	token, botID, err := a.getSignToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取 sign-token 失败: %w", err)
	}
	a.botID = botID

	// 连接 WebSocket
	conn, err := a.openConnection(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("连接失败: %w", err)
	}

	a.conn = conn
	a.connected = true

	// 创建消息通道
	msgCh := make(chan *MessageEvent, 100)

	// 启动监听循环
	go a.listenLoop(msgCh)

	// 启动心跳循环
	go a.heartbeatLoop()

	slog.Info("[Yuanbao] connected", "bot_id", a.botID)
	return msgCh, nil
}

// Disconnect 断开连接。
func (a *YuanbaoAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.connected = false
	a.mu.Unlock()

	if a.conn != nil {
		a.conn.Close()
		a.conn = nil
	}

	slog.Info("[Yuanbao] disconnected")
	return nil
}

// getSignToken 获取认证 token。
func (a *YuanbaoAdapter) getSignToken(ctx context.Context) (string, string, error) {
	// 调用 sign-token API
	body := map[string]any{
		"app_id":     a.appID,
		"app_secret": a.appSecret,
		"instance_id": a.instanceID,
	}

	resp, err := a.callAPI(ctx, "/api/sign-token", body, 10*time.Second)
	if err != nil {
		return "", "", err
	}

	errcode := getInt(resp, "errcode", 0)
	if errcode != 0 {
		errmsg := getString(resp, "errmsg", "认证失败")
		return "", "", fmt.Errorf("认证失败: %s (errcode=%d)", errmsg, errcode)
	}

	token := getString(resp, "token", "")
	botID := getString(resp, "bot_id", "")

	if token == "" {
		return "", "", fmt.Errorf("token 未返回")
	}

	return token, botID, nil
}

// openConnection 打开并认证 WebSocket 连接。
func (a *YuanbaoAdapter) openConnection(ctx context.Context, token string) (*websocket.Conn, error) {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, a.wsURL, nil)
	if err != nil {
		return nil, err
	}

	// 发送 AUTH_BIND
	authReq := map[string]any{
		"cmd":       yuanbaoCmdAuthBind,
		"token":     token,
		"instance_id": a.instanceID,
		"seq_no":    a.nextSeqNo(),
	}

	if err := conn.WriteJSON(authReq); err != nil {
		conn.Close()
		return nil, fmt.Errorf("发送认证请求失败: %w", err)
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
func (a *YuanbaoAdapter) listenLoop(msgCh chan *MessageEvent) {
	for {
		a.mu.Lock()
		running := a.running
		conn := a.conn
		a.mu.Unlock()

		if !running {
			close(msgCh)
			return
		}

		if conn == nil {
			// 尝试重连
			time.Sleep(5 * time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), yuanbaoConnectTimeout)

			token, _, err := a.getSignToken(ctx)
			cancel()

			if err != nil {
				slog.Warn("[Yuanbao] failed to get token", "err", err)
				continue
			}

			newConn, err := a.openConnection(context.Background(), token)
			if err != nil {
				slog.Warn("[Yuanbao] reconnect failed", "err", err)
				continue
			}

			a.mu.Lock()
			a.conn = newConn
			a.connected = true
			a.mu.Unlock()

			slog.Info("[Yuanbao] reconnected")
			continue
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			slog.Warn("[Yuanbao] failed to read message", "err", err)
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

		a.dispatchPayload(payload, msgCh)
	}
}

// dispatchPayload 处理接收到的 WebSocket 消息。
func (a *YuanbaoAdapter) dispatchPayload(payload map[string]any, msgCh chan *MessageEvent) {
	cmd := getString(payload, "cmd", "")

	switch cmd {
	case yuanbaoCmdT05:
		// 接收消息
		event := a.parseMessage(payload)
		if event != nil && !a.dedup.isDuplicate(event.MessageID) {
			msgCh <- event
		}
	case yuanbaoCmdPong:
		// 心跳响应
		return
	default:
		// 检查是否是等待的响应
		seqNo := getInt(payload, "seq_no", 0)
		if seqNo > 0 {
			seqNoStr := fmt.Sprintf("%d", seqNo)
			a.mu.Lock()
			ch, exists := a.pendingResps[seqNoStr]
			if exists {
				delete(a.pendingResps, seqNoStr)
				a.mu.Unlock()
				ch <- payload
				return
			}
			a.mu.Unlock()
		}
	}
}

// parseMessage 解析消息。
func (a *YuanbaoAdapter) parseMessage(payload map[string]any) *MessageEvent {
	body := getMap(payload, "body")
	if body == nil {
		return nil
	}

	msgID := getString(body, "msg_id", "")
	if msgID == "" {
		msgID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	// 提取发送者信息
	fromUser := getMap(body, "from_user")
	userID := getString(fromUser, "user_id", "")

	// 提取聊天信息
	chatID := getString(body, "chat_id", userID)
	chatType := getString(body, "chat_type", "c2c")
	isGroup := chatType == "group"

	// 提取文本
	text := ""
	msgType := getString(body, "msg_type", "text")
	if msgType == "text" {
		textBlock := getMap(body, "text")
		text = getString(textBlock, "content", "")
	}

	if text == "" {
		return nil
	}

	// 去除群聊中的 @mention
	if isGroup && text != "" {
		text = strings.TrimSpace(strings.Replace(text, "@"+a.botID, "", 1))
	}

	return &MessageEvent{
		Text:        text,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformWeChat, // 元宝使用微信平台标识
			ChatID:   chatID,
			UserID:   userID,
			ChatType: chatType,
		},
		RawMessage: payload,
	}
}

// ───────────────────────────── 心跳循环 ─────────────────────────────

// heartbeatLoop 发送心跳。
func (a *YuanbaoAdapter) heartbeatLoop() {
	ticker := time.NewTicker(yuanbaoHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.mu.Lock()
			running := a.running
			conn := a.conn
			a.mu.Unlock()

			if !running || conn == nil {
				return
			}

			pingReq := map[string]any{
				"cmd":    yuanbaoCmdPing,
				"seq_no": a.nextSeqNo(),
			}

			if err := conn.WriteJSON(pingReq); err != nil {
				slog.Debug("[Yuanbao] heartbeat send failed", "err", err)
			}
		}
	}
}

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送文本消息。
func (a *YuanbaoAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chat_id 是必填项"}, nil
	}

	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return &SendResult{Success: false, Error: "WebSocket 未连接"}, nil
	}

	seqNo := a.nextSeqNo()
	chatType := "c2c"
	if strings.HasPrefix(chatID, "group:") {
		chatType = "group"
		chatID = strings.TrimPrefix(chatID, "group:")
	}

	req := map[string]any{
		"cmd":      yuanbaoCmdT06,
		"seq_no":   seqNo,
		"chat_id":  chatID,
		"chat_type": chatType,
		"msg_type":  "text",
		"text": map[string]any{
			"content": content,
		},
	}

	// 发送请求并等待响应
	respCh := make(chan map[string]any, 1)
	a.mu.Lock()
	a.pendingResps[fmt.Sprintf("%d", seqNo)] = respCh
	a.mu.Unlock()

	if err := conn.WriteJSON(req); err != nil {
		a.mu.Lock()
		delete(a.pendingResps, fmt.Sprintf("%d", seqNo))
		a.mu.Unlock()
		return &SendResult{Success: false, Error: fmt.Sprintf("发送失败: %v", err)}, nil
	}

	select {
	case resp := <-respCh:
		errcode := getInt(resp, "errcode", 0)
		if errcode != 0 {
			errmsg := getString(resp, "errmsg", "发送失败")
			return &SendResult{Success: false, Error: fmt.Sprintf("%s (errcode=%d)", errmsg, errcode)}, nil
		}
		msgID := getString(resp, "msg_id", "")
		return &SendResult{Success: true, MessageID: msgID}, nil

	case <-time.After(yuanbaoSendTimeout):
		a.mu.Lock()
		delete(a.pendingResps, fmt.Sprintf("%d", seqNo))
		a.mu.Unlock()
		return &SendResult{Success: false, Error: "发送超时"}, nil
	}
}

// SendImage 发送图片。
func (a *YuanbaoAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	// 元宝需要先上传媒体，简化为发送 URL 文本
	text := caption
	if text == "" {
		text = imageURL
	} else {
		text = text + "\n" + imageURL
	}
	return a.Send(ctx, chatID, text, opts)
}

// SendTyping 发送正在输入指示。
func (a *YuanbaoAdapter) SendTyping(ctx context.Context, chatID string) error {
	// 元宝支持 heartbeat 作为 typing 指示
	return nil
}

// ───────────────────────────── 群操作 ─────────────────────────────

// QueryGroupInfo 查询群信息。
func (a *YuanbaoAdapter) QueryGroupInfo(ctx context.Context, groupCode string) (*YuanbaoGroupInfo, error) {
	// 检查缓存
	a.mu.Lock()
 cached, exists := a.groupInfoCache[groupCode]
	a.mu.Unlock()

	if exists {
		return cached, nil
	}

	// 调用 API
	body := map[string]any{
		"group_code": groupCode,
	}

	resp, err := a.callAPI(ctx, "/api/query-group-info", body, 10*time.Second)
	if err != nil {
		return nil, err
	}

	errcode := getInt(resp, "errcode", 0)
	if errcode != 0 {
		return nil, fmt.Errorf("查询失败 (errcode=%d)", errcode)
	}

	info := &YuanbaoGroupInfo{
		GroupCode:   groupCode,
		GroupName:   getString(resp, "group_name", ""),
		MemberCount: getInt(resp, "member_count", 0),
		OwnerID:     getString(resp, "owner_id", ""),
	}

	// 缓存
	a.mu.Lock()
	a.groupInfoCache[groupCode] = info
	a.mu.Unlock()

	return info, nil
}

// GetGroupMemberList 获取群成员列表。
func (a *YuanbaoAdapter) GetGroupMemberList(ctx context.Context, groupCode string) ([]YuanbaoMember, error) {
	body := map[string]any{
		"group_code": groupCode,
	}

	resp, err := a.callAPI(ctx, "/api/get-group-member-list", body, 10*time.Second)
	if err != nil {
		return nil, err
	}

	errcode := getInt(resp, "errcode", 0)
	if errcode != 0 {
		return nil, fmt.Errorf("查询失败 (errcode=%d)", errcode)
	}

	membersRaw := getListAny(resp, "members")
	var members []YuanbaoMember
	for _, m := range membersRaw {
		if mMap, ok := m.(map[string]any); ok {
			members = append(members, YuanbaoMember{
				UserID:   getString(mMap, "user_id", ""),
				Nickname: getString(mMap, "nickname", getString(mMap, "nick_name", "")),
				UserType: getInt(mMap, "user_type", getInt(mMap, "role", 0)),
			})
		}
	}

	return members, nil
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func (a *YuanbaoAdapter) nextSeqNo() int64 {
	a.mu.Lock()
	a.seqNo++
	seq := a.seqNo
	a.mu.Unlock()
	return seq
}

// callAPI 调用 HTTP API。
func (a *YuanbaoAdapter) callAPI(ctx context.Context, endpoint string, body map[string]any, timeout time.Duration) (map[string]any, error) {
	bodyBytes, _ := json.Marshal(body)

	url := a.apiDomain + endpoint
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.appID+":"+a.appSecret)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
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
func (a *YuanbaoAdapter) Name() string { return "Yuanbao" }

// PlatformType 返回平台类型。
func (a *YuanbaoAdapter) PlatformType() Platform { return PlatformYuanbao }

// EditMessage 编辑消息（元宝不支持）。
func (a *YuanbaoAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "元宝不支持编辑消息"}, nil
}

// DeleteMessage 删除消息（元宝不支持）。
func (a *YuanbaoAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("元宝不支持删除消息")
}

// SendVoice 发送语音。
func (a *YuanbaoAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "元宝语音发送需要媒体上传"}, nil
}

// SendVideo 发送视频。
func (a *YuanbaoAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "元宝视频发送需要媒体上传"}, nil
}

// SendDocument 发送文件。
func (a *YuanbaoAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "元宝文件发送需要媒体上传"}, nil
}

// MaxMessageLength 返回最大消息长度。
func (a *YuanbaoAdapter) MaxMessageLength() int { return 4000 }

// SupportsStreaming 返回是否支持流式输出。
func (a *YuanbaoAdapter) SupportsStreaming() bool { return true }

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformYuanbao,
		Name:     "Yuanbao",
		Factory:  func() PlatformAdapter { return NewYuanbaoAdapter("", "", nil) },
	})
}