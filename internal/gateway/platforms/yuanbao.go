// Package platforms 提供元宝平台适配器。
// 通过 WebSocket 连接元宝 Bot Gateway 进行消息收发。
package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

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
		_ = conn.Close()
	}

	slog.Info("[Yuanbao] disconnected")
	return nil
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
		_ = conn.Close()
		return nil, fmt.Errorf("发送认证请求失败: %w", err)
	}

	// 等待认证响应
	_, msg, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("读取认证响应失败: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(msg, &resp); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("解析认证响应失败: %w", err)
	}

	errcode := getInt(resp, "errcode", 0)
	if errcode != 0 {
		errmsg := getString(resp, "errmsg", "认证失败")
		_ = conn.Close()
		return nil, fmt.Errorf("认证失败: %s (errcode=%d)", errmsg, errcode)
	}

	return conn, nil
}

// ───────────────────────────── 监听循环 ─────────────────────────────
// (见 yuanbao_handlers.go)

