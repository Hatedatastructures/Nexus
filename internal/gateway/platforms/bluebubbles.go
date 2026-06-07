// Package platforms 提供 BlueBubbles (iMessage) 平台适配器。
// 通过 BlueBubbles REST API 连接 iMessage 网络，
// 支持接收和发送 iMessage 文本及图片消息。
// 参考文档: https://bluebubbles.app/documentation/
package platforms

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	bbDefaultURL        = "http://127.0.0.1:1234"
	bbMaxMessageLength  = 10000
	bbRequestTimeout    = 30 * time.Second
	bbPollInterval      = 5 * time.Second
	bbDedupMaxSize      = 1000
	bbMaxAttachmentSize = 100 * 1024 * 1024 // 100MB
)

// ───────────────────────────── BlueBubblesAdapter ─────────────────────────────

// BlueBubblesAdapter BlueBubbles (iMessage) 平台适配器。
// 通过 BlueBubbles REST API 发送消息，轮询接收新消息。
type BlueBubblesAdapter struct {
	baseURL  string
	password string

	// HTTP 客户端
	httpClient *http.Client

	// 运行状态
	running   bool
	connected bool
	mu        sync.Mutex

	// 去重
	dedup *bbDeduplicator

	// 上次轮询的 GUID（用于增量获取）
	lastGUID string

	closeOnce sync.Once
	msgCh     chan *MessageEvent
}

// bbDeduplicator 消息去重器。
type bbDeduplicator struct {
	mu      sync.Mutex
	msgIDs  map[string]time.Time
	maxSize int
}

func newBBDeduplicator(maxSize int) *bbDeduplicator {
	return &bbDeduplicator{
		msgIDs:  make(map[string]time.Time),
		maxSize: maxSize,
	}
}

func (d *bbDeduplicator) isDuplicate(msgID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.msgIDs[msgID]; exists {
		return true
	}

	d.msgIDs[msgID] = time.Now()

	// 清理过期条目
	if len(d.msgIDs) > d.maxSize {
		oldest := ""
		oldestTime := time.Now()
		for id, t := range d.msgIDs {
			if t.Before(oldestTime) {
				oldestTime = t
				oldest = id
			}
		}
		if oldest != "" {
			delete(d.msgIDs, oldest)
		}
	}

	return false
}

// NewBlueBubblesAdapter 创建 BlueBubbles 适配器。
// 从环境变量读取 BLUEBUBBLES_URL 和 BLUEBUBBLES_PASSWORD。
func NewBlueBubblesAdapter() *BlueBubblesAdapter {
	baseURL := os.Getenv("BLUEBUBBLES_URL")
	if baseURL == "" {
		baseURL = bbDefaultURL
	}
	// 去除末尾斜杠
	baseURL = strings.TrimRight(baseURL, "/")

	password := os.Getenv("BLUEBUBBLES_PASSWORD")

	return &BlueBubblesAdapter{
		baseURL:    baseURL,
		password:   password,
		httpClient: &http.Client{Timeout: bbRequestTimeout},
		dedup:      newBBDeduplicator(bbDedupMaxSize),
	}
}

// ───────────────────────────── 连接生命周期 ─────────────────────────────

// Name 返回适配器名称。
func (a *BlueBubblesAdapter) Name() string { return "BlueBubbles" }

// PlatformType 返回平台类型枚举。
func (a *BlueBubblesAdapter) PlatformType() Platform { return PlatformBlueBubbles }

// MaxMessageLength 返回最大消息长度。
func (a *BlueBubblesAdapter) MaxMessageLength() int { return bbMaxMessageLength }

// SupportsStreaming 返回是否支持流式编辑。
func (a *BlueBubblesAdapter) SupportsStreaming() bool { return false }

// Connect 启动轮询 goroutine 接收新消息。
func (a *BlueBubblesAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	a.mu.Lock()
	a.running = true
	a.connected = true
	a.mu.Unlock()

	// 验证连接
	if err := a.ping(ctx); err != nil {
		a.mu.Lock()
		a.running = false
		a.connected = false
		a.mu.Unlock()
		return nil, fmt.Errorf("BlueBubbles 连接失败: %w", err)
	}

	a.msgCh = make(chan *MessageEvent, 100)
	go a.pollLoop(ctx, a.msgCh)

	slog.Info("[BlueBubbles] connected", "url", a.baseURL)
	return a.msgCh, nil
}

// Disconnect 停止轮询并断开连接。
func (a *BlueBubblesAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.connected = false
	a.mu.Unlock()

	slog.Info("[BlueBubbles] disconnected")
	return nil
}

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送文本消息。
func (a *BlueBubblesAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chatID 是必填项"}, nil
	}

	body := map[string]any{
		"chatGuid": chatID,
		"message":  content,
	}

	// 添加消息选项
	if opts != nil {
		if opts.ReplyToMsgID != "" {
			body["selectedMessageGuid"] = opts.ReplyToMsgID
			body["method"] = "private-api"
		}
	}

	resp, err := a.apiRequest(ctx, "POST", "/api/v1/message/text", body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, nil
	}

	// 提取消息 GUID
	data := getMap(resp, "data")
	msgGUID := ""
	if data != nil {
		msgGUID = getString(data, "guid", "")
	}

	return &SendResult{Success: true, MessageID: msgGUID}, nil
}

// EditMessage 编辑消息（BlueBubbles 不支持）。
func (a *BlueBubblesAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "BlueBubbles 不支持编辑消息"}, nil
}

// DeleteMessage 删除消息（BlueBubbles 不支持）。
func (a *BlueBubblesAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("BlueBubbles 不支持删除消息")
}

// SendTyping 发送正在输入指示。
func (a *BlueBubblesAdapter) SendTyping(ctx context.Context, chatID string) error {
	body := map[string]any{
		"chatGuid": chatID,
	}
	path := "/api/v1/chat/" + url.PathEscape(chatID) + "/typing"
	_, err := a.apiRequest(ctx, "POST", path, body)
	return err
}

// SendImage 发送图片消息。
func (a *BlueBubblesAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chatID 是必填项"}, nil
	}

	body := map[string]any{
		"chatGuid":      chatID,
		"tempImagePath": imageURL,
	}

	if caption != "" {
		body["message"] = caption
	}

	if opts != nil && opts.ReplyToMsgID != "" {
		body["selectedMessageGuid"] = opts.ReplyToMsgID
		body["method"] = "private-api"
	}

	resp, err := a.apiRequest(ctx, "POST", "/api/v1/message/attachment", body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, nil
	}

	data := getMap(resp, "data")
	msgGUID := ""
	if data != nil {
		msgGUID = getString(data, "guid", "")
	}

	return &SendResult{Success: true, MessageID: msgGUID}, nil
}

// SendVoice 发送语音消息。
func (a *BlueBubblesAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chatID 是必填项"}, nil
	}

	body := map[string]any{
		"chatGuid":      chatID,
		"tempImagePath": audioPath,
	}

	if opts != nil && opts.ReplyToMsgID != "" {
		body["selectedMessageGuid"] = opts.ReplyToMsgID
	}

	resp, err := a.apiRequest(ctx, "POST", "/api/v1/message/attachment", body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, nil
	}

	data := getMap(resp, "data")
	msgGUID := ""
	if data != nil {
		msgGUID = getString(data, "guid", "")
	}

	return &SendResult{Success: true, MessageID: msgGUID}, nil
}

// SendVideo 发送视频消息。
func (a *BlueBubblesAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chatID 是必填项"}, nil
	}

	body := map[string]any{
		"chatGuid":      chatID,
		"tempImagePath": videoPath,
	}

	if caption != "" {
		body["message"] = caption
	}

	resp, err := a.apiRequest(ctx, "POST", "/api/v1/message/attachment", body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, nil
	}

	data := getMap(resp, "data")
	msgGUID := ""
	if data != nil {
		msgGUID = getString(data, "guid", "")
	}

	return &SendResult{Success: true, MessageID: msgGUID}, nil
}

// SendDocument 发送文件消息。
func (a *BlueBubblesAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chatID 是必填项"}, nil
	}

	body := map[string]any{
		"chatGuid":      chatID,
		"tempImagePath": filePath,
	}

	if caption != "" {
		body["message"] = caption
	}

	resp, err := a.apiRequest(ctx, "POST", "/api/v1/message/attachment", body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, nil
	}

	data := getMap(resp, "data")
	msgGUID := ""
	if data != nil {
		msgGUID = getString(data, "guid", "")
	}

	return &SendResult{Success: true, MessageID: msgGUID}, nil
}

