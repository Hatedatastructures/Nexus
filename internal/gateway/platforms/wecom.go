// Package platforms 提供企业微信平台适配器。
// 通过 WebSocket 连接企业微信 AI Bot Gateway 进行消息收发。
package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
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
	wecomCmdSubscribe    = "aibot_subscribe"
	wecomCmdCallback     = "aibot_msg_callback"
	wecomCmdSend         = "aibot_send_msg"
	wecomCmdRespond      = "aibot_respond_msg"
	wecomCmdPing         = "ping"
	wecomCmdUploadInit   = "aibot_upload_media_init"
	wecomCmdUploadChunk  = "aibot_upload_media_chunk"
	wecomCmdUploadFinish = "aibot_upload_media_finish"
)

// 重连退避时间
var wecomReconnectBackoff = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}

// ───────────────────────────── WeComAdapter ─────────────────────────────

// WeComAdapter 企业微信适配器。
type WeComAdapter struct {
	botID          string
	secret         string
	wsURL          string
	dmPolicy       string
	allowFrom      []string
	groupPolicy    string
	groupAllowFrom []string

	// 连接状态
	conn      *websocket.Conn
	running   bool
	connected bool
	msgCh     chan *MessageEvent
	closeOnce sync.Once

	// 并发控制
	mu             sync.Mutex
	writeMu        sync.Mutex // WebSocket 写锁，保护并发 WriteJSON
	dedup          *wecomDeduplicator
	replyReqIDs    map[string]string
	lastChatReqIDs map[string]string

	// 设备 ID
	deviceID string
}

// wecomDeduplicator 消息去重器。
type wecomDeduplicator struct {
	mu      sync.Mutex
	msgIDs  map[string]time.Time
	maxSize int
}

func newWecomDeduplicator(maxSize int) *wecomDeduplicator {
	return &wecomDeduplicator{
		msgIDs:  make(map[string]time.Time),
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
			if time.Since(t) > 5*time.Minute {
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
		_ = conn.Close()
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
		"cmd":     wecomCmdSubscribe,
		"headers": map[string]any{"req_id": reqID},
		"body": map[string]any{
			"bot_id":    a.botID,
			"secret":    a.secret,
			"device_id": a.deviceID,
		},
	}

	if err := a.writeWS(conn, subscribeReq); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("发送订阅请求失败: %w", err)
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
				_ = a.conn.Close()
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
				"cmd":     wecomCmdPing,
				"headers": map[string]any{"req_id": a.newReqID("ping")},
				"body":    map[string]any{},
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
