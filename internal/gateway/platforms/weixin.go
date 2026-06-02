// Package platforms 提供微信个人号平台适配器。
// 通过腾讯 iLink Bot API 连接微信个人账号。
package platforms

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	weixinBaseURL               = "https://ilinkai.weixin.qq.com"
	weixinLongPollTimeoutMs     = 35000
	weixinAPITimeoutMs          = 15000
	weixinMaxConsecutiveFailures = 3
	weixinRetryDelaySeconds     = 2
	weixinBackoffDelaySeconds   = 30
	weixinSessionExpiredErrcode = -14
	weixinContextTokenMaxSize   = 5000
	weixinHTTPTimeout           = 40 * time.Second
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
	contextTokens map[string]string
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

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送文本消息。
func (a *WeixinAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chat_id 是必填项"}, nil
	}

	contextToken := a.getContextToken(chatID)

	body := map[string]any{
		"base_info": map[string]any{
			"channel_version": "2.2.0",
		},
		"to_user": map[string]any{
			"user_id": chatID,
		},
		"context_token": contextToken,
		"msg_type":      2,
		"msg_state":     2,
		"items": []map[string]any{
			{
				"type": weixinItemText,
				"text": map[string]any{
					"content": content,
				},
			},
		},
	}

	resp, err := a.callAPI(ctx, weixinEpSendMessage, body, weixinAPITimeoutMs)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	errcode := getInt(resp, "errcode", 0)
	if errcode == weixinSessionExpiredErrcode {
		return &SendResult{Success: false, Error: "会话已过期"}, nil
	}
	if errcode != 0 {
		errmsg := getString(resp, "errmsg", "发送失败")
		return &SendResult{Success: false, Error: fmt.Sprintf("%s (errcode=%d)", errmsg, errcode)}, nil
	}

	newToken := getString(resp, "context_token", "")
	if newToken != "" {
		a.setContextToken(chatID, newToken)
	}

	msgID := getString(resp, "msg_id", "")
	return &SendResult{Success: true, MessageID: msgID}, nil
}

// SendImage 发送图片。
func (a *WeixinAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	text := caption
	if text == "" {
		text = imageURL
	} else {
		text = text + "\n" + imageURL
	}
	return a.Send(ctx, chatID, text, opts)
}

// SendTyping 发送正在输入指示。
func (a *WeixinAdapter) SendTyping(ctx context.Context, chatID string) error {
	body := map[string]any{
		"base_info": map[string]any{
			"channel_version": "2.2.0",
		},
		"to_user": map[string]any{
			"user_id": chatID,
		},
		"typing": 1,
	}

	_, err := a.callAPI(ctx, weixinEpSendTyping, body, weixinAPITimeoutMs)
	return err
}

// ───────────────────────────── Context Token 管理 ─────────────────────────────

func (a *WeixinAdapter) getContextToken(userID string) string {
	a.contextTokenMu.Lock()
	defer a.contextTokenMu.Unlock()
	return a.contextTokens[userID]
}

func (a *WeixinAdapter) setContextToken(userID, token string) {
	if userID == "" || token == "" {
		return
	}
	a.contextTokenMu.Lock()
	if len(a.contextTokens) > weixinContextTokenMaxSize {
		for k := range a.contextTokens {
			delete(a.contextTokens, k)
			if len(a.contextTokens) <= weixinContextTokenMaxSize/2 {
				break
			}
		}
	}
	a.contextTokens[userID] = token
	a.contextTokenMu.Unlock()
}

// ───────────────────────────── API 调用 ─────────────────────────────

// callAPI 调用 iLink API。
func (a *WeixinAdapter) callAPI(ctx context.Context, endpoint string, body map[string]any, timeoutMs int) (map[string]any, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	reqURL := a.baseURL + "/" + endpoint
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("iLink-App-Id", "bot")
	req.Header.Set("iLink-App-ClientVersion", "131072")
	req.Header.Set("X-WECHAT-UIN", generateWechatUIN())

	ctx2, cancel2 := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel2()
	req = req.WithContext(ctx2)

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

// generateWechatUIN 生成随机微信 UIN。
func generateWechatUIN() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		slog.Warn("[Weixin] crypto/rand failed, using weak fallback for UIN")
		b = fmt.Appendf(nil, "%d", time.Now().UnixNano())
	}
	return base64.StdEncoding.EncodeToString(b)
}

// ───────────────────────────── 接口实现 ─────────────────────────────

func (a *WeixinAdapter) Name() string            { return "Weixin" }
func (a *WeixinAdapter) PlatformType() Platform   { return PlatformWeChat }
func (a *WeixinAdapter) MaxMessageLength() int     { return 2000 }
func (a *WeixinAdapter) SupportsStreaming() bool   { return false }

func (a *WeixinAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "微信不支持编辑消息"}, nil
}

func (a *WeixinAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("微信不支持删除消息")
}

func (a *WeixinAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "微信语音发送需要媒体上传"}, nil
}

func (a *WeixinAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "微信视频发送需要媒体上传"}, nil
}

func (a *WeixinAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "微信文件发送需要媒体上传"}, nil
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformWeiXin,
		Name:     "Weixin",
		Factory:  func() PlatformAdapter { return NewWeixinAdapter("", "") },
	})
}
