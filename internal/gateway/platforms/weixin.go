// Package platforms 提供微信个人号平台适配器。
// 通过腾讯 iLink Bot API 连接微信个人账号。
package platforms

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	weixinBaseURL                = "https://ilinkai.weixin.qq.com"
	weixinLongPollTimeoutMs      = 35000
	weixinAPITimeoutMs           = 15000
	weixinMaxConsecutiveFailures = 3
	weixinRetryDelaySeconds      = 2
	weixinBackoffDelaySeconds    = 30
	weixinSessionExpiredErrcode  = -14
	weixinContextTokenMaxSize    = 5000
	weixinHTTPTimeout            = 40 * time.Second
)

// iLink API endpoints
const (
	weixinEpGetUpdates  = "ilink/bot/getupdates"
	weixinEpSendMessage = "ilink/bot/sendmessage"
	weixinEpSendTyping  = "ilink/bot/sendtyping"
)

// Message item types
const (
	weixinItemText = 1
)

// ───────────────────────────── WeixinAdapter ─────────────────────────────

// WeixinAdapter 微信个人号适配器。
type WeixinAdapter struct {
	accountID string
	token     string
	baseURL   string

	// HTTP 客户端
	httpClient *http.Client

	// Context token 存储 (有界)
	contextTokens  map[string]string
	contextTokenMu sync.Mutex

	// 运行状态
	running   bool
	connected bool
	mu        sync.Mutex

	// 消息通道保护
	msgCh     chan *MessageEvent
	msgMu     sync.RWMutex
	closeOnce sync.Once

	// 消息去重
	dedup *weixinDeduplicator
}

// weixinDeduplicator 消息去重器。
type weixinDeduplicator struct {
	mu      sync.Mutex
	msgIDs  map[string]time.Time
	maxSize int
}

func newWeixinDeduplicator(maxSize int) *weixinDeduplicator {
	return &weixinDeduplicator{
		msgIDs:  make(map[string]time.Time),
		maxSize: maxSize,
	}
}

func (d *weixinDeduplicator) isDuplicate(msgID string) bool {
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

// NewWeixinAdapter 创建微信个人号适配器。
func NewWeixinAdapter(accountID, token string) *WeixinAdapter {
	baseURL := os.Getenv("WEIXIN_BASE_URL")
	if baseURL == "" {
		baseURL = weixinBaseURL
	}

	return &WeixinAdapter{
		accountID:     accountID,
		token:         token,
		baseURL:       baseURL,
		httpClient:    &http.Client{Timeout: weixinHTTPTimeout},
		contextTokens: make(map[string]string),
		dedup:         newWeixinDeduplicator(1000),
	}
}

// ───────────────────────────── 连接生命周期 ─────────────────────────────

// Connect 连接到微信 iLink API。
func (a *WeixinAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if a.token == "" {
		return nil, fmt.Errorf("WEIXIN_TOKEN 是必填项")
	}

	a.mu.Lock()
	a.running = true
	a.connected = true
	a.mu.Unlock()

	// 创建消息通道
	a.msgMu.Lock()
	a.msgCh = make(chan *MessageEvent, 100)
	a.msgMu.Unlock()

	// 启动长轮询循环
	go a.pollLoop(ctx)

	slog.Info("[Weixin] connected", "account", a.accountID)
	return a.msgCh, nil
}

// Disconnect 断开连接。
func (a *WeixinAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.connected = false
	a.mu.Unlock()

	a.closeOnce.Do(func() {
		a.msgMu.Lock()
		if a.msgCh != nil {
			close(a.msgCh)
		}
		a.msgMu.Unlock()
	})

	slog.Info("[Weixin] disconnected")
	return nil
}

// ───────────────────────────── 长轮询循环 ─────────────────────────────

// pollLoop 长轮询获取消息。
func (a *WeixinAdapter) pollLoop(ctx context.Context) {
	consecutiveFailures := 0

	for {
		a.mu.Lock()
		running := a.running
		a.mu.Unlock()

		if !running {
			return
		}

		updates, err := a.getUpdates(ctx)
		if err != nil {
			consecutiveFailures++
			slog.Warn("[Weixin] failed to fetch messages", "err", err, "consecutive", consecutiveFailures)

			if consecutiveFailures >= weixinMaxConsecutiveFailures {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(weixinBackoffDelaySeconds) * time.Second):
				}
				consecutiveFailures = 0
			} else {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(weixinRetryDelaySeconds) * time.Second):
				}
			}
			continue
		}

		consecutiveFailures = 0

		for _, update := range updates {
			event := a.parseUpdate(update)
			if event != nil && !a.dedup.isDuplicate(event.MessageID) {
				if event.Source != nil {
					a.setContextToken(event.Source.UserID, getString(update, "context_token", ""))
				}
				a.msgMu.RLock()
				if a.msgCh != nil {
					select {
					case a.msgCh <- event:
					default:
						slog.Warn("[Weixin] message channel full, dropping message")
					}
				}
				a.msgMu.RUnlock()
			}
		}
	}
}

// getUpdates 调用 iLink getupdates API。
func (a *WeixinAdapter) getUpdates(ctx context.Context) ([]map[string]any, error) {
	body := map[string]any{
		"base_info": map[string]any{
			"channel_version": "2.2.0",
		},
	}

	resp, err := a.callAPI(ctx, weixinEpGetUpdates, body, weixinLongPollTimeoutMs)
	if err != nil {
		return nil, err
	}

	errcode := getInt(resp, "errcode", 0)
	if errcode == weixinSessionExpiredErrcode {
		return nil, fmt.Errorf("会话已过期，需要重新登录")
	}
	if errcode != 0 {
		errmsg := getString(resp, "errmsg", "未知错误")
		return nil, fmt.Errorf("API 错误: %s (errcode=%d)", errmsg, errcode)
	}

	items := getListAny(resp, "items")
	var updates []map[string]any
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			updates = append(updates, m)
		}
	}

	return updates, nil
}

// ───────────────────────────── 消息解析 ─────────────────────────────

// parseUpdate 解析消息更新。
func (a *WeixinAdapter) parseUpdate(update map[string]any) *MessageEvent {
	msgType := getInt(update, "msg_type", 0)
	if msgType != 1 {
		return nil
	}

	fromUser := getMap(update, "from_user")
	userID := getString(fromUser, "user_id", "")
	if userID == "" {
		return nil
	}

	msgID := getString(update, "msg_id", "")
	if msgID == "" {
		var rnd [8]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			msgID = fmt.Sprintf("%d", time.Now().UnixNano())
		} else {
			msgID = base64.RawURLEncoding.EncodeToString(rnd[:])
		}
	}

	text := ""
	items := getListAny(update, "items")
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			itemType := getInt(m, "type", 0)
			if itemType == weixinItemText {
				textBlock := getMap(m, "text")
				text = getString(textBlock, "content", "")
			}
		}
	}

	if text == "" {
		return nil
	}

	return &MessageEvent{
		Text:        text,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformWeChat,
			ChatID:   userID,
			UserID:   userID,
			ChatType: "dm",
		},
		RawMessage: update,
	}
}

// ───────────────────────────── 接口实现 ─────────────────────────────

func (a *WeixinAdapter) Name() string            { return "Weixin" }
func (a *WeixinAdapter) PlatformType() Platform  { return PlatformWeChat }
func (a *WeixinAdapter) MaxMessageLength() int   { return 2000 }
func (a *WeixinAdapter) SupportsStreaming() bool { return false }

func (a *WeixinAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "微信不支持编辑消息"}, nil
}

func (a *WeixinAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("微信不支持删除消息")
}

