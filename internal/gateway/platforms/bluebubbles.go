// Package platforms 提供 BlueBubbles (iMessage) 平台适配器。
// 通过 BlueBubbles REST API 连接 iMessage 网络，
// 支持接收和发送 iMessage 文本及图片消息。
// 参考文档: https://bluebubbles.app/documentation/
package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	bbDefaultURL          = "http://127.0.0.1:1234"
	bbMaxMessageLength    = 10000
	bbRequestTimeout      = 30 * time.Second
	bbPollInterval        = 5 * time.Second
	bbDedupMaxSize        = 1000
	bbMaxAttachmentSize   = 100 * 1024 * 1024 // 100MB
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
		for id, t := range d.msgIDs {
			if time.Since(t) > 10*time.Minute {
				delete(d.msgIDs, id)
			}
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

	slog.Info("[BlueBubbles] 已连接", "url", a.baseURL)
	return a.msgCh, nil
}

// Disconnect 停止轮询并断开连接。
func (a *BlueBubblesAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.connected = false
	a.mu.Unlock()

	a.closeOnce.Do(func() { close(a.msgCh) })
	slog.Info("[BlueBubbles] 已断开")
	return nil
}

// ───────────────────────────── 轮询循环 ─────────────────────────────

// pollLoop 轮询新消息。
func (a *BlueBubblesAdapter) pollLoop(ctx context.Context, msgCh chan *MessageEvent) {
	ticker := time.NewTicker(bbPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.mu.Lock()
			running := a.running
			a.mu.Unlock()

			if !running {
				return
			}

			messages, err := a.fetchNewMessages(ctx)
			if err != nil {
				slog.Warn("[BlueBubbles] 获取消息失败", "err", err)
				continue
			}

			for _, msg := range messages {
				select {
				case <-ctx.Done():
					return
				default:
					if err := a.safeSend(msgCh, msg); err != nil {
						return
					}
				}
			}
		case <-ctx.Done():
			// msgCh 由 Disconnect 通过 closeOnce 关闭
			return
		}
	}
}

// safeSend 安全发送消息到 channel，防止向已关闭 channel 发送导致 panic。
func (a *BlueBubblesAdapter) safeSend(ch chan *MessageEvent, msg *MessageEvent) error {
	defer func() {
		if r := recover(); r != nil {
			// channel 已关闭
		}
	}()
	select {
	case <-time.After(5 * time.Second):
		return fmt.Errorf("send timeout")
	case ch <- msg:
		return nil
	}
}

// fetchNewMessages 获取新消息。
func (a *BlueBubblesAdapter) fetchNewMessages(ctx context.Context) ([]*MessageEvent, error) {
	// 调用 BlueBubbles API 获取最近消息
	params := "limit=50&withChats=true&sort=DESC"
	if a.lastGUID != "" {
		params += "&afterGuid=" + a.lastGUID
	}

	resp, err := a.apiRequest(ctx, "GET", "/api/v1/message?"+params, nil)
	if err != nil {
		return nil, err
	}

	// 解析响应
	data := getMap(resp, "data")
	if data == nil {
		return nil, nil
	}

	messagesList := getListAny(data, "messages")
	if len(messagesList) == 0 {
		return nil, nil
	}

	var events []*MessageEvent
	for i := len(messagesList) - 1; i >= 0; i-- {
		msgMap, ok := messagesList[i].(map[string]any)
		if !ok {
			continue
		}

		// 去重
		msgGUID := getString(msgMap, "guid", "")
		if msgGUID == "" || a.dedup.isDuplicate(msgGUID) {
			continue
		}

		// 过滤自己发送的消息
		isFromMe, _ := msgMap["isFromMe"].(bool)
		if isFromMe {
			continue
		}

		event := a.parseMessage(msgMap)
		if event != nil {
			events = append(events, event)
			a.lastGUID = msgGUID
		}
	}

	return events, nil
}

// parseMessage 将 BlueBubbles 消息转换为 MessageEvent。
func (a *BlueBubblesAdapter) parseMessage(msg map[string]any) *MessageEvent {
	text := getString(msg, "text", "")
	msgGUID := getString(msg, "guid", "")

	// 提取聊天信息
	chats := getListAny(msg, "chats")
	chatID := ""
	chatType := "dm"
	chatName := ""

	if len(chats) > 0 {
		if chatMap, ok := chats[0].(map[string]any); ok {
			chatID = getString(chatMap, "guid", "")
			chatName = getString(chatMap, "displayName", "")
			// isGroup 判断聊天类型
			isGroup, _ := chatMap["isGroup"].(bool)
			if isGroup {
				chatType = "group"
			}
		}
	}

	// 提取发送者
	handle := getMap(msg, "handle")
	userID := ""
	userName := ""
	if handle != nil {
		userID = getString(handle, "id", "")
		userName = getString(handle, "address", "")
	}

	// 确定消息类型
	msgType := MsgText
	if text == "" {
		// 检查是否有附件
		attachments := getListAny(msg, "attachments")
		if len(attachments) > 0 {
			if attMap, ok := attachments[0].(map[string]any); ok {
				mimeType := getString(attMap, "mimeType", "")
				if strings.HasPrefix(mimeType, "image/") {
					msgType = MsgPhoto
				} else if strings.HasPrefix(mimeType, "audio/") {
					msgType = MsgVoice
				} else if strings.HasPrefix(mimeType, "video/") {
					msgType = MsgVideo
				} else {
					msgType = MsgDocument
				}
			}
		}
	}

	// 提取时间戳
	var timestamp time.Time
	if dateCreated, ok := msg["dateCreated"].(float64); ok && dateCreated > 0 {
		// BlueBubbles 时间戳是毫秒级 Unix 时间
		timestamp = time.UnixMilli(int64(dateCreated))
	}

	return &MessageEvent{
		Text:        text,
		MessageType: msgType,
		MessageID:   msgGUID,
		Source: &SessionSource{
			Platform: PlatformBlueBubbles,
			ChatID:   chatID,
			ChatName: chatName,
			ChatType: chatType,
			UserID:   userID,
			UserName: userName,
		},
		Timestamp:  timestamp,
		RawMessage: msg,
	}
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
	path := fmt.Sprintf("/api/v1/chat/%s/typing", chatID)
	_, err := a.apiRequest(ctx, "POST", path, body)
	return err
}

// SendImage 发送图片消息。
func (a *BlueBubblesAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chatID 是必填项"}, nil
	}

	body := map[string]any{
		"chatGuid":    chatID,
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
		"chatGuid":    chatID,
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
		"chatGuid":    chatID,
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
		"chatGuid":    chatID,
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

// ───────────────────────────── API 调用 ─────────────────────────────

// apiRequest 发送 API 请求并返回结果。
func (a *BlueBubblesAdapter) apiRequest(ctx context.Context, method, path string, body map[string]any) (map[string]any, error) {
	reqURL := a.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		if method == "GET" {
			// GET 请求: 将参数编码到 URL 查询字符串
			q := url.Values{}
			for k, v := range body {
				q.Set(k, fmt.Sprintf("%v", v))
			}
			if len(q) > 0 {
				if strings.Contains(reqURL, "?") {
					reqURL += "&" + q.Encode()
				} else {
					reqURL += "?" + q.Encode()
				}
			}
		} else {
			// POST 请求: JSON 编码请求体
			data, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("序列化请求体失败: %w", err)
			}
			bodyReader = bytes.NewReader(data)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")

	// 添加密码认证（查询参数）
	if a.password != "" {
		q := req.URL.Query()
		q.Set("password", a.password)
		req.URL.RawQuery = q.Encode()
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	// 检查 API 错误
	status := getString(result, "status", "")
	if status == "error" {
		errMsg := getString(result, "error", "未知错误")
		return nil, fmt.Errorf("API 错误: %s", errMsg)
	}

	return result, nil
}

// ping 验证与 BlueBubbles 服务器的连接。
func (a *BlueBubblesAdapter) ping(ctx context.Context) error {
	resp, err := a.apiRequest(ctx, "GET", "/api/v1/ping", nil)
	if err != nil {
		return fmt.Errorf("ping 失败: %w", err)
	}

	data := getMap(resp, "data")
	if data == nil {
		return fmt.Errorf("ping 响应无效")
	}

	// 检查是否需要密码
	encrypted, _ := data["encrypted"].(bool)
	if encrypted && a.password == "" {
		return fmt.Errorf("BlueBubbles 服务器需要密码，请设置 BLUEBUBBLES_PASSWORD")
	}

	return nil
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformBlueBubbles,
		Name:     "BlueBubbles",
		Factory:  func() PlatformAdapter { return NewBlueBubblesAdapter() },
	})
}
