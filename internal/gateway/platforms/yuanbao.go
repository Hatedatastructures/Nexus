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

	"crypto/rand"

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
	yuanbaoCacheMaxSize      = 500
	yuanbaoPendingMaxSize    = 200
)

// WebSocket 命令类型 (简化版)
const (
	yuanbaoCmdAuthBind  = "AUTH_BIND"
	yuanbaoCmdPing      = "PING"
	yuanbaoCmdPong      = "PONG"
	yuanbaoCmdT05       = "T05" // 接收消息
	yuanbaoCmdT06       = "T06" // 发送消息
	yuanbaoCmdHeartbeat = "HEARTBEAT"
)

// ───────────────────────────── YuanbaoAdapter ─────────────────────────────

// YuanbaoAdapter 元宝平台适配器。
type YuanbaoAdapter struct {
	appID     string
	appSecret string
	botID     string
	wsURL     string
	apiDomain string

	// HTTP 客户端 (复用)
	httpClient *http.Client

	// WebSocket 连接
	conn      *websocket.Conn
	running   bool
	connected bool

	// 并发控制
	mu           sync.Mutex
	writeMu      sync.Mutex
	pendingResps map[string]chan map[string]any
	dedup        *yuanbaoDeduplicator
	instanceID   string
	seqNo        int64
	closeOnce    sync.Once

	// 群信息缓存 (有界)
	groupInfoCache map[string]*yuanbaoCacheEntry
}

// yuanbaoCacheEntry 带过期时间的缓存条目。
type yuanbaoCacheEntry struct {
	info      *YuanbaoGroupInfo
	expiresAt time.Time
}

// YuanbaoGroupInfo 群信息。
type YuanbaoGroupInfo struct {
	GroupCode   string `json:"group_code"`
	GroupName   string `json:"group_name"`
	MemberCount int    `json:"member_count"`
	OwnerID     string `json:"owner_id"`
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
func NewYuanbaoAdapter(appID, appSecret string) *YuanbaoAdapter {
	wsURL := os.Getenv("YUANBAO_WS_URL")
	if wsURL == "" {
		wsURL = yuanbaoDefaultWSURL
	}

	apiDomain := os.Getenv("YUANBAO_API_DOMAIN")
	if apiDomain == "" {
		apiDomain = yuanbaoDefaultAPIDomain
	}

	return &YuanbaoAdapter{
		appID:          appID,
		appSecret:      appSecret,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		wsURL:          wsURL,
		apiDomain:      apiDomain,
		pendingResps:   make(map[string]chan map[string]any),
		dedup:          newYuanbaoDeduplicator(1000),
		instanceID:     generateCryptoID(),
		seqNo:          0,
		groupInfoCache: make(map[string]*yuanbaoCacheEntry),
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
	go a.heartbeatLoop(ctx)

	slog.Info("[Yuanbao] connected", "bot_id", a.botID)
	return msgCh, nil
}

// Disconnect 断开连接。
func (a *YuanbaoAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.connected = false
	conn := a.conn
	a.conn = nil
	a.mu.Unlock()

	if conn != nil {
		conn.Close()
	}

	slog.Info("[Yuanbao] disconnected")
	return nil
}

// getSignToken 获取认证 token。
func (a *YuanbaoAdapter) getSignToken(ctx context.Context) (string, string, error) {
	body := map[string]any{
		"app_id":      a.appID,
		"app_secret":  a.appSecret,
		"instance_id": a.instanceID,
	}

	resp, err := a.callAPI(ctx, "/api/sign-token", body)
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
		"cmd":         yuanbaoCmdAuthBind,
		"token":       token,
		"instance_id": a.instanceID,
		"seq_no":      a.nextSeqNo(),
	}

	a.writeMu.Lock()
	err = conn.WriteJSON(authReq)
	a.writeMu.Unlock()
	if err != nil {
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
			a.closeOnce.Do(func() { close(msgCh) })
			return
		}

		if conn == nil {
			// 尝试重连
			time.Sleep(5 * time.Second)
			reconnectCtx, reconnectCancel := context.WithTimeout(context.Background(), yuanbaoConnectTimeout)

			token, _, err := a.getSignToken(reconnectCtx)
			if err != nil {
				reconnectCancel()
				slog.Warn("[Yuanbao] failed to get token", "err", err)
				continue
			}

			newConn, err := a.openConnection(reconnectCtx, token)
			reconnectCancel()
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
		event := a.parseMessage(payload)
		if event != nil && !a.dedup.isDuplicate(event.MessageID) {
			select {
			case msgCh <- event:
			default:
				slog.Warn("[Yuanbao] message channel full, dropping message")
			}
		}
	case yuanbaoCmdPong:
		return
	default:
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
		b := make([]byte, 8)
		if _, err := rand.Read(b); err == nil {
			msgID = fmt.Sprintf("%x", b)
		} else {
			msgID = fmt.Sprintf("%d", time.Now().UnixNano())
		}
	}

	fromUser := getMap(body, "from_user")
	userID := getString(fromUser, "user_id", "")

	chatID := getString(body, "chat_id", userID)
	chatType := getString(body, "chat_type", "c2c")
	isGroup := chatType == "group"

	text := ""
	msgType := getString(body, "msg_type", "text")
	if msgType == "text" {
		textBlock := getMap(body, "text")
		text = getString(textBlock, "content", "")
	}

	if text == "" {
		return nil
	}

	if isGroup && text != "" {
		text = strings.TrimSpace(strings.Replace(text, "@"+a.botID, "", 1))
	}

	return &MessageEvent{
		Text:        text,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformYuanbao,
			ChatID:   chatID,
			UserID:   userID,
			ChatType: chatType,
		},
		RawMessage: payload,
	}
}

// ───────────────────────────── 心跳循环 ─────────────────────────────

// heartbeatLoop 发送心跳。
func (a *YuanbaoAdapter) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(yuanbaoHeartbeatInterval)
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
				"cmd":    yuanbaoCmdPing,
				"seq_no": a.nextSeqNo(),
			}

			a.writeMu.Lock()
			if err := conn.WriteJSON(pingReq); err != nil {
				slog.Debug("[Yuanbao] heartbeat send failed", "err", err)
			}
			a.writeMu.Unlock()
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
		"cmd":       yuanbaoCmdT06,
		"seq_no":    seqNo,
		"chat_id":   chatID,
		"chat_type": chatType,
		"msg_type":  "text",
		"text": map[string]any{
			"content": content,
		},
	}

	// 注册 pending response
	respCh := make(chan map[string]any, 1)
	seqNoStr := fmt.Sprintf("%d", seqNo)
	a.mu.Lock()
	if len(a.pendingResps) > yuanbaoPendingMaxSize {
		for k := range a.pendingResps {
			delete(a.pendingResps, k)
			if len(a.pendingResps) <= yuanbaoPendingMaxSize/2 {
				break
			}
		}
	}
	a.pendingResps[seqNoStr] = respCh
	a.mu.Unlock()

	a.writeMu.Lock()
	if err := conn.WriteJSON(req); err != nil {
		a.writeMu.Unlock()
		a.mu.Lock()
		delete(a.pendingResps, seqNoStr)
		a.mu.Unlock()
		return &SendResult{Success: false, Error: fmt.Sprintf("发送失败: %v", err)}, nil
	}
	a.writeMu.Unlock()

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
		delete(a.pendingResps, seqNoStr)
		a.mu.Unlock()
		return &SendResult{Success: false, Error: "发送超时"}, nil
	}
}

// SendImage 发送图片。
func (a *YuanbaoAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
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
	return nil
}

// ───────────────────────────── 群操作 ─────────────────────────────

// QueryGroupInfo 查询群信息。
func (a *YuanbaoAdapter) QueryGroupInfo(ctx context.Context, groupCode string) (*YuanbaoGroupInfo, error) {
	// 检查缓存
	a.mu.Lock()
	cached, exists := a.groupInfoCache[groupCode]
	if exists && time.Now().Before(cached.expiresAt) {
		info := cached.info
		a.mu.Unlock()
		return info, nil
	}
	a.mu.Unlock()

	// 调用 API
	body := map[string]any{
		"group_code": groupCode,
	}

	resp, err := a.callAPI(ctx, "/api/query-group-info", body)
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

	// 缓存 (带 TTL + 有界驱逐)
	a.mu.Lock()
	if len(a.groupInfoCache) > yuanbaoCacheMaxSize {
		for k, v := range a.groupInfoCache {
			if time.Now().After(v.expiresAt) {
				delete(a.groupInfoCache, k)
			}
		}
	}
	a.groupInfoCache[groupCode] = &yuanbaoCacheEntry{
		info:      info,
		expiresAt: time.Now().Add(10 * time.Minute),
	}
	a.mu.Unlock()

	return info, nil
}

// GetGroupMemberList 获取群成员列表。
func (a *YuanbaoAdapter) GetGroupMemberList(ctx context.Context, groupCode string) ([]YuanbaoMember, error) {
	body := map[string]any{
		"group_code": groupCode,
	}

	resp, err := a.callAPI(ctx, "/api/get-group-member-list", body)
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

// generateCryptoID 生成加密安全的随机 ID。
func generateCryptoID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// callAPI 调用 HTTP API (复用 httpClient)。
func (a *YuanbaoAdapter) callAPI(ctx context.Context, endpoint string, body map[string]any) (map[string]any, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	url := a.apiDomain + endpoint
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.appID+":"+a.appSecret)

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

// ───────────────────────────── 接口实现 ─────────────────────────────

func (a *YuanbaoAdapter) Name() string            { return "Yuanbao" }
func (a *YuanbaoAdapter) PlatformType() Platform   { return PlatformYuanbao }
func (a *YuanbaoAdapter) MaxMessageLength() int     { return 4000 }
func (a *YuanbaoAdapter) SupportsStreaming() bool   { return true }

func (a *YuanbaoAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "元宝不支持编辑消息"}, nil
}

func (a *YuanbaoAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("元宝不支持删除消息")
}

func (a *YuanbaoAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "元宝语音发送需要媒体上传"}, nil
}

func (a *YuanbaoAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "元宝视频发送需要媒体上传"}, nil
}

func (a *YuanbaoAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "元宝文件发送需要媒体上传"}, nil
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformYuanbao,
		Name:     "Yuanbao",
		Factory:  func() PlatformAdapter { return NewYuanbaoAdapter("", "") },
	})
}
